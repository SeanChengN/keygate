import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Copy, Package, Pencil, Plus, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { AIModelTiers } from "@/components/ai-model-tiers"
import { EntityId } from "@/components/entity-id"
import { AIPlanDialog } from "@/components/license-ai"
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
import {
  DataTable,
  DataTableBody,
  DataTableCell,
  DataTableEmpty,
  DataTableHead,
  DataTableHeader,
  DataTablePagination,
  DataTableRow,
  useClientPagination,
} from "@/components/ui/data-table"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Separator } from "@/components/ui/separator"
import { useI18n } from "@/i18n"
import { errorMessage } from "@/i18n/errors"
import { admin, type Entitlement, type EntitlementValue, type Plan, type Product } from "@/lib/api"
import { boolColor, formatDate } from "@/lib/utils"

export default function PlansPage() {
  const { t } = useI18n()
  const qc = useQueryClient()
  const [productFilter, setProductFilter] = useState<string>("")
  const [search, setSearch] = useState("")
  const { data: productsData } = useQuery({ queryKey: ["admin", "products"], queryFn: () => admin.listProducts() })
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "plans", productFilter, search],
    queryFn: () => admin.listPlans(productFilter || undefined, search || undefined),
  })
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Plan | null>(null)
  const [deleting, setDeleting] = useState<Plan | null>(null)

  const products = productsData?.products || []
  const plans = data?.plans || []
  const { page, setPage, pageSize, setPageSize, total, totalPages, paginatedItems } = useClientPagination(plans, 10)

  const createMut = useMutation({
    mutationFn: admin.createPlan,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setCreating(false)
      showToast(t("toast.planCreated"), "success")
    },
  })
  const updateMut = useMutation({
    mutationFn: ({ id, ...data }: Partial<Plan> & { id: string }) => admin.updatePlan(id, data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setEditing(null)
    },
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => admin.deletePlan(id),
    onError: (error: Error) => showToast(error.message, "error"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setDeleting(null)
      showToast(t("toast.planDeleted"), "success")
    },
  })

  if (!isLoading && products.length === 0) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("plans.title")}</h1>
          <p className="text-muted-foreground">{t("plans.subtitle")}</p>
        </div>
        <Card>
          <CardContent className="py-12 text-center">
            <Package className="h-12 w-12 mx-auto text-muted-foreground mb-4" />
            <p className="text-lg font-medium">{t("licenses.noProducts")}</p>
            <p className="text-muted-foreground mt-1 mb-4">{t("licenses.noProductsDesc")}</p>
            <Button asChild>
              <Link to="/admin/products">
                <Plus className="h-4 w-4 mr-2" /> {t("products.createTitle")}
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("plans.title")}</h1>
          <p className="text-muted-foreground">{t("plans.subtitle")}</p>
        </div>
        <Button onClick={() => setCreating(true)}>
          <Plus className="h-4 w-4 mr-2" /> {t("plans.new")}
        </Button>
      </div>

      <div className="flex gap-4">
        <Input
          placeholder={t("common.search")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-64"
        />
        <Select value={productFilter} onValueChange={setProductFilter}>
          <SelectTrigger className="w-48">
            <SelectValue placeholder={t("plans.allProducts")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("plans.allProducts")}</SelectItem>
            {products.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <Card>
        <CardContent className="pt-6">
          {isLoading ? (
            <div className="h-32 animate-pulse bg-muted rounded-lg" />
          ) : (
            <>
              <DataTable>
                <DataTableHeader>
                  <DataTableRow>
                    <DataTableHead>{t("common.name")}</DataTableHead>
                    <DataTableHead>{t("common.product")}</DataTableHead>
                    <DataTableHead>{t("plans.licenseType")}</DataTableHead>
                    <DataTableHead>{t("plans.billingInterval")}</DataTableHead>
                    <DataTableHead>{t("plans.maxActivations")}</DataTableHead>
                    <DataTableHead>{t("plans.maxSeats")}</DataTableHead>
                    <DataTableHead>{t("common.status")}</DataTableHead>
                    <DataTableHead>{t("common.created")}</DataTableHead>
                    <DataTableHead className="w-24" />
                  </DataTableRow>
                </DataTableHeader>
                <DataTableBody>
                  {plans.length === 0 && <DataTableEmpty colSpan={9} message={t("plans.empty")} />}
                  {paginatedItems.map((p) => {
                    const prodType = products.find((pr) => pr.id === p.product_id)?.type
                    const supportsActivations = prodType === "desktop" || prodType === "hybrid"
                    const supportsSeats = prodType === "saas" || prodType === "hybrid"
                    return (
                      <DataTableRow key={p.id}>
                        <DataTableCell>
                          <div className="font-medium">{p.name}</div>
                          <EntityId label={t("plans.id")} value={p.id} />
                        </DataTableCell>
                        <DataTableCell className="text-muted-foreground">
                          {products.find((pr) => pr.id === p.product_id)?.name || p.product_id}
                        </DataTableCell>
                        <DataTableCell>
                          <Badge variant="secondary">{t(`plans.${p.license_type}` as "plans.subscription")}</Badge>
                        </DataTableCell>
                        <DataTableCell>
                          {p.license_type === "subscription" && p.billing_interval === "day"
                            ? t("plans.termDays", { days: p.billing_days ?? 0 })
                            : p.license_type === "subscription" && p.billing_interval === "month"
                              ? t("plans.monthly")
                              : p.license_type === "subscription" && p.billing_interval === "year"
                                ? t("plans.yearly")
                                : "—"}
                        </DataTableCell>
                        <DataTableCell>
                          {supportsActivations ? (
                            p.max_activations
                          ) : (
                            <span className="text-muted-foreground" title={t("plans.notApplicableForType")}>
                              —
                            </span>
                          )}
                        </DataTableCell>
                        <DataTableCell>
                          {supportsSeats ? (
                            p.max_seats || 0
                          ) : (
                            <span className="text-muted-foreground" title={t("plans.notApplicableForType")}>
                              —
                            </span>
                          )}
                        </DataTableCell>
                        <DataTableCell>
                          <Badge className={boolColor(p.active)}>
                            {p.active ? t("common.active") : t("common.inactive")}
                          </Badge>
                        </DataTableCell>
                        <DataTableCell className="text-muted-foreground text-xs">
                          {formatDate(p.created_at)}
                        </DataTableCell>
                        <DataTableCell>
                          <div className="flex gap-1">
                            {products.find((pr) => pr.id === p.product_id)?.package_ai_enabled && (
                              <AIPlanDialog
                                id={p.id}
                                productId={p.product_id}
                                dayPlan={p.billing_interval === "day"}
                                canProvision={prodType !== "saas"}
                              />
                            )}
                            {p.checkout_id && p.billing_interval !== "day" && (
                              <Button
                                variant="ghost"
                                size="icon"
                                title={t("plans.copyCheckoutLink")}
                                onClick={() => {
                                  const url = `${window.location.origin}/pay/${p.checkout_id}`
                                  navigator.clipboard.writeText(url)
                                  showToast(t("plans.linkCopied"))
                                }}
                              >
                                <Copy className="h-4 w-4" />
                              </Button>
                            )}
                            <Button variant="ghost" size="icon" onClick={() => setEditing(p)}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <Button variant="ghost" size="icon" onClick={() => setDeleting(p)}>
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </div>
                        </DataTableCell>
                      </DataTableRow>
                    )
                  })}
                </DataTableBody>
              </DataTable>
              {total > 0 && (
                <DataTablePagination
                  page={page}
                  totalPages={totalPages}
                  total={total}
                  pageSize={pageSize}
                  onPageChange={setPage}
                  onPageSizeChange={setPageSize}
                />
              )}
            </>
          )}
        </CardContent>
      </Card>

      {/* Create */}
      <PlanDialog
        open={creating}
        onClose={() => setCreating(false)}
        products={products}
        onSubmit={(d) => createMut.mutate(d)}
        loading={createMut.isPending}
        title={t("plans.createTitle")}
      />

      {/* Edit */}
      {editing && (
        <PlanDialog
          open
          onClose={() => setEditing(null)}
          plan={plans.find((entry) => entry.id === editing.id) ?? editing}
          products={products}
          onSubmit={(d) => updateMut.mutate({ id: editing.id, ...d })}
          loading={updateMut.isPending}
          title={t("plans.editTitle")}
        />
      )}

      {/* Delete */}
      <AlertDialog open={!!deleting} onOpenChange={() => setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("common.delete")} "{deleting?.name}"?
            </AlertDialogTitle>
            <AlertDialogDescription>{t("plans.deleteConfirm")}</AlertDialogDescription>
          </AlertDialogHeader>
          <div className="flex justify-end gap-2">
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => deleting && deleteMut.mutate(deleting.id)}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </div>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function PlanDialog({
  open,
  onClose,
  plan,
  products,
  onSubmit,
  loading,
  title,
}: {
  open: boolean
  onClose: () => void
  plan?: Plan
  products: Product[]
  onSubmit: (data: Partial<Plan>) => void
  loading: boolean
  title: string
}) {
  const { t } = useI18n()
  const [form, setForm] = useState({
    product_id: plan?.product_id || products[0]?.id || "",
    name: plan?.name || "",
    slug: plan?.slug || "",
    license_type: plan?.license_type || "subscription",
    billing_interval: plan?.billing_interval || "month",
    billing_days: plan?.billing_days ?? 7,
    max_activations: plan?.max_activations ?? 3,
    max_seats: plan?.max_seats ?? 1,
    trial_days: plan?.trial_days ?? 0,
    grace_days: plan?.grace_days ?? 7,
    license_model: plan?.license_model || "standard",
    floating_timeout: plan?.floating_timeout ?? 30,
    active: plan?.active ?? true,
    stripe_price_id: plan?.stripe_price_id || "",
  })

  const set = (key: string, val: string | number | boolean) => setForm((f) => ({ ...f, [key]: val }))

  // Capability map keyed by the currently-selected product's type.
  // Mirrors backend model.ProductSupports — DO NOT diverge or admins
  // will see fields the server then rejects on submit.
  const selectedProduct = products.find((p) => p.id === form.product_id)
  const dayPlan = form.license_type === "subscription" && form.billing_interval === "day"
  const trialPlan = form.license_type === "trial"
  const productType = selectedProduct?.type || "hybrid"
  const supports = {
    activations: productType === "desktop" || productType === "hybrid",
    seats: productType === "saas" || productType === "hybrid",
  }

  // When the product type doesn't expose a capability, force the
  // corresponding numeric field to 0 on submit (avoids sending
  // max_activations=3 for a saas product just because the form
  // pre-filled it before the product was selected).
  const handleSubmit = () => {
    const { billing_days, trial_days, ...values } = form
    const payload: Partial<Plan> = values
    if (form.license_type !== "subscription") {
      payload.billing_interval = ""
    }
    if (trialPlan) {
      payload.trial_days = trial_days
    }
    if (dayPlan) {
      payload.billing_days = billing_days
      payload.grace_days = 0
      payload.stripe_price_id = ""
    }
    if (!supports.activations) {
      payload.max_activations = 0
      payload.license_model = "standard"
      payload.floating_timeout = 0
    }
    if (!supports.seats) {
      payload.max_seats = 0
    }
    onSubmit(payload)
  }

  return (
    <Dialog open={open} onOpenChange={onClose}>
      <DialogContent className="max-w-lg max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{t("plans.formDesc")}</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            handleSubmit()
          }}
          className="space-y-4"
        >
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2 col-span-2">
              <Label>{t("common.product")}</Label>

              <Select
                disabled={!!plan}
                value={form.product_id}
                onValueChange={(v) =>
                  setForm((current) => ({
                    ...current,
                    product_id: v,
                    billing_interval:
                      current.billing_interval === "day" &&
                      !products.find((product) => product.id === v)?.inventory_enabled
                        ? "month"
                        : current.billing_interval,
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {products.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                      {p.type ? (
                        <span className="ml-2 text-xs text-muted-foreground">
                          [{t(`products.${p.type}` as "products.desktop")}]
                        </span>
                      ) : null}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {/* Capability hint — what this product type allows on plans */}
              <p className="text-xs text-muted-foreground">
                {productType === "desktop" && t("plans.hintDesktop")}
                {productType === "saas" && t("plans.hintSaas")}
                {productType === "hybrid" && t("plans.hintHybrid")}
              </p>
            </div>
            <div className="space-y-2">
              <Label>{t("common.name")}</Label>
              <Input
                value={form.name}
                onChange={(e) => {
                  set("name", e.target.value)
                  if (!plan) set("slug", e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "-"))
                }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label>{t("products.slug")}</Label>
              <Input value={form.slug} onChange={(e) => set("slug", e.target.value)} required />
            </div>
            <div className="space-y-2">
              <Label>{t("plans.licenseType")}</Label>
              <Select
                disabled={plan?.billing_interval === "day"}
                value={form.license_type}
                onValueChange={(v) =>
                  setForm((current) => ({
                    ...current,
                    license_type: v,
                    billing_interval:
                      v === "subscription" ? (current.billing_interval === "year" ? "year" : "month") : "",
                  }))
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="subscription">{t("plans.subscription")}</SelectItem>
                  <SelectItem value="perpetual">{t("plans.perpetual")}</SelectItem>
                  <SelectItem value="trial">{t("plans.trial")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {form.license_type === "subscription" && (
              <div className="space-y-2">
                <Label>{t("plans.billingInterval")}</Label>
                <Select
                  disabled={plan?.billing_interval === "day"}
                  value={form.billing_interval || "month"}
                  onValueChange={(v) => set("billing_interval", v)}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(plan?.billing_interval === "day" || (!plan && selectedProduct?.inventory_enabled)) && (
                      <SelectItem value="day">{t("plans.daily")}</SelectItem>
                    )}
                    <SelectItem value="month">{t("plans.monthly")}</SelectItem>
                    <SelectItem value="year">{t("plans.yearly")}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}
            {dayPlan && (
              <div className="space-y-2">
                <Label htmlFor="plan-billing-days">{t("plans.billingDays")}</Label>
                <Input
                  id="plan-billing-days"
                  type="number"
                  min={1}
                  max={365}
                  step={1}
                  required
                  value={form.billing_days}
                  onChange={(e) => set("billing_days", Number(e.target.value))}
                />
                <p className="text-xs text-muted-foreground">{t("plans.dayTermHint")}</p>
              </div>
            )}
            {supports.activations && (
              <div className="space-y-2">
                <Label>{t("plans.maxActivations")}</Label>
                <Input
                  type="number"
                  min={1}
                  value={form.max_activations}
                  onChange={(e) => set("max_activations", Number(e.target.value))}
                />
              </div>
            )}
            {supports.seats && (
              <div className="space-y-2">
                <Label>{t("plans.maxSeats")}</Label>
                <Input
                  type="number"
                  min={1}
                  value={form.max_seats}
                  onChange={(e) => set("max_seats", Number(e.target.value))}
                />
              </div>
            )}
            {supports.activations && (
              <div className="space-y-2">
                <Label>{t("plans.licenseModel")}</Label>
                <Select value={form.license_model} onValueChange={(v) => set("license_model", v)}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="standard">{t("plans.modelStandard")}</SelectItem>
                    <SelectItem value="floating">{t("plans.modelFloating")}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}
            {supports.activations && form.license_model === "floating" && (
              <div className="space-y-2">
                <Label>{t("plans.floatingTimeout")}</Label>
                <Input
                  type="number"
                  min={1}
                  value={form.floating_timeout}
                  onChange={(e) => set("floating_timeout", Number(e.target.value))}
                />
              </div>
            )}
            {trialPlan && (
              <div className="space-y-2">
                <Label htmlFor="plan-trial-days">{t("plans.trialDays")}</Label>
                <Input
                  id="plan-trial-days"
                  type="number"
                  min={0}
                  max={365}
                  value={form.trial_days}
                  onChange={(e) => set("trial_days", Number(e.target.value))}
                />
                <p className="text-xs text-muted-foreground">{t("plans.trialDaysHint")}</p>
              </div>
            )}
            <div className="space-y-2">
              <Label>{t("plans.graceDays")}</Label>
              <Input
                type="number"
                min={0}
                disabled={dayPlan}
                value={dayPlan ? 0 : form.grace_days}
                onChange={(e) => set("grace_days", Number(e.target.value))}
              />
            </div>
            <div className="space-y-2 flex items-center gap-3 pt-5">
              <input
                type="checkbox"
                checked={form.active}
                onChange={(e) => set("active", e.target.checked)}
                className="h-4 w-4 rounded border-input accent-primary"
                id="plan-active"
              />
              <Label htmlFor="plan-active">{t("common.active")}</Label>
            </div>
            <div className="space-y-2">
              <Label>{t("plans.stripePriceId")}</Label>
              <Input
                disabled={dayPlan}
                value={dayPlan ? "" : form.stripe_price_id}
                onChange={(e) => set("stripe_price_id", e.target.value)}
                placeholder="price_..."
              />
            </div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={loading}>
              {loading ? t("common.loading") : t("common.save")}
            </Button>
          </div>
        </form>

        {/* Entitlements section (only when editing) */}
        {plan && (
          <>
            <Separator />
            <EntitlementSection
              planId={plan.id}
              entitlements={plan.entitlements}
              canCreateStudioProfile={
                selectedProduct?.slug === "movaura-studio" && !!selectedProduct.package_ai_enabled
              }
            />
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

const emptyEntitlement = {
  feature: "",
  value_type: "bool",
  value: "true",
  quota_period: "",
  quota_unit: "",
  stripe_meter_event_name: "",
}

function isAIProfile(feature: string) {
  return feature === "studio.ai_profile" || feature === "license.ai_profile"
}

function EntitlementSection({
  planId,
  entitlements: initial,
  canCreateStudioProfile,
}: {
  planId: string
  entitlements?: Entitlement[]
  canCreateStudioProfile: boolean
}) {
  const { t, locale } = useI18n()
  const qc = useQueryClient()
  const [entitlements, setEntitlements] = useState(initial ?? [])
  const [editor, setEditor] = useState<{ original?: Entitlement } | null>(null)
  const [newEnt, setNewEnt] = useState(emptyEntitlement)
  const [confirmDelete, setConfirmDelete] = useState<{ id: string; feature: string } | null>(null)

  useEffect(() => {
    setEntitlements(initial ?? [])
  }, [initial])

  function applyEntitlement(saved: Entitlement) {
    setEntitlements((rows) =>
      rows.some((row) => row.id === saved.id)
        ? rows.map((row) => (row.id === saved.id ? saved : row))
        : [...rows, saved],
    )
  }

  const save = useMutation({
    mutationFn: () => {
      const quota = newEnt.value_type === "quota"
      const fields: EntitlementValue = {
        value_type: newEnt.value_type,
        value: newEnt.value,
        quota_period: quota ? newEnt.quota_period : "",
        quota_unit: quota ? newEnt.quota_unit : "",
        stripe_meter_event_name: quota ? newEnt.stripe_meter_event_name : "",
      }
      if (editor?.original) {
        const { id, feature, value_type, value, quota_period, quota_unit, stripe_meter_event_name } = editor.original
        return admin.updateEntitlement(id, {
          ...fields,
          expected: { feature, value_type, value, quota_period, quota_unit, stripe_meter_event_name },
        })
      }
      return admin.createEntitlement({ plan_id: planId, feature: newEnt.feature.trim(), ...fields })
    },
    onSuccess: (saved) => {
      applyEntitlement(saved)
      void qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setEditor(null)
    },
    onError: (error) => {
      if ("code" in error && error.code === "ENTITLEMENT_CHANGED") {
        void qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      }
    },
  })

  function openEditor(original?: Entitlement) {
    save.reset()
    setEditor({ original })
    setNewEnt(
      original
        ? {
            feature: original.feature,
            value_type: original.value_type,
            value: original.value,
            quota_period: original.quota_period ?? "",
            quota_unit: original.quota_unit ?? "",
            stripe_meter_event_name: original.stripe_meter_event_name ?? "",
          }
        : emptyEntitlement,
    )
  }

  const deleteMut = useMutation({
    mutationFn: (id: string) => admin.deleteEntitlement(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin", "plans"] })
      setConfirmDelete(null)
    },
  })

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-semibold">{t("plans.entitlements")}</h4>
        <div className="flex flex-wrap justify-end gap-2">
          {canCreateStudioProfile && !entitlements.some((row) => isAIProfile(row.feature)) && (
            <AIModelTiers planId={planId} onSaved={applyEntitlement} />
          )}
          <Button type="button" variant="outline" size="sm" disabled={save.isPending} onClick={() => openEditor()}>
            <Plus className="h-3 w-3 mr-1" /> {t("common.create")}
          </Button>
        </div>
      </div>
      {entitlements.length > 0 && (
        <div className="space-y-2">
          {entitlements.map((e) => (
            <div key={e.id} className="flex items-center justify-between bg-muted/50 rounded px-3 py-2 text-sm">
              <div>
                <span className="font-medium">{e.feature}</span>
                {isAIProfile(e.feature) ? (
                  <AIModelTiers entitlement={e} planId={planId} onSaved={applyEntitlement} />
                ) : (
                  <span className="text-muted-foreground ml-2">
                    ({e.value_type}: {e.value})
                  </span>
                )}
                {e.quota_period && (
                  <span className="text-muted-foreground ml-1">
                    / {e.quota_period}
                    {e.quota_unit ? ` (${e.quota_unit})` : ""}
                  </span>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-1">
                {!isAIProfile(e.feature) && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    disabled={save.isPending}
                    onClick={() => openEditor(e)}
                  >
                    <Pencil className="h-3 w-3" /> {t("common.edit")}
                  </Button>
                )}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  onClick={() => setConfirmDelete({ id: e.id, feature: e.feature })}
                >
                  <Trash2 className="h-3 w-3 text-destructive" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
      {editor && (
        <div className="border rounded p-3 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label className="text-xs">{t("plans.feature")}</Label>
              <Input
                aria-label={t("plans.feature")}
                value={newEnt.feature}
                readOnly={!!editor.original}
                onChange={(e) => {
                  const feature = e.target.value
                  setNewEnt((n) => ({
                    ...n,
                    feature,
                    ...(isAIProfile(feature.trim()) && n.value_type !== "string"
                      ? { value_type: "string", value: "" }
                      : {}),
                  }))
                }}
                placeholder={t("plans.featurePlaceholder")}
              />
            </div>
            <div className="space-y-2">
              <Label className="text-xs">{t("plans.valueType")}</Label>
              <Select
                disabled={isAIProfile(newEnt.feature.trim())}
                value={newEnt.value_type}
                onValueChange={(v) => setNewEnt((n) => ({ ...n, value_type: v }))}
              >
                <SelectTrigger aria-label={t("plans.valueType")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="bool">{t("plans.boolean")}</SelectItem>
                  <SelectItem value="int">{t("plans.integer")}</SelectItem>
                  <SelectItem value="string">{t("plans.string")}</SelectItem>
                  <SelectItem value="quota">{t("plans.quota")}</SelectItem>
                  <SelectItem value="flag">{t("plans.flag")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className={isAIProfile(newEnt.feature.trim()) ? "col-span-2 space-y-2" : "space-y-2"}>
              <Label className="text-xs">{t("plans.value")}</Label>
              {isAIProfile(newEnt.feature.trim()) ? (
                <textarea
                  className="min-h-32 w-full rounded-md border bg-background p-2 font-mono text-sm"
                  aria-label={t("plans.value")}
                  value={newEnt.value}
                  onChange={(e) => setNewEnt((n) => ({ ...n, value: e.target.value }))}
                />
              ) : (
                <Input
                  aria-label={t("plans.value")}
                  value={newEnt.value}
                  onChange={(e) => setNewEnt((n) => ({ ...n, value: e.target.value }))}
                  placeholder={t("plans.valuePlaceholder")}
                />
              )}
            </div>
            {newEnt.value_type === "quota" && (
              <>
                <div className="space-y-2">
                  <Label className="text-xs">{t("plans.quotaPeriod")}</Label>
                  <Select
                    value={newEnt.quota_period}
                    onValueChange={(v) => setNewEnt((n) => ({ ...n, quota_period: v }))}
                  >
                    <SelectTrigger>
                      <SelectValue placeholder={t("plans.selectPeriod")} />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="hourly">{t("plans.hourly")}</SelectItem>
                      <SelectItem value="daily">{t("plans.daily")}</SelectItem>
                      <SelectItem value="monthly">{t("plans.monthly")}</SelectItem>
                      <SelectItem value="yearly">{t("plans.yearly")}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label className="text-xs">{t("plans.quotaUnit")}</Label>
                  <Input
                    value={newEnt.quota_unit}
                    onChange={(e) => setNewEnt((n) => ({ ...n, quota_unit: e.target.value }))}
                    placeholder={t("plans.quotaUnitPlaceholder")}
                  />
                </div>
                {/* Optional Stripe Billing Meter binding. When non-empty,
                    every RecordUsage call for this feature enqueues a
                    meter event the background sync pushes to Stripe.
                    Configure the meter (and event_name) in your Stripe
                    dashboard first. */}
                <div className="space-y-2 col-span-2">
                  <Label className="text-xs">{t("plans.stripeMeterEventName")}</Label>
                  <Input
                    value={newEnt.stripe_meter_event_name}
                    onChange={(e) => setNewEnt((n) => ({ ...n, stripe_meter_event_name: e.target.value }))}
                    placeholder={t("plans.meterEventPlaceholder")}
                  />
                  <p className="text-[10px] text-muted-foreground">{t("plans.stripeMeterEventNameHelp")}</p>
                </div>
              </>
            )}
          </div>
          {save.isError && (
            <p role="alert" className="text-sm text-destructive">
              {errorMessage(save.error, locale)}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" disabled={save.isPending} onClick={() => setEditor(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={save.isPending || !newEnt.feature.trim()}
              onClick={() => save.mutate()}
            >
              {save.isPending ? t("common.loading") : t(editor.original ? "common.save" : "plans.addEntitlement")}
            </Button>
          </div>
        </div>
      )}

      <AlertDialog open={!!confirmDelete} onOpenChange={() => setConfirmDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("plans.deleteEntitlementTitle", { feature: confirmDelete?.feature || "" })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t("plans.deleteEntitlementDesc")}</AlertDialogDescription>
          </AlertDialogHeader>
          <div className="flex justify-end gap-2">
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-white hover:bg-destructive/90"
              onClick={() => confirmDelete && deleteMut.mutate(confirmDelete.id)}
            >
              {t("common.delete")}
            </AlertDialogAction>
          </div>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
