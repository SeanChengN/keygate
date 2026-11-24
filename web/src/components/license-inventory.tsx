import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useRef, useState } from "react"
import { Link } from "react-router-dom"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useI18n } from "@/i18n"
import { errorMessage } from "@/i18n/errors"
import { admin, inventory, licenseAI, type Plan } from "@/lib/api"
import { formatDate } from "@/lib/utils"

export function InventoryManager({ selected, clear }: { selected: string[]; clear: () => void }) {
  const { t, locale } = useI18n()
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [product, setProduct] = useState("")
  const [plan, setPlan] = useState("")
  const [quantity, setQuantity] = useState(10)
  const [notes, setNotes] = useState("")
  const [withAI, setWithAI] = useState<boolean | null>(null)
  const [batch, setBatch] = useState("")
  const [exportIDs, setExportIDs] = useState<string[] | null>(null)
  const createKey = useRef(crypto.randomUUID())
  const exportKey = useRef(crypto.randomUUID())
  const [failedDownload, setFailedDownload] = useState<{ id: string; error: unknown } | null>(null)
  const products = useQuery({ queryKey: ["admin", "products"], queryFn: () => admin.listProducts() })
  const plans = useQuery({
    queryKey: ["admin", "plans", product],
    queryFn: () => admin.listPlans(product),
    enabled: open && !!product,
    staleTime: 0,
    refetchOnMount: "always",
  })
  const batches = useQuery({ queryKey: ["inventory-batches"], queryFn: inventory.batches, enabled: open })
  const current = useQuery({
    queryKey: ["inventory-batch", batch],
    queryFn: () => inventory.batch(batch),
    enabled: open && !!batch,
  })
  const exports = useQuery({ queryKey: ["inventory-exports"], queryFn: inventory.exports, enabled: open })
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["inventory-batches"] })
    void qc.invalidateQueries({ queryKey: ["inventory-batch"] })
    void qc.invalidateQueries({ queryKey: ["inventory-exports"] })
    void qc.invalidateQueries({ queryKey: ["admin", "licenses"] })
  }
  const productRows = products.data?.products ?? []
  const planRows = plans.data?.plans ?? []
  const batchRows = batches.data?.batches ?? []
  const licenses = current.data?.licenses ?? []
  const exportRows = exports.data?.exports ?? []
  const availableProducts = productRows.filter((value) => value.inventory_enabled && value.type !== "saas")
  const exportableLicenses = licenses.filter((value) => value.status === "pending_activation" && !value.first_export_id)
  const selectedProduct = availableProducts.find((value) => value.id === product)
  const canAttachAI = !!selectedProduct?.package_ai_enabled
  const aiAccount = useQuery({
    queryKey: ["ai-account", product],
    queryFn: () => licenseAI.account(product),
    enabled: open && canAttachAI,
    retry: false,
  })
  const aiPlan = useQuery({
    queryKey: ["ai-plan", plan],
    queryFn: () => licenseAI.plan(plan),
    enabled: open && canAttachAI && !!plan,
    retry: false,
  })
  const failedQueries = [products, plans, batches, current, exports, aiAccount, aiPlan].filter(
    (query) => query.isEnabled && query.isError,
  )
  const queryErrors = [...new Set(failedQueries.map((query) => errorMessage(query.error, locale)))]
  const aiReady = !!(
    aiAccount.data?.configured &&
    aiAccount.data.enabled &&
    aiAccount.data.state === "ready" &&
    (aiPlan.data?.group_id ?? 0) > 0
  )
  const checkingAI = canAttachAI && !!plan && (aiAccount.isPending || aiPlan.isPending)
  const attachAI = canAttachAI && (withAI ?? aiReady)
  const create = useMutation({
    mutationFn: () =>
      inventory.create({ product_id: product, plan_id: plan, quantity, notes, with_ai: attachAI }, createKey.current),
    onSuccess: (value) => {
      setBatch(value.id)
      createKey.current = crypto.randomUUID()
      refresh()
    },
  })
  const download = async (id: string) => {
    setFailedDownload(null)
    try {
      await inventory.download(id)
    } catch (error) {
      setFailedDownload({ id, error })
    }
  }
  const exportMut = useMutation({
    mutationFn: () => inventory.exportKeys(exportIDs || [], exportKey.current),
    onSuccess: (value) => {
      setExportIDs(null)
      clear()
      refresh()
      void download(value.id)
    },
  })
  const chooseExport = (ids: string[]) => {
    exportKey.current = crypto.randomUUID()
    exportMut.reset()
    setExportIDs(ids)
    setOpen(true)
  }
  const change = () => {
    createKey.current = crypto.randomUUID()
    create.reset()
  }
  const planReason = (value: Plan) => {
    if (!value.active) return t("inventory.planInactive")
    if (value.license_type !== "subscription") return t("inventory.planSubscription")
    if (!["day", "month", "year"].includes(value.billing_interval || "")) return t("inventory.planInterval")
    if (value.grace_days !== 0) return t("inventory.planGrace", { days: value.grace_days })
    return ""
  }
  const selectedPlan = planRows.find((value) => value.id === plan)
  const invalidPlan =
    !selectedProduct ||
    !selectedPlan ||
    !!planReason(selectedPlan) ||
    products.isError ||
    plans.isError ||
    checkingAI ||
    (attachAI && !aiReady)
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        {t("inventory.title")}
      </Button>
      {!!selected.length && (
        <Button variant="outline" onClick={() => chooseExport(selected)}>
          {t("inventory.export")} ({selected.length})
        </Button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{t("inventory.title")}</DialogTitle>
            <DialogDescription>{t("inventory.note")}</DialogDescription>
          </DialogHeader>
          {products.isSuccess && !availableProducts.length && (
            <p className="text-sm text-muted-foreground">{t("inventory.enableProductFirst")}</p>
          )}
          <form
            className="grid gap-4 sm:grid-cols-2"
            onSubmit={(event) => {
              event.preventDefault()
              if (!invalidPlan) create.mutate()
            }}
          >
            <Label className="grid gap-2">
              {t("common.product")}
              <Select
                value={product}
                onValueChange={(value) => {
                  setProduct(value)
                  setPlan("")
                  setWithAI(null)
                  change()
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {availableProducts.map((value) => (
                    <SelectItem key={value.id} value={value.id}>
                      {value.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Label>
            <Label className="grid gap-2">
              {t("common.plan")}
              <Select
                value={plan}
                onValueChange={(value) => {
                  setPlan(value)
                  setWithAI(null)
                  change()
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {planRows.map((value) => (
                    <SelectItem key={value.id} value={value.id} disabled={!!planReason(value)}>
                      {value.name}
                      {value.billing_interval === "day" &&
                        ` · ${t("plans.termDays", { days: value.billing_days ?? 0 })}`}
                      {planReason(value) ? ` — ${planReason(value)}` : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Label>
            {canAttachAI && (
              <div className="space-y-1 sm:col-span-2">
                <Label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={attachAI}
                    disabled={create.isPending || (!aiReady && !attachAI)}
                    onChange={(event) => {
                      setWithAI(event.target.checked)
                      change()
                    }}
                  />
                  {t("inventory.withAI")}
                </Label>
                {!aiReady && (
                  <p className="text-xs text-muted-foreground">
                    {t(checkingAI ? "common.loading" : "inventory.planRequirements")}
                  </p>
                )}
              </div>
            )}
            <Label className="grid gap-2">
              {t("inventory.quantity")}
              <Input
                type="number"
                min={1}
                max={500}
                required
                value={quantity}
                onChange={(event) => {
                  setQuantity(Number(event.target.value))
                  change()
                }}
              />
            </Label>
            <Label className="grid gap-2">
              {t("licenses.notes")}
              <Input
                maxLength={2000}
                value={notes}
                onChange={(event) => {
                  setNotes(event.target.value)
                  change()
                }}
              />
            </Label>
            <Button
              className="sm:col-span-2"
              disabled={invalidPlan || plans.isFetching || quantity < 1 || quantity > 500 || create.isPending}
            >
              {t("inventory.create")}
            </Button>
          </form>
          {create.error && (
            <p role="alert" className="text-destructive">
              {errorMessage(create.error, locale)}
            </p>
          )}
          <p className="text-sm text-muted-foreground">{t("inventory.planRequirements")}</p>
          <Button variant="link" asChild>
            <Link to="/admin/plans">{t("inventory.managePlans")}</Link>
          </Button>
          {failedQueries.length > 0 && (
            <div className="flex flex-wrap items-center gap-2">
              <p role="alert" className="text-destructive">
                {queryErrors.join(" · ")}
              </p>
              <Button
                variant="outline"
                size="sm"
                disabled={failedQueries.some((query) => query.isFetching)}
                onClick={() => void Promise.all(failedQueries.map((query) => query.refetch()))}
              >
                {t("inventory.retry")}
              </Button>
            </div>
          )}
          <Label className="grid gap-2">
            {t("inventory.batches")}
            <Select value={batch} onValueChange={setBatch} disabled={!batchRows.length}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {batchRows.map((value) => (
                  <SelectItem key={value.id} value={value.id}>
                    {formatDate(value.created_at)} · {value.quantity} · {value.id.slice(0, 8)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Label>
          {batches.isSuccess && !batchRows.length && (
            <p className="text-sm text-muted-foreground">{t("inventory.noBatches")}</p>
          )}
          {current.data && (
            <div className="space-y-2">
              <p className="text-sm text-muted-foreground">
                {licenses.length} · {t("inventory.unstarted")}
              </p>
              <Button
                variant="outline"
                disabled={!exportableLicenses.length}
                onClick={() => chooseExport(exportableLicenses.map((value) => value.id))}
              >
                {t("inventory.exportBatch")}
              </Button>
            </div>
          )}
          {exportIDs && (
            <div className="rounded-md border p-4 space-y-3">
              <p>{t("inventory.exportNote")}</p>
              <p>
                {exportIDs.length} · {t("licenses.title")}
              </p>
              <Button disabled={exportMut.isPending} onClick={() => exportMut.mutate()}>
                {t("inventory.confirm")}
              </Button>
              <Button variant="ghost" onClick={() => setExportIDs(null)}>
                {t("common.cancel")}
              </Button>
            </div>
          )}
          {exportMut.error && (
            <p role="alert" className="text-destructive">
              {errorMessage(exportMut.error, locale)}
            </p>
          )}
          {failedDownload && (
            <div className="flex flex-wrap items-center gap-2">
              <p role="alert" className="text-destructive">
                {errorMessage(failedDownload.error, locale)}
              </p>
              <Button variant="outline" size="sm" onClick={() => void download(failedDownload.id)}>
                {t("inventory.retryDownload")}
              </Button>
            </div>
          )}
          <h3 className="font-medium">{t("inventory.history")}</h3>
          <p className="text-sm text-muted-foreground">{t("inventory.exportNote")}</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.created")}</TableHead>
                <TableHead>{t("inventory.count")}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {exports.isSuccess && !exportRows.length && (
                <TableRow>
                  <TableCell colSpan={3} className="text-center text-muted-foreground">
                    {t("inventory.noExports")}
                  </TableCell>
                </TableRow>
              )}
              {exportRows.map((value) => (
                <TableRow key={value.id}>
                  <TableCell>{formatDate(value.created_at)}</TableCell>
                  <TableCell>{value.license_ids.length}</TableCell>
                  <TableCell>
                    <Button size="sm" variant="outline" onClick={() => void download(value.id)}>
                      {t("inventory.redownload")}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </DialogContent>
      </Dialog>
    </>
  )
}
