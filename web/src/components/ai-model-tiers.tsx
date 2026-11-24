import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useId, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { useI18n } from "@/i18n"
import { admin, type Entitlement } from "@/lib/api"

const kinds = ["text", "vision"] as const
const indexes = [0, 1, 2] as const
type Kind = (typeof kinds)[number]
type Profile = {
  version: number
  models: Record<Kind, string[]>
  defaults: Record<Kind, string>
}

const initialModels = {
  text: ["gpt-6-astra", "gpt-6.1-sol", "gpt-6-luna"],
  vision: ["gpt-6-astra", "gpt-6.1-terra", "gpt-6-luna"],
}

function readProfile(value: string): Profile | null {
  try {
    const profile = JSON.parse(value)
    if (
      !Number.isSafeInteger(profile.version) ||
      profile.version < 1 ||
      !kinds.every((kind) => Array.isArray(profile.models?.[kind]) && typeof profile.defaults?.[kind] === "string")
    ) {
      return null
    }
    return profile
  } catch {
    return null
  }
}

export function AIModelTiers({ entitlement }: { entitlement: Entitlement }) {
  const { locale, t } = useI18n()
  const cn = locale === "zh"
  const tiers = cn ? ["高性能", "中性能", "低性能"] : ["High", "Medium", "Low"]
  const qc = useQueryClient()
  const fieldId = useId()
  const [snapshot, setSnapshot] = useState<{ value: string; profile: Profile } | null>(null)
  const [models, setModels] = useState(initialModels)
  const [defaults, setDefaults] = useState({ text: 1, vision: 1 })
  const valid = kinds.every(
    (kind) =>
      models[kind].every((id) => !!id.trim() && [...id.trim()].length <= 200) &&
      new Set(models[kind].map((id) => id.trim())).size === 3,
  )
  const old = readProfile(entitlement.value)
  const save = useMutation({
    mutationFn: () => {
      if (!snapshot || !valid) throw new Error(cn ? "请填写三个不同模型" : "Enter three distinct models.")
      const text = models.text.map((id) => id.trim())
      const vision = models.vision.map((id) => id.trim())
      const next = {
        ...snapshot.profile,
        version: Math.max(3, snapshot.profile.version + 1),
        models: { text, vision },
        defaults: { text: text[defaults.text], vision: vision[defaults.vision] },
      }
      return admin.updateEntitlement(entitlement.id, { value: JSON.stringify(next), expected_value: snapshot.value })
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setSnapshot(null)
    },
  })
  function open(profile: Profile) {
    const existing = profile.version >= 3 && kinds.every((kind) => profile.models[kind].length === 3)
    setModels(existing ? profile.models : initialModels)
    setDefaults(
      existing
        ? {
            text: Math.max(0, profile.models.text.indexOf(profile.defaults.text)),
            vision: Math.max(0, profile.models.vision.indexOf(profile.defaults.vision)),
          }
        : { text: 1, vision: 1 },
    )
    save.reset()
    setSnapshot({ value: entitlement.value, profile })
  }
  if (!old) return <span className="ml-2 text-muted-foreground">{cn ? "配置无效" : "Invalid profile"}</span>
  return (
    <>
      <Button type="button" size="sm" variant="ghost" className="ml-2" onClick={() => open(old)}>
        {cn ? "配置模型档位" : "Configure model tiers"}
      </Button>
      <span className="ml-2 text-muted-foreground">v{old.version}</span>
      <Dialog
        open={snapshot !== null}
        onOpenChange={(visible) => {
          if (!visible && !save.isPending) setSnapshot(null)
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{cn ? "套餐模型档位" : "Package model tiers"}</DialogTitle>
            <DialogDescription>
              {cn
                ? "填写吉吉实际可用的模型 ID。用户刷新授权后获取新版配置。"
                : "Enter Jiji model IDs. The new configuration arrives with a license refresh."}
            </DialogDescription>
          </DialogHeader>
          <form
            className="grid gap-4"
            onSubmit={(event) => {
              event.preventDefault()
              if (valid && !save.isPending) save.mutate()
            }}
          >
            {kinds.map((kind) => (
              <fieldset key={kind} className="grid gap-2 rounded border p-3" disabled={save.isPending}>
                <legend className="px-1 font-medium">
                  {kind === "text" ? (cn ? "文本" : "Text") : cn ? "视觉" : "Vision"}
                </legend>
                {indexes.map((index) => (
                  <label
                    key={index}
                    htmlFor={`${fieldId}-${kind}-${index}`}
                    className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-2 text-sm"
                  >
                    <span>{tiers[index]}</span>
                    <Input
                      id={`${fieldId}-${kind}-${index}`}
                      required
                      maxLength={200}
                      value={models[kind][index]}
                      onChange={(event) => {
                        const value = event.target.value
                        setModels((current) => ({
                          ...current,
                          [kind]: current[kind].map((id, i) => (i === index ? value : id)),
                        }))
                      }}
                    />
                  </label>
                ))}
                <label className="flex items-center gap-2 text-sm">
                  {cn ? "默认档位" : "Default tier"}
                  <select
                    className="rounded border bg-background p-1"
                    value={defaults[kind]}
                    onChange={(event) => setDefaults((current) => ({ ...current, [kind]: Number(event.target.value) }))}
                  >
                    {indexes.map((index) => (
                      <option key={index} value={index}>
                        {tiers[index]}
                      </option>
                    ))}
                  </select>
                </label>
              </fieldset>
            ))}
            {save.isError && (
              <p role="alert" className="text-sm text-destructive">
                {save.error.message}
              </p>
            )}
            <div className="flex justify-end">
              <Button type="submit" disabled={!valid || save.isPending}>
                {t("common.save")}
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}
