package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var blockedWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func isPublicWebhookIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return false
	}
	for _, prefix := range blockedWebhookPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func validateWebhookURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return errors.New("webhook URL is invalid")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("webhook URL must use http or https")
	}
	if u.User != nil {
		return errors.New("webhook URL must not contain credentials")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addrs) == 0 {
		return errors.New("webhook hostname cannot be resolved")
	}
	for _, addr := range addrs {
		if !isPublicWebhookIP(addr) {
			return fmt.Errorf("webhook hostname resolves to a private or special address")
		}
	}
	return nil
}

func newWebhookHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: min(timeout, 10*time.Second), KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", strings.Trim(host, "[]"))
			if err != nil || len(addrs) == 0 {
				return nil, errors.New("webhook hostname cannot be resolved")
			}
			for _, addr := range addrs {
				if !isPublicWebhookIP(addr) {
					return nil, errors.New("webhook destination is private or special")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("webhook redirects are disabled")
		},
	}
}

// ValidateWebhookURL performs the same DNS/address policy used again at dial
// time. Validating twice prevents both unsafe configuration and DNS rebinding.
func ValidateWebhookURL(ctx context.Context, rawURL string) error {
	return validateWebhookURL(ctx, rawURL)
}
