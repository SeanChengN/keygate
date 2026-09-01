import { KeyRound, Mail, ShieldCheck, Terminal } from "lucide-react"
import { useEffect, useState } from "react"
import { Navigate } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { useAuth } from "@/hooks/use-auth"
import { useSiteConfig } from "@/hooks/use-site-config"
import { useI18n } from "@/i18n"
import { auth } from "@/lib/api"

export default function LoginPage() {
  const { t } = useI18n()
  const { site_name, logo_url, attribution_text, attribution_url } = useSiteConfig()
  const { user, loading, refetch } = useAuth()
  const [devLogin, setDevLogin] = useState(false)
  const [devEmail, setDevEmail] = useState("admin@keygate.dev")
  const [devName, setDevName] = useState("Admin")
  const [devLoading, setDevLoading] = useState(false)
  const [devError, setDevError] = useState("")
  const [authMode, setAuthMode] = useState<"otp" | "admin">("admin")
  const [otpAvailable, setOtpAvailable] = useState(true)
  const [adminEmail, setAdminEmail] = useState("")
  const [adminPassword, setAdminPassword] = useState("")
  const [adminLoading, setAdminLoading] = useState(false)
  const [adminError, setAdminError] = useState("")
  const [recovering, setRecovering] = useState(false)
  const [recoveryCode, setRecoveryCode] = useState("")
  const [newPassword, setNewPassword] = useState("")

  // OTP state
  const [otpStep, setOtpStep] = useState<"email" | "code">("email")
  const [otpEmail, setOtpEmail] = useState("")
  const [otpCode, setOtpCode] = useState("")
  const [otpLoading, setOtpLoading] = useState(false)
  const [otpError, setOtpError] = useState("")
  const [otpCooldown, setOtpCooldown] = useState(0)

  useEffect(() => {
    auth
      .providers()
      .then((r) => {
        setDevLogin(r.dev_login)
        setOtpAvailable(r.otp)
        if (!r.admin_password && r.otp) setAuthMode("otp")
      })
      .catch(() => {})
  }, [])

  // Cooldown timer for resend
  useEffect(() => {
    if (otpCooldown <= 0) return
    const timer = setTimeout(() => setOtpCooldown(otpCooldown - 1), 1000)
    return () => clearTimeout(timer)
  }, [otpCooldown])

  const handleOtpSend = async (e: React.FormEvent) => {
    e.preventDefault()
    setOtpLoading(true)
    setOtpError("")
    try {
      await auth.otpSend(otpEmail)
      setOtpStep("code")
      setOtpCooldown(60)
    } catch (err) {
      setOtpError(err instanceof Error ? err.message : t("login.failed"))
    } finally {
      setOtpLoading(false)
    }
  }

  const handleOtpResend = async () => {
    if (otpCooldown > 0) return
    setOtpLoading(true)
    setOtpError("")
    try {
      await auth.otpSend(otpEmail)
      setOtpCooldown(60)
    } catch (err) {
      setOtpError(err instanceof Error ? err.message : t("login.failed"))
    } finally {
      setOtpLoading(false)
    }
  }

  const handleOtpVerify = async (e: React.FormEvent) => {
    e.preventDefault()
    setOtpLoading(true)
    setOtpError("")
    try {
      await auth.otpVerify(otpEmail, otpCode)
      await refetch()
    } catch (err) {
      setOtpError(err instanceof Error ? err.message : t("login.failed"))
    } finally {
      setOtpLoading(false)
    }
  }

  const handleDevLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setDevLoading(true)
    setDevError("")
    try {
      await auth.devLogin(devEmail, devName)
      await refetch()
    } catch (err) {
      setDevError(err instanceof Error ? err.message : t("login.failed"))
    } finally {
      setDevLoading(false)
    }
  }

  const handleAdminLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setAdminLoading(true)
    setAdminError("")
    try {
      if (recovering) await auth.adminRecover(adminEmail, recoveryCode, newPassword)
      else await auth.adminPasswordLogin(adminEmail, adminPassword)
      await refetch()
    } catch (err) {
      setAdminError(err instanceof Error ? err.message : t("login.failed"))
    } finally {
      setAdminLoading(false)
    }
  }

  if (loading) return null
  if (user) return <Navigate to={user.is_admin ? "/admin" : "/portal"} replace />

  return (
    <div className="flex flex-col items-center justify-center min-h-screen bg-muted/30">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <div className="flex justify-center mb-2">
            <img src={logo_url || "/logo.svg"} alt={site_name} className="h-12 w-12" />
          </div>
          <CardTitle className="text-2xl">{site_name}</CardTitle>
          <CardDescription>{t("login.subtitle")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="grid grid-cols-2 rounded-lg bg-muted p-1" role="tablist" aria-label={t("login.method")}>
            <button
              type="button"
              role="tab"
              aria-selected={authMode === "admin"}
              className={`rounded-md px-3 py-2 text-sm font-medium transition-colors ${authMode === "admin" ? "bg-background shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setAuthMode("admin")}
            >
              {t("login.adminPassword")}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={authMode === "otp"}
              disabled={!otpAvailable}
              className={`rounded-md px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${authMode === "otp" ? "bg-background shadow-sm" : "text-muted-foreground"}`}
              onClick={() => setAuthMode("otp")}
            >
              {t("login.emailCode")}
            </button>
          </div>

          {authMode === "admin" && (
            <form onSubmit={handleAdminLogin} className="space-y-3">
              <div className="flex items-center gap-2 rounded-md border border-primary/20 bg-primary/5 px-3 py-2 text-xs text-muted-foreground">
                <ShieldCheck className="h-4 w-4 shrink-0 text-primary" />
                {recovering ? t("login.recoveryHint") : t("login.adminPrivateHint")}
              </div>
              <div className="space-y-2">
                <Label>{t("common.email")}</Label>
                <Input
                  type="email"
                  value={adminEmail}
                  onChange={(e) => setAdminEmail(e.target.value)}
                  required
                  autoFocus
                />
              </div>
              {recovering ? (
                <>
                  <div className="space-y-2">
                    <Label>{t("login.recoveryCode")}</Label>
                    <Input
                      className="font-mono uppercase"
                      value={recoveryCode}
                      onChange={(e) => setRecoveryCode(e.target.value)}
                      required
                    />
                  </div>
                  <div className="space-y-2">
                    <Label>{t("login.newPassword")}</Label>
                    <Input
                      type="password"
                      minLength={16}
                      maxLength={128}
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      required
                    />
                  </div>
                </>
              ) : (
                <div className="space-y-2">
                  <Label>{t("login.password")}</Label>
                  <Input
                    type="password"
                    value={adminPassword}
                    onChange={(e) => setAdminPassword(e.target.value)}
                    required
                  />
                </div>
              )}
              {adminError && <p className="text-sm text-destructive">{adminError}</p>}
              <Button type="submit" className="w-full" disabled={adminLoading}>
                <KeyRound className="mr-2 h-4 w-4" />
                {adminLoading ? t("login.signingIn") : recovering ? t("login.recoverAccess") : t("login.signIn")}
              </Button>
              <button
                type="button"
                className="w-full text-center text-xs text-muted-foreground hover:text-foreground"
                onClick={() => {
                  setRecovering(!recovering)
                  setAdminError("")
                }}
              >
                {recovering ? t("login.backToPassword") : t("login.useRecoveryCode")}
              </button>
            </form>
          )}

          {/* OTP Email Step */}
          {authMode === "otp" && otpStep === "email" && (
            <form onSubmit={handleOtpSend} className="space-y-3">
              <div className="space-y-2">
                <Label>{t("common.email")}</Label>
                <Input
                  type="email"
                  placeholder="you@example.com"
                  value={otpEmail}
                  onChange={(e) => setOtpEmail(e.target.value)}
                  required
                  autoFocus
                />
              </div>
              {otpError && <p className="text-sm text-destructive">{otpError}</p>}
              <Button type="submit" className="w-full" disabled={otpLoading}>
                <Mail className="h-4 w-4 mr-2" />
                {otpLoading ? t("login.sendingCode") : t("login.sendCode")}
              </Button>
            </form>
          )}

          {/* OTP Code Step */}
          {authMode === "otp" && otpStep === "code" && (
            <form onSubmit={handleOtpVerify} className="space-y-3">
              <p className="text-sm text-muted-foreground text-center">{t("login.codeSentTo", { email: otpEmail })}</p>
              <div className="space-y-2">
                <Input
                  type="text"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  maxLength={6}
                  placeholder="000000"
                  value={otpCode}
                  onChange={(e) => setOtpCode(e.target.value.replace(/\D/g, ""))}
                  required
                  autoFocus
                  className="text-center text-2xl tracking-[0.5em] font-mono"
                />
              </div>
              {otpError && <p className="text-sm text-destructive">{otpError}</p>}
              <Button type="submit" className="w-full" disabled={otpLoading || otpCode.length !== 6}>
                {otpLoading ? t("login.verifying") : t("login.verify")}
              </Button>
              <div className="flex justify-between text-xs">
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground"
                  onClick={() => {
                    setOtpStep("email")
                    setOtpCode("")
                    setOtpError("")
                  }}
                >
                  {t("login.changeEmail")}
                </button>
                <button
                  type="button"
                  className="text-muted-foreground hover:text-foreground disabled:opacity-50"
                  disabled={otpCooldown > 0}
                  onClick={handleOtpResend}
                >
                  {otpCooldown > 0 ? t("login.resendIn", { seconds: String(otpCooldown) }) : t("login.resendCode")}
                </button>
              </div>
            </form>
          )}

          {/* Dev Login (development only) */}
          {devLogin && otpStep === "email" && (
            <>
              <div className="relative my-4">
                <Separator />
                <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-card px-2 text-xs text-muted-foreground">
                  DEV MODE
                </span>
              </div>
              <form onSubmit={handleDevLogin} className="space-y-3">
                <div className="space-y-2">
                  <Label>{t("common.email")}</Label>
                  <Input type="email" value={devEmail} onChange={(e) => setDevEmail(e.target.value)} required />
                </div>
                <div className="space-y-2">
                  <Label>{t("common.name")}</Label>
                  <Input value={devName} onChange={(e) => setDevName(e.target.value)} />
                </div>
                {devError && <p className="text-sm text-destructive">{devError}</p>}
                <Button type="submit" className="w-full" disabled={devLoading}>
                  <Terminal className="h-4 w-4 mr-2" />
                  {devLoading ? t("login.signingIn") : t("login.devLogin")}
                </Button>
                <p className="text-xs text-muted-foreground text-center">{t("login.devNote")}</p>
              </form>
            </>
          )}
        </CardContent>
      </Card>
      {/* Attribution required by AGPL v3 Section 7(b) — see NOTICE */}
      <a
        href={attribution_url}
        target="_blank"
        rel="noopener noreferrer"
        className="mt-4 text-[10px] text-muted-foreground/40 hover:text-muted-foreground transition-colors"
      >
        {attribution_text}
      </a>
    </div>
  )
}
