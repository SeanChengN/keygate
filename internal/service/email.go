package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/tabloy/keygate/internal/branding"
	"github.com/tabloy/keygate/internal/crypto"
	"github.com/tabloy/keygate/internal/store"
	"github.com/uptrace/bun"
)

// emailFooter returns the attribution footer appended to all outgoing emails.
func emailFooter() string { return branding.EmailFooter }

type EmailService struct {
	environment      smtpConfig
	logger           *slog.Logger
	store            *store.Store
	smtpAEAD         *crypto.AESGCM
	previousSMTPAEAD *crypto.AESGCM
	tlsConfig        *tls.Config // Test override. Production uses verified TLS.
}

func (s *EmailService) IsConfigured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.effectiveSMTP(ctx)
	return err == nil
}

func NewEmailService(host, port, username, password, from string, logger *slog.Logger, db *store.Store) *EmailService {
	if port == "" {
		port = "587"
	}
	return &EmailService{
		environment: smtpConfig{host: host, port: port, username: username, password: password, from: from},
		logger:      logger, store: db,
	}
}

// getTemplate returns the custom template from DB settings if it exists, otherwise the default.
func (s *EmailService) getTemplate(key, defaultTmpl string) string {
	if s.store == nil {
		return defaultTmpl
	}
	custom, err := s.store.GetSetting(context.Background(), "email_template_"+key)
	if err != nil || custom == "" {
		return defaultTmpl
	}
	return custom
}

// DefaultTemplates returns all default email templates keyed by their setting suffix.
func DefaultTemplates() map[string]string {
	return map[string]string{
		"license_created":   tmplLicenseCreated,
		"license_expiring":  tmplLicenseExpiring,
		"license_expired":   tmplLicenseExpired,
		"trial_expired":     tmplTrialExpired,
		"license_suspended": tmplLicenseSuspended,
		"quota_warning":     tmplQuotaWarning,
		"seat_invite":       tmplSeatInvite,
		"admin_invite":      tmplAdminInvite,
		"payment_failed":    tmplPaymentFailed,
	}
}

func (s *EmailService) Send(to, subject, htmlBody string) error {
	err := s.SendConfigured(context.Background(), to, subject, htmlBody)
	if errors.Is(err, ErrSMTPNotConfigured) {
		return nil // Mail remains optional for existing best-effort callers.
	}
	return err
}

func (s *EmailService) SendConfigured(ctx context.Context, to, subject, htmlBody string) error {
	config, err := s.effectiveSMTP(ctx)
	if err != nil {
		return err
	}
	return s.deliver(ctx, config, to, subject, htmlBody, true)
}

func (s *EmailService) deliver(ctx context.Context, config smtpConfig, to, subject, htmlBody string, retry bool) error {
	if !strings.Contains(htmlBody, emailFooter()) {
		if strings.Contains(htmlBody, "</body>") {
			htmlBody = strings.Replace(htmlBody, "</body>", emailFooter()+"</body>", 1)
		} else {
			htmlBody += emailFooter()
		}
	}
	if validateSMTPLine(subject) != nil || validateSMTPLine(config.from) != nil || validateSMTPLine(to) != nil {
		return &smtpFailure{code: "EMAIL_ENVELOPE_FAILED", err: errors.New("invalid SMTP header")}
	}
	msg := strings.Join([]string{
		"From: " + config.from,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"",
		htmlBody,
	}, "\r\n")
	addr := net.JoinHostPort(config.host, config.port)
	err := s.sendOnce(ctx, config, addr, to, []byte(msg))
	// Retry only before a message could have been accepted by the server.
	if retry && SMTPFailureCode(err) == "EMAIL_CONNECTION_FAILED" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			err = s.sendOnce(ctx, config, addr, to, []byte(msg))
		}
	}
	if err != nil {
		code := SMTPFailureCode(err)
		s.logger.Warn("email delivery failed", "code", code)
		return &smtpFailure{code: code, err: errors.New("SMTP delivery did not complete")}
	}
	return nil
}

type smtpFailure struct {
	code string
	err  error
}

func (e *smtpFailure) Error() string { return e.code + ": " + e.err.Error() }
func (e *smtpFailure) Unwrap() error { return e.err }

func SMTPFailureCode(err error) string {
	var failure *smtpFailure
	switch {
	case err == nil:
		return ""
	case errors.As(err, &failure):
		return failure.code
	case errors.Is(err, ErrSMTPNotConfigured):
		return "EMAIL_NOT_CONFIGURED"
	case errors.Is(err, ErrSMTPInvalidSettings), errors.Is(err, ErrSMTPEncryption):
		return "EMAIL_INVALID_SETTINGS"
	}
	return "EMAIL_SEND_FAILED"
}

// sendOnce supports implicit TLS on port 465 and STARTTLS on submission
// ports. Authenticated sessions always verify TLS before sending credentials.
func (s *EmailService) sendOnce(ctx context.Context, config smtpConfig, addr, to string, msg []byte) error {
	fail := func(code string, err error) error { return &smtpFailure{code: code, err: err} }
	from, err := parseEnvelopeAddress(config.from)
	if err != nil {
		return fail("EMAIL_ENVELOPE_FAILED", err)
	}
	if err := validateSMTPLine(to); err != nil {
		return fail("EMAIL_ENVELOPE_FAILED", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fail("EMAIL_CONNECTION_FAILED", err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fail("EMAIL_CONNECTION_FAILED", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	tlsConfig := s.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: config.host, MinVersion: tls.VersionTLS12}
	}
	var transport net.Conn = conn
	encrypted := config.port == "465"
	if encrypted {
		secured := tls.Client(conn, tlsConfig)
		if err := secured.HandshakeContext(ctx); err != nil {
			return fail("EMAIL_TLS_FAILED", err)
		}
		transport = secured
	}
	client, err := smtp.NewClient(transport, config.host)
	if err != nil {
		return fail("EMAIL_CONNECTION_FAILED", err)
	}
	defer client.Close()
	if err := client.Hello(localHostname()); err != nil {
		return fail("EMAIL_CONNECTION_FAILED", err)
	}
	if ok, _ := client.Extension("STARTTLS"); !encrypted && ok {
		if err := client.StartTLS(tlsConfig); err != nil {
			return fail("EMAIL_TLS_FAILED", err)
		}
		encrypted = true
	}

	if !encrypted && (config.username != "" || config.port == "587") {
		return fail("EMAIL_TLS_FAILED", errors.New("server does not support TLS"))
	}
	if config.username != "" {
		auth, err := pickAuth(client, config.host, config.username, config.password)
		if err != nil {
			return fail("EMAIL_AUTH_FAILED", err)
		}
		if err := client.Auth(auth); err != nil {
			return fail("EMAIL_AUTH_FAILED", fmt.Errorf("auth: %w", err))
		}
	}
	if err := client.Mail(from); err != nil {
		return fail("EMAIL_ENVELOPE_FAILED", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fail("EMAIL_ENVELOPE_FAILED", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fail("EMAIL_SEND_FAILED", err)
	}
	if _, err := writer.Write(msg); err != nil {
		return fail("EMAIL_SEND_FAILED", err)
	}
	if err := writer.Close(); err != nil {
		return fail("EMAIL_SEND_FAILED", err)
	}
	// DATA succeeded. A QUIT error cannot undo the accepted delivery.
	_ = client.Quit()
	return nil
}

// pickAuth selects an SMTP AUTH mechanism based on what the server
// advertised in its post-TLS EHLO response. Preference order:
//
//  1. PLAIN — standard, single round-trip, supported by Gmail,
//     SendGrid, Mailgun, Postmark, SES, Postfix.
//  2. LOGIN — non-standard but widely supported; the ONLY mechanism
//     Office 365 / Outlook / Exchange Online accepts for basic auth.
//
// Returns an error if the server requires AUTH but advertises neither
// (typical for OAuth2-only endpoints — those need XOAUTH2 which is a
// separate authentication flow).
func pickAuth(c *smtp.Client, host, username, password string) (smtp.Auth, error) {
	// Client.Extension returns (ok, params): ok = is the extension
	// supported, params = the parameter string (for AUTH this is the
	// space-separated list of mechanisms the server accepts).
	ok, authExt := c.Extension("AUTH")
	if !ok {
		return nil, errors.New("smtp: server does not advertise AUTH (configure SMTP_USERNAME='' for anonymous relays)")
	}
	mechs := strings.ToUpper(authExt)
	switch {
	case strings.Contains(mechs, "PLAIN"):
		return smtp.PlainAuth("", username, password, host), nil
	case strings.Contains(mechs, "LOGIN"):
		return &loginAuth{username: username, password: password, host: host}, nil
	default:
		return nil, fmt.Errorf("smtp: no supported AUTH mechanism (server advertised: %q)", authExt)
	}
}

// loginAuth implements RFC-less SMTP AUTH LOGIN. Microsoft 365 /
// Outlook.com / Exchange Online reject AUTH PLAIN with "504 5.7.4
// Unrecognized authentication type" so we need this fallback.
//
// LOGIN is base64 challenge-response: server sends "Username:" then
// "Password:" (both base64-encoded over the wire, but Client.Auth
// decodes before passing to Next). Some servers omit the trailing
// colon, send lowercase, or use a different word — match leniently.
type loginAuth struct{ username, password, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// Only allow LOGIN over TLS. The credentials go on the wire in
	// (base64 of) plaintext — anything else is a credential leak.
	if !server.TLS {
		return "", nil, errors.New("smtp: refusing LOGIN auth on unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("smtp: wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	// Normalise the prompt: outer trim, drop trailing colon, inner
	// trim (so " Username : " → "username"), lower-case. Tolerant
	// of the variants seen across Microsoft / Postfix / Exim.
	prompt := strings.ToLower(strings.TrimSpace(
		strings.TrimRight(strings.TrimSpace(string(fromServer)), ":"),
	))
	switch prompt {
	case "username", "user name":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("smtp: unexpected LOGIN challenge: %q", fromServer)
	}
}

// parseEnvelopeAddress extracts the bare RFC 5321 address used for
// the SMTP MAIL FROM command. Accepts both forms operators commonly
// put in SMTP_FROM:
//
//	"noreply@example.com"               → noreply@example.com
//	"Keygate <noreply@example.com>"     → noreply@example.com
//	"\"Keygate Billing\" <a@b.com>"     → a@b.com
//
// The display-name form is kept verbatim in the message's "From:"
// header (built in Send) so recipients still see "Keygate"; only the
// envelope is stripped to the bare address.
func parseEnvelopeAddress(from string) (string, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", errors.New("empty address")
	}
	a, err := mail.ParseAddress(from)
	if err != nil {
		// Fall back: maybe it's already a bare addr-spec that
		// ParseAddress is being strict about (rare). Validate the
		// minimal shape before giving up.
		if strings.Count(from, "@") == 1 && !strings.ContainsAny(from, "<> ") {
			return from, nil
		}
		return "", err
	}
	return a.Address, nil
}

// validateSMTPLine rejects strings containing CR or LF so callers
// can't smuggle additional SMTP commands through a header value.
func validateSMTPLine(s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return errors.New("smtp: line contains CR or LF")
	}
	return nil
}

// localHostname returns the EHLO argument. Some strict servers reject
// "localhost" or empty values; "[127.0.0.1]" is universally accepted.
func localHostname() string { return "[127.0.0.1]" }

func (s *EmailService) SendLicenseCreated(to, productName, planName, licenseKey string) {
	body := renderTemplate(s.getTemplate("license_created", tmplLicenseCreated), map[string]string{
		"Product":    productName,
		"Plan":       planName,
		"LicenseKey": licenseKey,
	})
	go func() {
		if err := s.Send(to, "Your license for "+productName, body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", "Your license for "+productName, "error", err)
		}
	}()
}

func (s *EmailService) SendLicenseExpiring(to, productName, licenseKey, expiresAt string) {
	body := renderTemplate(s.getTemplate("license_expiring", tmplLicenseExpiring), map[string]string{
		"Product":    productName,
		"LicenseKey": licenseKey,
		"ExpiresAt":  expiresAt,
	})
	go func() {
		if err := s.Send(to, productName+" license expiring soon", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", productName+" license expiring soon", "error", err)
		}
	}()
}

func (s *EmailService) SendQuotaWarning(to, productName, feature string, used, limit int64, pct int) {
	body := renderTemplate(s.getTemplate("quota_warning", tmplQuotaWarning), map[string]any{
		"Product": productName,
		"Feature": feature,
		"Used":    used,
		"Limit":   limit,
		"Pct":     pct,
	})
	subject := fmt.Sprintf("%s: %s quota at %d%%", productName, feature, pct)
	go func() {
		if err := s.Send(to, subject, body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", subject, "error", err)
		}
	}()
}

// SendSeatInvite delivers the claim link. acceptURL is the absolute
// URL pointing at the portal accept-invite page; if the template
// uses {{InviteURL}} the link renders inline, otherwise we append a
// plain "Accept the invitation: <URL>" block so even a template
// admin who forgot to add the placeholder still ships a working
// link.
func (s *EmailService) SendSeatInvite(to, productName, inviterName, acceptURL string) {
	tmpl := s.getTemplate("seat_invite", tmplSeatInvite)
	body := renderTemplate(tmpl, map[string]string{
		"Product":   productName,
		"Inviter":   inviterName,
		"InviteURL": acceptURL,
	})
	// Defensive fallback: if the rendered body doesn't already
	// contain the claim URL (custom template missed the placeholder),
	// append it so the recipient still has a way in.
	if acceptURL != "" && !strings.Contains(body, acceptURL) {
		body += `<p style="margin-top:24px;font-size:13px;color:#555;">Accept the invitation: <a href="` + acceptURL + `">` + acceptURL + `</a></p>`
	}
	go func() {
		if err := s.Send(to, "You've been invited to "+productName, body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", "You've been invited to "+productName, "error", err)
		}
	}()
}

func (s *EmailService) SendLicenseExpired(to, productName string) {
	body := renderTemplate(s.getTemplate("license_expired", tmplLicenseExpired), map[string]string{
		"Product": productName,
	})
	go func() {
		if err := s.Send(to, productName+" license expired", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", productName+" license expired", "error", err)
		}
	}()
}

func (s *EmailService) SendTrialExpired(to, productName string) {
	body := renderTemplate(s.getTemplate("trial_expired", tmplTrialExpired), map[string]string{
		"Product": productName,
	})
	go func() {
		if err := s.Send(to, productName+" trial has ended", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", productName+" trial has ended", "error", err)
		}
	}()
}

func (s *EmailService) SendLicenseSuspended(to, productName, reason string) {
	body := renderTemplate(s.getTemplate("license_suspended", tmplLicenseSuspended), map[string]string{
		"Product": productName,
		"Reason":  reason,
	})
	go func() {
		if err := s.Send(to, productName+" license suspended", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", productName+" license suspended", "error", err)
		}
	}()
}

func (s *EmailService) SendSubscriptionCanceled(to, productName string, immediate bool) {
	var tmpl string
	if immediate {
		tmpl = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Subscription Canceled</h2>
<p>Your <strong>` + productName + `</strong> subscription has been canceled immediately.</p>
<p>Your access has ended. Thank you for being a customer.</p>
</body></html>`
	} else {
		tmpl = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Subscription Will Be Canceled</h2>
<p>Your <strong>` + productName + `</strong> subscription will be canceled at the end of the current billing period.</p>
<p>You can continue using the service until then.</p>
</body></html>`
	}
	go func() {
		if err := s.Send(to, productName+" subscription canceled", tmpl); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendPaymentFailed(to, productName string) {
	body := renderTemplate(s.getTemplate("payment_failed", tmplPaymentFailed), map[string]string{
		"Product": productName,
	})
	go func() {
		if err := s.Send(to, productName+" payment failed", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", productName+" payment failed", "error", err)
		}
	}()
}

func (s *EmailService) SendDunningSecond(to, productName string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Payment Still Outstanding</h2>
<p>We've been unable to process your payment for <strong>` + productName + `</strong> for over a week.</p>
<p>Please update your payment method to avoid losing access.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" — payment still outstanding", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendDunningFinal(to, productName string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #dc2626;">Final Notice — Access Will Be Suspended</h2>
<p>Your <strong>` + productName + `</strong> payment has been overdue for 14 days.</p>
<p>Your access will be suspended soon if payment is not received.</p>
<p>Please update your payment method immediately.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" — final payment notice", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

// SendAdminInvite notifies a Keygate platform operator that they
// were added (or promoted) to the admin team. The "invite" is
// really a role grant — the recipient can log in via email-OTP
// immediately and the email's job is just to tell them that
// happened.
//
// Best-effort: a delivery failure does NOT roll back the role
// grant on the InviteTeamMember handler. The recipient can still
// learn out-of-band that they're admin.
func (s *EmailService) SendAdminInvite(to, siteName, inviterName, role, loginURL string) {
	body := renderTemplate(s.getTemplate("admin_invite", tmplAdminInvite), map[string]string{
		"SiteName": siteName,
		"Inviter":  inviterName,
		"Role":     role,
		"LoginURL": loginURL,
	})
	go func() {
		subj := "You've been added to " + siteName
		if err := s.Send(to, subj, body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "subject", subj, "error", err)
		}
	}()
}

// SendPaymentRecovered closes the dunning loop: customer fixed the
// card mid-grace and the subscription is back to active. Without
// this, the last touch the user has from us is "payment failed",
// which makes a successful retry feel silent.
func (s *EmailService) SendPaymentRecovered(to, productName string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #059669;">Payment Recovered — Thanks!</h2>
<p>Your payment for <strong>` + productName + `</strong> went through. Your subscription is active again, and access is fully restored.</p>
<p>No further action required.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" — payment recovered", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendWelcome(to, name string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Welcome to Keygate!</h2>
<p>Hi ` + name + `, your account has been created.</p>
<p>You can manage your licenses and subscriptions from your portal.</p>
</body></html>`
	go func() {
		if err := s.Send(to, "Welcome to Keygate", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendOTPCode(to, code string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Your login code</h2>
<p>Enter this code to sign in:</p>
<div style="background: #f4f4f5; border-radius: 8px; padding: 16px; margin: 16px 0; font-family: monospace; font-size: 32px; text-align: center; letter-spacing: 8px; font-weight: bold;">` + code + `</div>
<p style="color: #666; font-size: 14px;">This code expires in 10 minutes. If you didn't request this, ignore this email.</p>
</body></html>`
	go func() {
		if err := s.Send(to, "Your login code", body); err != nil {
			s.logger.Error("OTP email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendPlanChanged(to, productName, oldPlan, newPlan string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Plan Changed</h2>
<p>Your <strong>` + productName + `</strong> plan has been changed from <strong>` + oldPlan + `</strong> to <strong>` + newPlan + `</strong>.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" plan changed", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendRenewalReminder(to, productName, renewalDate string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Renewal Reminder</h2>
<p>Your <strong>` + productName + `</strong> subscription will renew on <strong>` + renewalDate + `</strong>.</p>
<p>No action is needed if you'd like to continue.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" renewal coming up", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendPaymentActionRequired(to, productName, invoiceURL string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Payment Authentication Required</h2>
<p>Your payment for <strong>` + productName + `</strong> requires additional authentication.</p>
<p><a href="` + invoiceURL + `" style="display:inline-block;background:#2563eb;color:white;padding:10px 24px;border-radius:6px;text-decoration:none;">Complete Payment</a></p>
<p style="color:#666;font-size:14px;">If you don't complete this step, your subscription may be interrupted.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" — payment authentication required", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

func (s *EmailService) SendTrialEnding(to, productName, trialEnd string) {
	body := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2>Your Trial is Ending Soon</h2>
<p>Your <strong>` + productName + `</strong> trial ends on <strong>` + trialEnd + `</strong>.</p>
<p>After the trial, your subscription will begin automatically. No action needed if you'd like to continue.</p>
<p style="color:#666;font-size:14px;">If you'd like to cancel, you can do so from your account portal before the trial ends.</p>
</body></html>`
	go func() {
		if err := s.Send(to, productName+" trial ending soon", body); err != nil {
			s.logger.Error("email delivery failed", "to", to, "error", err)
		}
	}()
}

// StartEmailQueueProcessor processes queued emails periodically.
func (s *EmailService) StartEmailQueueProcessor(ctx context.Context, db *store.Store) {
	// Process immediately on start
	s.processQueue(ctx, db)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processQueue(ctx, db)
		}
	}
}

func (s *EmailService) processQueue(ctx context.Context, db *store.Store) {
	for i := 0; i < 20 && ctx.Err() == nil; i++ {
		config, err := s.effectiveSMTP(ctx)
		if err != nil {
			return // Leave pending mail intact until SMTP becomes usable.
		}
		found := false
		err = db.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			email, err := store.LockPendingEmail(ctx, tx)
			if err != nil || email == nil {
				return err
			}
			found = true
			err = s.deliver(ctx, config, email.ToAddr, email.Subject, email.Body, false)
			return store.RecordEmailAttempt(ctx, tx, email.ID, SMTPFailureCode(err))
		})
		if err != nil || !found {
			return
		}
	}
}

func renderTemplate(tmplStr string, data any) string {
	t, err := template.New("email").Parse(tmplStr)
	if err != nil {
		return tmplStr
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return tmplStr
	}
	return buf.String()
}

const tmplLicenseCreated = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #111;">Your {{.Product}} License</h2>
<p>Your <strong>{{.Plan}}</strong> license is ready.</p>
<div style="background: #f4f4f5; border-radius: 8px; padding: 16px; margin: 16px 0; font-family: monospace; font-size: 18px; text-align: center; letter-spacing: 2px;">
{{.LicenseKey}}
</div>
<p style="color: #666; font-size: 14px;">Keep this key safe. You'll need it to activate your software.</p>
</body></html>`

const tmplLicenseExpiring = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #111;">License Expiring Soon</h2>
<p>Your <strong>{{.Product}}</strong> license expires on <strong>{{.ExpiresAt}}</strong>.</p>
<p>License key: <code>{{.LicenseKey}}</code></p>
<p>Please renew to avoid service interruption.</p>
</body></html>`

const tmplQuotaWarning = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Quota Warning: {{.Feature}}</h2>
<p>Your <strong>{{.Product}}</strong> {{.Feature}} usage is at <strong>{{.Pct}}%</strong>.</p>
<p>Used: {{.Used}} / {{.Limit}}</p>
<p>Consider upgrading your plan to avoid interruptions.</p>
</body></html>`

const tmplSeatInvite = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #111;">You've Been Invited</h2>
<p><strong>{{.Inviter}}</strong> has invited you to join <strong>{{.Product}}</strong>.</p>
<p style="margin: 24px 0;">
  <a href="{{.InviteURL}}" style="display: inline-block; padding: 10px 20px; background: #2563eb; color: white; text-decoration: none; border-radius: 6px;">Accept the invitation</a>
</p>
<p style="font-size: 12px; color: #666;">Or paste this link into your browser: <a href="{{.InviteURL}}">{{.InviteURL}}</a></p>
<p style="font-size: 12px; color: #999;">This link expires in 7 days.</p>
</body></html>`

const tmplAdminInvite = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #111;">You've been added to {{.SiteName}}</h2>
<p><strong>{{.Inviter}}</strong> added you as a <strong>{{.Role}}</strong> on the <strong>{{.SiteName}}</strong> admin team.</p>
<p>Sign in with this email using the email-OTP login to access the admin panel:</p>
<p style="margin: 24px 0;">
  <a href="{{.LoginURL}}" style="display: inline-block; padding: 10px 20px; background: #2563eb; color: white; text-decoration: none; border-radius: 6px;">Sign in</a>
</p>
<p style="font-size: 12px; color: #666;">Or paste this link into your browser: <a href="{{.LoginURL}}">{{.LoginURL}}</a></p>
</body></html>`

const tmplLicenseSuspended = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #dc2626;">License Suspended</h2>
<p>Your <strong>{{.Product}}</strong> license has been suspended.</p>
{{if .Reason}}<p>Reason: {{.Reason}}</p>{{end}}
</body></html>`

const tmplPaymentFailed = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Payment Failed</h2>
<p>We couldn't process your payment for <strong>{{.Product}}</strong>.</p>
<p>Please update your payment method to avoid service interruption.</p>
</body></html>`

const tmplLicenseExpired = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #dc2626;">License Expired</h2>
<p>Your <strong>{{.Product}}</strong> license has expired.</p>
<p>Please renew your subscription to continue using the software.</p>
</body></html>`

const tmplTrialExpired = `<!DOCTYPE html>
<html><body style="font-family: -apple-system, sans-serif; max-width: 600px; margin: 0 auto; padding: 20px;">
<h2 style="color: #d97706;">Trial Period Ended</h2>
<p>Your <strong>{{.Product}}</strong> trial has ended.</p>
<p>Subscribe to a paid plan to continue using all features.</p>
</body></html>`
