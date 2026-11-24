import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ChevronDown, Copy, MoreHorizontal } from "lucide-react"
import { useEffect, useId, useRef, useState } from "react"
import { showToast } from "@/components/toast"
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
import { useSiteConfig } from "@/hooks/use-site-config"
import { useI18n } from "@/i18n"
import { errorMessage } from "@/i18n/errors"
import type en from "@/i18n/locales/en"

type TranslationKeys = keyof typeof en

import { type AIGroupChoice, type AILimits, type AIProvisionPreview, type LicenseAIJob, licenseAI } from "@/lib/api"

const empty: AILimits = { enabled: false, group_id: 0, quota: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }
const labels = {
  quota: "ai.quota",
  rate_limit_5h: "ai.rate5h",
  rate_limit_1d: "ai.rate1d",
  rate_limit_7d: "ai.rate7d",
}
const states: Record<string, string> = {
  pending: "ai.pending",
  deleting: "ai.deleting",
  waiting_activation: "ai.waiting_activation",
  running: "ai.pending",
  synced: "ai.synced",
  done: "ai.done",
  failed: "ai.failed",
  uncertain: "ai.uncertain",
  ready: "ai.ready",
  attention: "ai.attention",
  unconfigured: "ai.unconfigured",
  refreshing: "ai.accountRefreshing",
  account_disabled: "ai.accountDisabled",
  refresh_uncertain: "ai.uncertain",
  skipped: "ai.skipped",
  removed: "ai.removed",
}
function useAIState() {
  const { t } = useI18n()
  return (state: string) => (states[state] ? t(states[state] as TranslationKeys) : state)
}
function useAIGroupChoices(productId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["ai-groups", productId],
    queryFn: ({ signal }) => licenseAI.groups(productId, signal),
    enabled,
    staleTime: 60_000,
    retry: false,
    retryOnMount: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}

type AIGroupQuery = ReturnType<typeof useAIGroupChoices>

function canSaveGroup(groupId: number, savedGroupId: number, query: AIGroupQuery) {
  return (
    groupId === savedGroupId ||
    (query.isSuccess && !query.isFetching && query.data.groups.some((group) => group.id === groupId))
  )
}

function GroupChoiceLabel({ group }: { group: AIGroupChoice }) {
  const { t } = useI18n()
  return (
    <span className="grid min-w-0 gap-0.5 text-left leading-snug">
      <span className="break-words font-medium">{group.name}</span>
      <span className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground tabular-nums">
        <span>#{group.id}</span>
        <span className="font-medium text-foreground">{group.rate_multiplier}×</span>
        <span>{t(group.rate_source === "account" ? "ai.groupAccountRate" : "ai.groupDefaultRate")}</span>
      </span>
    </span>
  )
}

function GroupField({
  groupId,
  savedGroupId,
  query,
  onChange,
}: {
  groupId: number
  savedGroupId: number
  query: AIGroupQuery
  onChange: (groupId: number) => void
}) {
  const { t } = useI18n()
  const fieldId = useId()
  const groups = query.isError ? [] : (query.data?.groups ?? [])
  const selected = groups.find((group) => group.id === groupId)
  const originalMissing = !groups.some((group) => group.id === savedGroupId)
  return (
    <div className="grid min-w-0 gap-2 sm:col-span-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Label htmlFor={fieldId}>{t("ai.group")}</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t(query.isFetching ? "ai.groupLoading" : "ai.groupRefresh")}
        </Button>
      </div>
      <Select
        value={String(groupId)}
        onValueChange={(id) => {
          const next = Number(id)
          if (id && next !== groupId && (next === savedGroupId || groups.some((group) => group.id === next))) {
            onChange(next)
          }
        }}
      >
        <SelectTrigger
          id={fieldId}
          className="h-auto min-h-9 gap-2 [&>span]:line-clamp-none [&>span]:min-w-0 [&>svg]:shrink-0"
        >
          <SelectValue>
            {selected ? <GroupChoiceLabel group={selected} /> : groupId > 0 ? `#${groupId}` : t("ai.groupUnconfigured")}
          </SelectValue>
        </SelectTrigger>
        <SelectContent className="max-h-72 w-[var(--radix-select-trigger-width)] max-w-[var(--radix-select-content-available-width)] [&_[data-radix-select-viewport]]:h-auto">
          {!selected && groupId !== savedGroupId && (
            <SelectItem value={String(groupId)} disabled>
              {groupId > 0 ? `#${groupId}` : t("ai.groupUnconfigured")}
            </SelectItem>
          )}
          {originalMissing && (
            <SelectItem value={String(savedGroupId)}>
              {savedGroupId > 0 ? t("ai.groupKeep", { id: savedGroupId }) : t("ai.groupUnconfigured")}
            </SelectItem>
          )}
          {groups.map((group) => (
            <SelectItem key={group.id} value={String(group.id)} className="whitespace-normal">
              <GroupChoiceLabel group={group} />
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Failure error={query.error} />
      {query.isError && <p className="text-xs text-muted-foreground">{t("ai.groupKeepOnError")}</p>}
      {query.isSuccess && groups.length === 0 && <p className="text-xs text-muted-foreground">{t("ai.groupEmpty")}</p>}
      {query.isSuccess && !query.isFetching && originalMissing && groupId === savedGroupId && groupId > 0 && (
        <p className="text-xs text-muted-foreground">{t("ai.groupOriginalUnavailable")}</p>
      )}
      {!query.isFetching && !canSaveGroup(groupId, savedGroupId, query) && (
        <p role="alert" className="text-sm text-destructive">
          {t("ai.groupDraftUnavailable")}
        </p>
      )}
    </div>
  )
}

function Limits({
  value,
  onChange,
  savedGroupId,
  groups,
  dayPlan = false,
}: {
  dayPlan?: boolean
  value: AILimits
  onChange: (value: AILimits) => void
  savedGroupId: number
  groups: AIGroupQuery
}) {
  const { t } = useI18n()
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <GroupField
        groupId={value.group_id}
        savedGroupId={savedGroupId}
        query={groups}
        onChange={(group_id) => onChange({ ...value, group_id })}
      />
      {Object.entries(labels).map(([key, label]) => (
        <Label key={key} className="grid gap-2">
          {t(key === "quota" && dayPlan ? "ai.termQuota" : (label as TranslationKeys))}
          <Input
            type="number"
            min="0"
            max="1000000"
            step="any"
            required
            value={value[key as keyof typeof labels]}
            onChange={(e) => onChange({ ...value, [key]: Number(e.target.value) })}
          />
        </Label>
      ))}
    </div>
  )
}
function Failure({ error }: { error: Error | null }) {
  const { locale } = useI18n()
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {errorMessage(error, locale)}
    </p>
  ) : null
}

export function AIAccountDialog({ id }: { id: string }) {
  const { t, locale } = useI18n()
  const qc = useQueryClient()
  const dirty = useRef(false)
  const { timezone } = useSiteConfig()
  const stateLabel = useAIState()
  const [open, setOpen] = useState(false),
    [enabled, setEnabled] = useState(false)
  const [access, setAccess] = useState(""),
    [refresh, setRefresh] = useState(""),
    [expires, setExpires] = useState("")
  const expiryMillis = /^\d{13}$/.test(expires.trim()) ? Number(expires.trim()) : NaN
  const expiryValid =
    Number.isSafeInteger(expiryMillis) && Math.floor(expiryMillis / 1000) > Math.floor(Date.now() / 1000)
  const expiryPreview = Number.isFinite(expiryMillis)
    ? new Intl.DateTimeFormat(locale === "zh" ? "zh-CN" : "en-US", {
        timeZone: timezone || "UTC",
        dateStyle: "medium",
        timeStyle: "medium",
      }).format(new Date(expiryMillis))
    : ""
  const query = useQuery({
    queryKey: ["ai-account", id],
    queryFn: () => licenseAI.account(id),
    enabled: open,
    staleTime: 0,
    refetchInterval: 15_000,
    retry: false,
  })
  useEffect(() => {
    if (open && query.data && !dirty.current) {
      setEnabled(query.data.enabled)
      setExpires(query.data.expires_at ? String(query.data.expires_at * 1000) : "")
    }
  }, [query.data, open])
  const save = useMutation({
    mutationFn: () => {
      const body = {
        enabled,
        ...(access || refresh
          ? { access_token: access, refresh_token: refresh, expires_at: Math.floor(expiryMillis / 1000) }
          : {}),
      }
      return licenseAI.saveAccount(id, body)
    },
    onSuccess: (metadata) => {
      setAccess("")
      setRefresh("")
      dirty.current = false
      qc.setQueryData(["ai-account", id], metadata)
      void qc.resetQueries({ queryKey: ["ai-groups", id] })
    },
  })
  const healthMessages: Record<string, TranslationKeys> = {
    refresh_rejected: "ai.refreshRejected",
    refresh_interrupted: "ai.refreshInterrupted",
    refresh_result_unknown: "ai.refreshUnknown",
    credential_unreadable: "ai.credentialInvalid",
    access_rejected: "ai.accessRejected",
  }
  const alertStatuses: Record<string, TranslationKeys> = {
    pending: "ai.alertPending",
    waiting_smtp: "ai.alertWaitingSMTP",
    waiting_recipients: "ai.alertWaitingRecipients",
    sent: "ai.alertSent",
    failed: "ai.alertFailed",
    partial: "ai.alertPartial",
  }
  const healthMessage = healthMessages[query.data?.health_error || ""]
  const alertStatus = alertStatuses[query.data?.alert_status || ""]
  const replacing = !!(access || refresh)
  const incomplete = replacing && (!access || !refresh || !expiryValid)
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
            setExpires("")
            dirty.current = false
            save.reset()
          }
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t("ai.accountTitle")}</DialogTitle>
            <DialogDescription>{t("ai.accountDescription")}</DialogDescription>
          </DialogHeader>
          <p>
            {t("ai.status")}：
            {query.isPending
              ? t("ai.loading")
              : query.isError
                ? t("ai.readFailed")
                : stateLabel(
                    query.data?.configured && !query.data.enabled
                      ? "account_disabled"
                      : query.data?.state || "unconfigured",
                  )}
          </p>
          {query.data?.configured && !query.isError && (
            <div className="rounded-lg border bg-muted/40 p-3 text-sm space-y-1">
              <p className="font-medium">{t("ai.configured")}</p>
              {query.data.updated_at && (
                <p>
                  {t("common.updated")}:{" "}
                  {new Date(query.data.updated_at).toLocaleString(locale === "zh" ? "zh-CN" : "en-US", {
                    timeZone: timezone || "UTC",
                  })}{" "}
                  ({timezone || "UTC"})
                </p>
              )}
              <p>
                {t("ai.lastRefresh")}{" "}
                {query.data.last_refresh_at
                  ? new Date(query.data.last_refresh_at).toLocaleString(locale === "zh" ? "zh-CN" : "en-US", {
                      timeZone: timezone || "UTC",
                    })
                  : t("ai.noRefreshRecorded")}
              </p>
              {healthMessage && <p className="font-medium text-destructive">{t(healthMessage)}</p>}
              {alertStatus && (
                <p>
                  {t("ai.ownerAlert")} {t(alertStatus)}
                </p>
              )}
            </div>
          )}
          <Failure error={query.error} />
          <Failure error={save.error} />
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (!incomplete) save.mutate()
            }}
          >
            <Label className="flex gap-2">
              <input
                type="checkbox"
                checked={enabled}
                disabled={save.isPending}
                onChange={(e) => {
                  dirty.current = true
                  setEnabled(e.target.checked)
                }}
              />
              {t("ai.enableSync")}
            </Label>
            <Label className="grid gap-2">
              Access token
              <Input
                type="password"
                autoComplete="off"
                disabled={save.isPending}
                placeholder={query.data?.configured ? t("ai.tokensSaved") : undefined}
                value={access}
                onChange={(e) => {
                  dirty.current = true
                  setAccess(e.target.value)
                }}
              />
            </Label>
            <Label className="grid gap-2">
              Refresh token
              <Input
                type="password"
                autoComplete="off"
                disabled={save.isPending}
                placeholder={query.data?.configured ? t("ai.tokensSaved") : undefined}
                value={refresh}
                onChange={(e) => {
                  dirty.current = true
                  setRefresh(e.target.value)
                }}
              />
            </Label>
            <Label className="grid gap-2">
              {t("ai.tokenExpiry")}
              <Input
                type="text"
                inputMode="numeric"
                pattern="[0-9]{13}"
                maxLength={13}
                placeholder="1794464309079"
                required={!!(access || refresh)}
                value={expires}
                disabled={save.isPending}
                onChange={(e) => {
                  dirty.current = true
                  setExpires(e.target.value)
                }}
              />
            </Label>
            {expiryPreview && (
              <p className="text-sm text-muted-foreground">
                {expiryPreview} ({timezone || "UTC"})
              </p>
            )}
            {replacing && (!access || !refresh) && (
              <p role="alert" className="text-sm text-destructive">
                {t("ai.tokensPair")}
              </p>
            )}
            {!!expires && !expiryValid && (
              <p role="alert" className="text-sm text-destructive">
                {t("ai.expiryInvalid")}
              </p>
            )}
            <Button disabled={save.isPending || query.isPending || query.isError || incomplete}>
              {t("common.save")}
            </Button>
            {save.isSuccess && <p role="status">{t("ai.saved")}</p>}
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function AIPlanDialog({
  id,
  productId,
  dayPlan,
  canProvision,
}: {
  id: string
  productId: string
  dayPlan: boolean
  canProvision: boolean
}) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const stateLabel = useAIState()
  const [dirty, setDirty] = useState(false)
  const [confirmSave, setConfirmSave] = useState(false)
  const [preview, setPreview] = useState<AIProvisionPreview | null>(null)
  const [batchId, setBatchId] = useState("")
  const batchKey = useRef("")
  const [open, setOpen] = useState(false),
    [value, setValue] = useState(empty)
  const query = useQuery({ queryKey: ["ai-plan", id], queryFn: () => licenseAI.plan(id), enabled: open, retry: false })
  const groups = useAIGroupChoices(productId, open)
  const savedGroupId = query.data?.group_id ?? 0
  const groupCanSave = canSaveGroup(value.group_id, savedGroupId, groups) && (!value.enabled || value.group_id > 0)
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
      void qc.invalidateQueries({ queryKey: ["admin", "plans"] })
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
              if (groupCanSave) setConfirmSave(true)
            }}
          >
            <Label className="flex gap-2">
              <input
                type="checkbox"
                checked={value.enabled && canProvision}
                disabled={!canProvision || dayPlan}
                onChange={(e) => {
                  setDirty(true)
                  setValue({ ...value, enabled: e.target.checked })
                }}
              />
              {t("ai.autoProvision")}
            </Label>
            {!canProvision && <p className="text-xs text-muted-foreground">{t("products.aiDeliveryUnavailable")}</p>}
            <Limits
              dayPlan={dayPlan}
              value={value}
              groups={groups}
              savedGroupId={savedGroupId}
              onChange={(v) => {
                setDirty(true)
                setValue(v)
              }}
            />
            <p className="text-sm text-muted-foreground">{t("ai.unlimitedNote")}</p>
            <Button disabled={save.isPending || query.isPending || query.isError || !groupCanSave}>
              {t("ai.saveSync")}
            </Button>
            {save.isSuccess && <p role="status">{t("ai.saved")}</p>}
          </form>
          <p className="text-sm text-muted-foreground">
            {t("ai.revision")} {query.data?.revision || 0} ·{" "}
            {query.data?.summary?.map((s) => `${stateLabel(s.state)}: ${s.count}`).join(" / ")}
          </p>
          <Button
            variant="outline"
            disabled={!canProvision || dirty || previewMutation.isPending}
            onClick={() => previewMutation.mutate()}
          >
            {t("ai.previewBatch")}
          </Button>
          {preview && (
            <div className="space-y-3 rounded-lg border p-4">
              <p>
                {t(dayPlan ? "ai.batchWarningDay" : "ai.batchWarning", {
                  count: preview.items.filter((i) => i.eligible).length,
                  quota: preview.config.quota === 0 ? t("ai.unlimited") : preview.config.quota,
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
                <AlertDialogAction disabled={!groupCanSave} onClick={() => groupCanSave && save.mutate()}>
                  {t("ai.confirm")}
                </AlertDialogAction>
              </div>
            </AlertDialogContent>
          </AlertDialog>
        </DialogContent>
      </Dialog>
    </>
  )
}

const historyActions = new Map<string, TranslationKeys>([
  ["sync", "ai.sync"],
  ["monthly", "ai.monthly"],
  ["reset_quota", "ai.resetQuota"],
  ["reset_rate", "ai.resetRate"],
  ["rotate", "ai.rotate"],
])

const historyFilters = [
  { key: "all", label: "common.all" },
  { key: "processing", label: "ai.pending" },
  { key: "completed", label: "ai.done" },
  { key: "issues", label: "ai.historyIssues" },
] as const

function isHistoryIssue(state: string) {
  return state === "failed" || state === "uncertain"
}

function AISyncHistory({ jobs }: { jobs: LicenseAIJob[] }) {
  const { t, locale } = useI18n()
  const stateLabel = useAIState()
  const [filter, setFilter] = useState<(typeof historyFilters)[number]["key"]>("all")
  const formatter = new Intl.DateTimeFormat(locale === "zh" ? "zh-CN" : "en-US", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  })
  const rows = jobs.filter(({ state }) => {
    switch (filter) {
      case "processing":
        return state === "pending" || state === "running"
      case "completed":
        return state === "done"
      case "issues":
        return isHistoryIssue(state)
      default:
        return true
    }
  })
  const now = Date.now()

  return (
    <details className="group rounded-lg border border-border p-4">
      <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-medium [&::-webkit-details-marker]:hidden">
        {t("ai.history")}
        <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
      </summary>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <p className="text-xs text-muted-foreground">{t("ai.historyHint")}</p>
        <fieldset className="flex min-w-0 flex-wrap gap-0.5">
          <legend className="sr-only">{t("ai.historyFilter")}</legend>
          {historyFilters.map(({ key, label }) => (
            <Button
              key={key}
              size="sm"
              variant={filter === key ? "secondary" : "ghost"}
              aria-pressed={filter === key}
              className="h-8 px-2.5"
              onClick={() => setFilter(key)}
            >
              {t(label)}
            </Button>
          ))}
        </fieldset>
      </div>
      <div className="mt-3 max-h-72 overflow-auto rounded-md border border-border">
        <Table className="min-w-[28rem]">
          <TableHeader className="bg-muted/40 [&_tr]:border-border">
            <TableRow>
              <TableHead className="whitespace-nowrap">{t("ai.operation")}</TableHead>
              <TableHead className="whitespace-nowrap">{t("ai.historyTime")}</TableHead>
              <TableHead className="w-full whitespace-nowrap">{t("ai.status")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={3} className="text-center text-muted-foreground">
                  {t(jobs.length === 0 ? "ai.historyEmpty" : "ai.historyNoMatch")}
                </TableCell>
              </TableRow>
            )}
            {rows.map((job) => {
              const submittedAt = Date.parse(job.created_at)
              const nextAttemptAt = Date.parse(job.next_attempt_at)
              const status =
                job.state === "pending"
                  ? t("ai.historyQueued")
                  : job.state === "running"
                    ? t("ai.historyRunning")
                    : stateLabel(job.state)
              const verifyReset = isHistoryIssue(job.state) && job.phase === "reset_sent"
              return (
                <TableRow key={job.id} className="border-border">
                  <TableCell className="py-3 align-top whitespace-nowrap">
                    {t(historyActions.get(job.action) ?? "ai.historyOther")}
                  </TableCell>
                  <TableCell className="py-3 align-top whitespace-nowrap tabular-nums">
                    {Number.isFinite(submittedAt) ? (
                      <time dateTime={job.created_at}>{formatter.format(submittedAt)}</time>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell className="space-y-1.5 py-3 align-top">
                    <Badge
                      variant={job.state === "failed" ? "destructive" : job.state === "done" ? "secondary" : "outline"}
                      className={
                        job.state === "uncertain"
                          ? "whitespace-nowrap border-amber-300 text-amber-800"
                          : "whitespace-nowrap"
                      }
                    >
                      {status}
                    </Badge>
                    {(job.attempts > 1 || (job.attempts > 0 && job.state !== "done")) && (
                      <p className="text-xs text-muted-foreground">
                        {t("ai.historyAttempts", { count: job.attempts })}
                      </p>
                    )}
                    {job.state === "pending" && job.attempts > 0 && nextAttemptAt > now && (
                      <p className="text-xs text-muted-foreground">
                        {t("ai.historyNextAttempt", { time: formatter.format(nextAttemptAt) })}
                      </p>
                    )}
                    {(job.last_error_code || job.last_error) && (
                      <p className="text-xs text-destructive [overflow-wrap:anywhere]">
                        {errorMessage({ code: job.last_error_code }, locale)}
                      </p>
                    )}
                    {verifyReset && <p className="text-xs text-muted-foreground">{t("ai.historyVerifyReset")}</p>}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>
    </details>
  )
}

export function LicenseAICard({
  id,
  productId,
  dayPlan,
  canProvision,
}: {
  id: string
  productId: string
  dayPlan: boolean
  canProvision: boolean
}) {
  const { t, locale } = useI18n()
  const stateLabel = useAIState()
  const [dirty, setDirty] = useState(false)
  const [editing, setEditing] = useState(false)
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
  const keyName = query.data?.key_name
  const keyNameLabel = t("ai.keyName")
  const copyKeyName = async () => {
    if (!keyName) return
    try {
      await navigator.clipboard.writeText(keyName)
      showToast(t("common.idCopied", { label: keyNameLabel }), "success")
    } catch {
      showToast(t("common.copyFailed"), "error")
    }
  }
  const limits = binding?.config
  const groups = useAIGroupChoices(productId, editing)
  const savedGroupId = limits?.group_id ?? 0
  const groupCanSave = value.group_id > 0 && canSaveGroup(value.group_id, savedGroupId, groups)
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
            <Button disabled={provision.isPending || !canProvision} onClick={() => setConfirm("provision")}>
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
              <div className="@container rounded-lg border border-border bg-muted/30 p-4 text-sm [&_dt]:text-xs [&_dt]:font-medium [&_dt]:text-muted-foreground [&_dd]:mt-1 [&_dd]:font-medium">
                <dl className="grid gap-4 @min-[28rem]:grid-cols-[6.5rem_minmax(0,1fr)]">
                  <div className="min-w-0">
                    <dt className="flex min-h-6 items-center">{t("ai.keyId")}</dt>
                    <dd className="leading-7 tabular-nums">{binding.remote_id || "—"}</dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="flex min-h-6 flex-wrap items-center gap-x-2 gap-y-1">
                      {keyNameLabel}
                      {keyName && query.data?.key_name_inferred && (
                        <Badge variant="outline">{t("ai.keyNameInferred")}</Badge>
                      )}
                    </dt>
                    <dd className="leading-7">
                      {keyName ? (
                        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3">
                          <code className="font-mono [overflow-wrap:anywhere]">{keyName}</code>
                          <Button
                            variant="outline"
                            size="icon"
                            className="h-7 w-7"
                            title={t("common.copyId", { label: keyNameLabel })}
                            aria-label={t("common.copyId", { label: keyNameLabel })}
                            onClick={() => void copyKeyName()}
                          >
                            <Copy className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      ) : (
                        "—"
                      )}
                    </dd>
                  </div>
                </dl>
                <dl className="mt-4 grid gap-4 border-t border-border pt-4 @min-[28rem]:grid-cols-2">
                  <div className="min-w-0">
                    <dt>{t("ai.used")}</dt>
                    <dd className="tabular-nums">
                      {binding.remote.quota_used ?? "—"} USD /{" "}
                      {binding.config.quota === 0 ? t("ai.unlimited") : `${binding.config.quota} USD`}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt>{t("ai.expiry")}</dt>
                    <dd className="tabular-nums">
                      {binding.remote.expires_at ? new Date(binding.remote.expires_at).toLocaleString() : "—"}
                    </dd>
                  </div>
                </dl>
              </div>
              {binding.last_error && (
                <p role="alert" className="text-destructive">
                  {errorMessage({ code: binding.last_error_code }, locale)}
                </p>
              )}
              <p className="text-sm text-muted-foreground">
                {t("ai.anchor", { zone: binding.timezone, date: new Date(binding.anchor).toLocaleString() })}
              </p>
              <details
                className="group rounded-lg border border-border p-4"
                onToggle={(e) => setEditing(e.currentTarget.open)}
              >
                <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-medium [&::-webkit-details-marker]:hidden">
                  {t("ai.adjust")}
                  <ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180" />
                </summary>
                <form
                  className="space-y-3 mt-3"
                  onSubmit={(e) => {
                    e.preventDefault()
                    if (!busy && groupCanSave) save.mutate()
                  }}
                >
                  <Limits
                    dayPlan={dayPlan}
                    value={value}
                    groups={groups}
                    savedGroupId={savedGroupId}
                    onChange={(v) => {
                      setDirty(true)
                      setValue(v)
                    }}
                  />
                  <Button disabled={busy || !groupCanSave}>{t("ai.saveLimits")}</Button>
                </form>
              </details>
              <AISyncHistory key={id} jobs={query.data.jobs ?? []} />
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
                  ? t(dayPlan ? "ai.provisionWarningDay" : "ai.provisionWarning")
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
