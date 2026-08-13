import { Check, Copy } from "lucide-react"
import { useState } from "react"
import { showToast } from "@/components/toast"
import { Button } from "@/components/ui/button"
import { useI18n } from "@/i18n"

export function EntityId({ label, value }: { label: string; value: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      showToast(t("common.idCopied", { label }), "success")
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      showToast(t("common.copyFailed"), "error")
    }
  }

  return (
    <div className="mt-1 flex max-w-md items-center gap-1 text-[11px] text-muted-foreground">
      <span className="shrink-0">{label}</span>
      <code className="min-w-0 break-all font-mono leading-tight">{value}</code>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="h-6 w-6 shrink-0"
        onClick={copy}
        title={t("common.copyId", { label })}
        aria-label={t("common.copyId", { label })}
      >
        {copied ? <Check className="h-3 w-3 text-emerald-600" /> : <Copy className="h-3 w-3" />}
      </Button>
    </div>
  )
}
