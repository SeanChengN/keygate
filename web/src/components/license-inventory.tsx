import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useI18n } from "@/i18n"
import { admin, inventory } from "@/lib/api"
import { formatDate } from "@/lib/utils"

export function InventoryManager({ selected, clear }: { selected: string[]; clear: () => void }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [product, setProduct] = useState("")
  const [plan, setPlan] = useState("")
  const [quantity, setQuantity] = useState(10)
  const [notes, setNotes] = useState("")
  const [batch, setBatch] = useState("")
  const [exportIDs, setExportIDs] = useState<string[] | null>(null)
  const createKey = useRef(crypto.randomUUID())
  const exportKey = useRef(crypto.randomUUID())
  const [downloadError, setDownloadError] = useState("")
  const products = useQuery({ queryKey: ["admin", "products"], queryFn: () => admin.listProducts() })
  const plans = useQuery({
    queryKey: ["inventory-plans", product],
    queryFn: () => admin.listPlans(product),
    enabled: !!product,
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
  const create = useMutation({
    mutationFn: () => inventory.create({ product_id: product, plan_id: plan, quantity, notes }, createKey.current),
    onSuccess: (value) => {
      setBatch(value.id)
      createKey.current = crypto.randomUUID()
      refresh()
    },
  })
  const download = async (id: string) => {
    setDownloadError("")
    try {
      await inventory.download(id)
    } catch (error) {
      setDownloadError((error as Error).message)
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
          <form
            className="grid gap-4 sm:grid-cols-2"
            onSubmit={(event) => {
              event.preventDefault()
              create.mutate()
            }}
          >
            <Label className="grid gap-2">
              {t("common.product")}
              <Select
                value={product}
                onValueChange={(value) => {
                  setProduct(value)
                  setPlan("")
                  change()
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {products.data?.products
                    .filter((value) => value.slug === "movaura-studio" && value.type === "desktop")
                    .map((value) => (
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
                  change()
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {plans.data?.plans
                    .filter(
                      (value) =>
                        value.active &&
                        value.license_type === "subscription" &&
                        ["month", "year"].includes(value.billing_interval || "") &&
                        value.grace_days === 0,
                    )
                    .map((value) => (
                      <SelectItem key={value.id} value={value.id}>
                        {value.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </Label>
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
            <Button className="sm:col-span-2" disabled={!plan || quantity < 1 || quantity > 500 || create.isPending}>
              {t("inventory.create")}
            </Button>
          </form>
          {create.error && (
            <p role="alert" className="text-destructive">
              {create.error.message}
            </p>
          )}
          <Label className="grid gap-2">
            {t("inventory.batches")}
            <Select value={batch} onValueChange={setBatch}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {batches.data?.batches.map((value) => (
                  <SelectItem key={value.id} value={value.id}>
                    {formatDate(value.created_at)} · {value.quantity} · {value.id.slice(0, 8)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Label>
          {current.data && (
            <div className="space-y-2">
              <p className="text-sm text-muted-foreground">
                {current.data.licenses.length} · {t("inventory.unstarted")}
              </p>
              <Button
                variant="outline"
                disabled={
                  !current.data.licenses.some(
                    (value) => value.status === "pending_activation" && !value.first_export_id,
                  )
                }
                onClick={() =>
                  chooseExport(
                    current.data.licenses
                      .filter((value) => value.status === "pending_activation" && !value.first_export_id)
                      .map((value) => value.id),
                  )
                }
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
          {(exportMut.error || current.error || batches.error || exports.error || downloadError) && (
            <p role="alert" className="text-destructive">
              {exportMut.error?.message ||
                current.error?.message ||
                batches.error?.message ||
                exports.error?.message ||
                downloadError}
            </p>
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
              {exports.data?.exports.map((value) => (
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
