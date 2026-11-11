import { useMutation, useQuery } from "@tanstack/react-query"
import { ChevronDown, MoreHorizontal } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useI18n } from "@/i18n"
import type en from "@/i18n/locales/en"

type TranslationKeys = keyof typeof en

import { type AILimits, type AIProvisionPreview, licenseAI } from "@/lib/api"

const empty: AILimits = { enabled: false, group_id: 0, quota: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }
const labels = {
  group_id: "ai.group",
  quota: "ai.quota",
  rate_limit_5h: "ai.rate5h",
  rate_limit_1d: "ai.rate1d",
  rate_limit_7d: "ai.rate7d",
}
const states: Record<string, string> = {
  pending: "ai.pending",
  running: "ai.pending",
  synced: "ai.synced",
  done: "ai.done",
  failed: "ai.failed",
  uncertain: "ai.uncertain",
  ready: "ai.ready",
  attention: "ai.attention",
  skipped: "ai.skipped",
  removed: "ai.removed",
}
function useAIState() {
  const { t } = useI18n()
  return (state: string) => (states[state] ? t(states[state] as TranslationKeys) : state)
}
function Limits({
  value,
  onChange,
  fixedGroup = false,
}: {
  value: AILimits
  onChange: (value: AILimits) => void
  fixedGroup?: boolean
}) {
  const { t } = useI18n()
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      {Object.entries(labels).map(([key, label]) => (
        <Label key={key} className="grid gap-2">
          {t(label as TranslationKeys)}
          <Input
            type="number"
            min="0"
            max="1000000"
            step={key === "group_id" ? "1" : "any"}
            required
            disabled={key === "group_id" && fixedGroup}
            value={value[key as keyof typeof labels]}
            onChange={(e) => onChange({ ...value, [key]: Number(e.target.value) })}
          />
        </Label>
      ))}
    </div>
  )
}
function Failure({ error }: { error: Error | null }) {
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {error.message}
    </p>
  ) : null
}

export function AIAccountDialog({ id }: { id: string }) {
  const { t } = useI18n()
  const stateLabel = useAIState()
  const [open, setOpen] = useState(false),
    [enabled, setEnabled] = useState(false)
  const [access, setAccess] = useState(""),
    [refresh, setRefresh] = useState(""),
    [expires, setExpires] = useState("")
  const query = useQuery({
    queryKey: ["ai-account", id],
    queryFn: () => licenseAI.account(id),
    enabled: open,
    retry: false,
  })
  useEffect(() => {
    if (query.data) setEnabled(query.data.enabled)
  }, [query.data])
  const save = useMutation({
    mutationFn: () => {
      const body = {
        enabled,
        ...(access || refresh
          ? { access_token: access, refresh_token: refresh, expires_at: Math.floor(new Date(expires).getTime() / 1000) }
          : {}),
      }
      setAccess("")
      setRefresh("")
      return licenseAI.saveAccount(id, body)
    },
    onSuccess: () => {
      void query.refetch()
    },
  })
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        {t("ai.account")}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) {
            setAccess("")
            setRefresh("")
            save.reset()
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("ai.accountTitle")}</DialogTitle>
            <DialogDescription>{t("ai.accountDescription")}</DialogDescription>
          </DialogHeader>
          <p>
            {t("ai.status")}：{query.data?.state ? stateLabel(query.data.state) : t("ai.unconfigured")}
          </p>
          <Failure error={query.error} />
          <Failure error={save.error} />
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              save.mutate()
            }}
          >
            <Label className="flex gap-2">
              <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
              {t("ai.enableSync")}
            </Label>
            <Label className="grid gap-2">
              Access token
              <Input type="password" autoComplete="off" value={access} onChange={(e) => setAccess(e.target.value)} />
            </Label>
            <Label className="grid gap-2">
              Refresh token
              <Input type="password" autoComplete="off" value={refresh} onChange={(e) => setRefresh(e.target.value)} />
            </Label>
            <Label className="grid gap-2">
              {t("ai.tokenExpiry")}
              <Input
                type="datetime-local"
                required={!!(access || refresh)}
                value={expires}
                onChange={(e) => setExpires(e.target.value)}
              />
            </Label>
            <Button disabled={save.isPending || query.isPending || query.isError}>{t("common.save")}</Button>
            {save.isSuccess && <p role="status">{t("ai.saved")}</p>}
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function AIPlanDialog({ id }: { id: string }) {
  const { t } = useI18n()
  const stateLabel = useAIState()
  const [dirty, setDirty] = useState(false)
  const [confirmSave, setConfirmSave] = useState(false)
  const [preview, setPreview] = useState<AIProvisionPreview | null>(null)
  const [batchId, setBatchId] = useState("")
  const batchKey = useRef("")
  const [open, setOpen] = useState(false),
    [value, setValue] = useState(empty)
  const query = useQuery({ queryKey: ["ai-plan", id], queryFn: () => licenseAI.plan(id), enabled: open, retry: false })
  const batches = useQuery({
    queryKey: ["ai-batches", id],
    queryFn: () => licenseAI.batches(id),
    enabled: open,
    refetchInterval: 5000,
    retry: false,
  })
  useEffect(() => {
    if (!batchId && batches.data?.batches[0]) setBatchId(batches.data.batches[0].id)
  }, [batchId, batches.data])
  useEffect(() => {
    if (query.data && !dirty) setValue(query.data)
  }, [query.data, dirty])
  const previewMutation = useMutation({
    mutationFn: () => licenseAI.preview(id),
    onSuccess: (v) => {
      batchKey.current = ""
      setPreview(v)
    },
  })
  const provision = useMutation({
    mutationFn: () => {
      batchKey.current ||= crypto.randomUUID()
      return licenseAI.provisionBatch(
        id,
        preview!.revision,
        preview!.items.filter((i) => i.eligible).map((i) => i.id),
        batchKey.current,
      )
    },
    onSuccess: (b) => {
      setBatchId(b.id)
      void batches.refetch()
      batchKey.current = ""
      setPreview(null)
    },
  })
  const batch = useQuery({
    queryKey: ["ai-batch", id, batchId],
    queryFn: () => licenseAI.batch(id, batchId),
    enabled: !!batchId,
    refetchInterval: 5000,
    retry: false,
  })
  const save = useMutation({
    mutationFn: () => licenseAI.savePlan(id, value),
    onSuccess: () => {
      setDirty(false)
      setPreview(null)
      void query.refetch()
    },
  })
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        {t("ai.planTitle")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t("ai.planTitle")}</DialogTitle>
            <DialogDescription>{t("ai.planDescription")}</DialogDescription>
          </DialogHeader>
          <Failure error={query.error} />
          <Failure error={save.error} />
          <Failure error={previewMutation.error || provision.error || batch.error} />
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              setConfirmSave(true)
            }}
          >
            <Label className="flex gap-2">
              <input
                type="checkbox"
                checked={value.enabled}
                onChange={(e) => {
                  setDirty(true)
                  setValue({ ...value, enabled: e.target.checked })
                }}
              />
              {t("ai.autoProvision")}
            </Label>
            <Limits
              value={value}
              onChange={(v) => {
                setDirty(true)
                setValue(v)
              }}
            />
            <Button disabled={save.isPending || query.isPending || query.isError}>{t("ai.saveSync")}</Button>
            {save.isSuccess && <p role="status">{t("ai.saved")}</p>}
          </form>
          <p className="text-sm text-muted-foreground">
            {t("ai.revision")} {query.data?.revision || 0} ·{" "}
            {query.data?.summary?.map((s) => `${stateLabel(s.state)}: ${s.count}`).join(" / ")}
          </p>
          <Button
            variant="outline"
            disabled={dirty || previewMutation.isPending}
            onClick={() => previewMutation.mutate()}
          >
            {t("ai.previewBatch")}
          </Button>
          {preview && (
            <div className="space-y-3 rounded-lg border p-4">
              <p>
                {t("ai.batchWarning", {
                  count: preview.items.filter((i) => i.eligible).length,
                  quota: preview.config.quota,
                })}
              </p>
              <ul className="max-h-40 overflow-auto text-xs">
                {preview.items.map((i) => (
                  <li key={i.id}>
                    {i.id} ·{" "}
                    {i.eligible
                      ? t("ai.eligible")
                      : t(i.reason === "already_bound" ? "ai.alreadyBound" : "ai.unavailable")}
                  </li>
                ))}
              </ul>
              <Button
                disabled={provision.isPending || !preview.items.some((i) => i.eligible)}
                onClick={() => provision.mutate()}
              >
                {t("ai.confirmBatch")}
              </Button>
            </div>
          )}
          {batch.data && (
            <div role="status" className="max-h-44 overflow-auto text-sm">
              {batch.data.items.map((i) => (
                <p key={i.id}>
                  {i.id} · {stateLabel(i.state)}
                </p>
              ))}
            </div>
          )}
          {!!batches.data?.batches.length && (
            <Select value={batchId} onValueChange={setBatchId}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {batches.data.batches.map((b) => (
                  <SelectItem key={b.id} value={b.id}>
                    {b.id.slice(0, 8)} · {t("ai.revision")} {b.revision}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <AlertDialog open={confirmSave} onOpenChange={setConfirmSave}>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t("ai.saveSync")}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t("ai.planConfirm", { count: query.data?.summary?.reduce((n, s) => n + s.count, 0) || 0 })}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <div className="flex justify-end gap-2">
                <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
                <AlertDialogAction onClick={() => save.mutate()}>{t("ai.confirm")}</AlertDialogAction>
              </div>
            </AlertDialogContent>
          </AlertDialog>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function LicenseAICard({ id }: { id: string }) {
  const { t } = useI18n()
  const stateLabel = useAIState()
  const [dirty, setDirty] = useState(false)
  const provisionKey = useRef("")
  const query = useQuery({
    queryKey: ["license-ai", id],
    queryFn: () => licenseAI.get(id),
    refetchInterval: 5000,
    retry: false,
  })
  const [value, setValue] = useState(empty),
    [confirm, setConfirm] = useState("")
  const keys = useRef<Record<string, string>>({})
  const binding = query.data?.binding
  const limits = binding?.config
  useEffect(() => {
    if (limits && !dirty) setValue(limits)
  }, [limits, dirty])
  const provision = useMutation({
    mutationFn: () => {
      provisionKey.current ||= crypto.randomUUID()
      return licenseAI.provision(id, provisionKey.current)
    },
    onSuccess: () => {
      provisionKey.current = ""
      void query.refetch()
    },
  })
  const save = useMutation({
    mutationFn: () => licenseAI.save(id, value),
    onSuccess: () => {
      setDirty(false)
      void query.refetch()
    },
  })
  const action = useMutation({
    mutationFn: (name: string) => {
      keys.current[name] ||= crypto.randomUUID()
      return licenseAI.action(id, name, keys.current[name])
    },
    onSuccess: (_, name) => {
      delete keys.current[name]
      void query.refetch()
    },
  })
  const actions: Record<string, string> = {
    reset_quota: t("ai.resetQuota"),
    reset_rate: t("ai.resetRate"),
    rotate: t("ai.rotate"),
    retry: t("ai.retry"),
  }
  const busy = save.isPending || action.isPending
  return (
    <Card className="border-0 shadow-none">
      <CardContent className="space-y-5 px-0 py-2">
        <div className="flex items-center justify-between">
          <h3 className="font-semibold">{t("ai.account")}</h3>
          {binding && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm">
                  <MoreHorizontal className="mr-2 h-4 w-4" />
                  {t("ai.operations")}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {Object.entries(actions).map(([name, label]) => (
                  <DropdownMenuItem key={name} disabled={busy} onSelect={() => setConfirm(name)}>
                    {label}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
        <Failure error={query.error} />
        <Failure error={save.error} />
        <Failure error={action.error} />
        <Failure error={provision.error} />
        {query.isPending ? (
          <p>{t("ai.loading")}</p>
        ) : !query.data?.enrolled ? (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">{t("ai.unbound")}</p>
            <Button disabled={provision.isPending} onClick={() => setConfirm("provision")}>
              {t("ai.provision")}
            </Button>
          </div>
        ) : (
          binding && (
            <>
              <div className="flex items-center justify-between gap-3">
                <Badge
                  variant={binding.state === "failed" || binding.state === "uncertain" ? "destructive" : "secondary"}
                >
                  {stateLabel(binding.state)}
                </Badge>
                <span className="text-xs text-muted-foreground">
                  {t("ai.revision")} {binding.revision}
                </span>
              </div>
              <dl className="grid grid-cols-1 gap-x-6 gap-y-4 rounded-lg bg-muted/40 p-4 text-sm sm:grid-cols-2">
                <div>
                  <dt className="text-muted-foreground">Key ID</dt>
                  <dd className="mt-1 font-medium">{binding.remote_id || "—"}</dd>
                </div>
                <div>
                  <dt className="text-muted-foreground">{t("ai.used")}</dt>
                  <dd className="mt-1 font-medium">
                    {binding.remote.quota_used ?? "—"} / {binding.config.quota} USD
                  </dd>
                </div>
                <div className="sm:col-span-2">
                  <dt className="text-muted-foreground">{t("ai.expiry")}</dt>
                  <dd className="mt-1">
                    {binding.remote.expires_at ? new Date(binding.remote.expires_at).toLocaleString() : "—"}
                  </dd>
                </div>
              </dl>
              {binding.last_error && (
                <p role="alert" className="text-destructive">
                  {binding.last_error}
                </p>
              )}
              <p className="text-sm text-muted-foreground">
                {t("ai.anchor", { zone: binding.timezone, date: new Date(binding.anchor).toLocaleString() })}
              </p>
              <details className="group rounded-lg border border-border p-4">
                <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-medium [&::-webkit-details-marker]:hidden">
                  {t("ai.adjust")}
                  <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
                </summary>
                <form
                  className="space-y-3 mt-3"
                  onSubmit={(e) => {
                    e.preventDefault()
                    save.mutate()
                  }}
                >
                  <Limits
                    value={value}
                    onChange={(v) => {
                      setDirty(true)
                      setValue(v)
                    }}
                  />
                  <Button disabled={busy}>{t("ai.saveLimits")}</Button>
                </form>
              </details>
              <details className="group rounded-lg border border-border p-4">
                <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-medium [&::-webkit-details-marker]:hidden">
                  {t("ai.history")}
                  <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
                </summary>
                <div className="mt-3 max-h-52 overflow-auto">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t("ai.operation")}</TableHead>
                        <TableHead>{t("ai.status")}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {query.data.jobs?.map((j) => (
                        <TableRow key={j.id}>
                          <TableCell>
                            {actions[j.action] || (j.action === "sync" ? t("ai.sync") : t("ai.monthly"))}
                          </TableCell>
                          <TableCell>
                            {stateLabel(j.state)}
                            {j.last_error && <p className="text-xs text-destructive">{j.last_error}</p>}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              </details>
            </>
          )
        )}
        <AlertDialog
          open={!!confirm}
          onOpenChange={(next) => {
            if (!next) setConfirm("")
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{confirm === "provision" ? t("ai.provision") : actions[confirm]}</AlertDialogTitle>
              <AlertDialogDescription>
                {confirm === "provision"
                  ? t("ai.provisionWarning")
                  : confirm === "rotate"
                    ? t("ai.rotateWarning")
                    : confirm === "reset_quota"
                      ? t("ai.resetQuotaWarning")
                      : confirm === "reset_rate"
                        ? t("ai.resetRateWarning")
                        : t("ai.retryWarning")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <div className="flex gap-2 justify-end">
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction
                onClick={() => (confirm === "provision" ? provision.mutate() : action.mutate(confirm))}
              >
                {t("ai.confirm")}
              </AlertDialogAction>
            </div>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  )
}
