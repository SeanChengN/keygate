import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Archive, Building2, Eye, Pencil, Plus, RotateCcw, Search, UserRound } from "lucide-react"
import { useState } from "react"
import { Link } from "react-router-dom"
import { showToast } from "@/components/toast"
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
} from "@/components/ui/data-table"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useI18n } from "@/i18n"
import { admin, type Customer, type CustomerInput } from "@/lib/api"
import { formatDate, statusColor } from "@/lib/utils"

const emptyCustomer: CustomerInput = {
  kind: "individual",
  name: "",
  primary_email: "",
  phone: "",
  company: "",
  notes: "",
  external_customer_id: "",
  stripe_customer_id: "",
}

export default function CustomersPage() {
  const { t } = useI18n()
  const qc = useQueryClient()
  const [search, setSearch] = useState("")
  const [kind, setKind] = useState("")
  const [status, setStatus] = useState("active")
  const [page, setPage] = useState(0)
  const [editing, setEditing] = useState<Customer | "new" | null>(null)
  const [viewing, setViewing] = useState<string | null>(null)
  const limit = 30
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "customers", search, kind, status, page],
    queryFn: () =>
      admin.listCustomers({
        search: search || undefined,
        kind: kind || undefined,
        status,
        offset: page * limit,
        limit,
      }),
  })
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin", "customers"] })
  const archiveMut = useMutation({
    mutationFn: ({ id, restore }: { id: string; restore: boolean }) =>
      restore ? admin.restoreCustomer(id) : admin.archiveCustomer(id),
    onSuccess: () => {
      refresh()
      setViewing(null)
      showToast(t("customers.saved"), "success")
    },
  })
  const customers = data?.customers || []
  const total = data?.total || 0

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">
            {t("customers.eyebrow")}
          </p>
          <h1 className="text-2xl font-bold tracking-tight">{t("customers.title")}</h1>
          <p className="text-muted-foreground">{t("customers.subtitle", { count: total })}</p>
        </div>
        <Button onClick={() => setEditing("new")}>
          <Plus className="mr-2 h-4 w-4" />
          {t("customers.new")}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative min-w-64 flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-9"
            value={search}
            placeholder={t("customers.search")}
            onChange={(e) => {
              setSearch(e.target.value)
              setPage(0)
            }}
          />
        </div>
        <select
          className="h-10 rounded-md border bg-background px-3 text-sm"
          value={kind}
          onChange={(e) => {
            setKind(e.target.value)
            setPage(0)
          }}
        >
          <option value="">{t("customers.allKinds")}</option>
          <option value="individual">{t("customers.individual")}</option>
          <option value="organization">{t("customers.organization")}</option>
        </select>
        <select
          className="h-10 rounded-md border bg-background px-3 text-sm"
          value={status}
          onChange={(e) => {
            setStatus(e.target.value)
            setPage(0)
          }}
        >
          <option value="active">{t("customers.active")}</option>
          <option value="archived">{t("customers.archived")}</option>
          <option value="all">{t("customers.allStatuses")}</option>
        </select>
      </div>
      <Card>
        <CardContent className="pt-6">
          {isLoading ? (
            <div className="h-64 animate-pulse rounded-lg bg-muted" />
          ) : (
            <>
              <DataTable>
                <DataTableHeader>
                  <DataTableRow>
                    <DataTableHead>{t("customers.customer")}</DataTableHead>
                    <DataTableHead>{t("common.email")}</DataTableHead>
                    <DataTableHead>{t("customers.phone")}</DataTableHead>
                    <DataTableHead>{t("customers.externalId")}</DataTableHead>
                    <DataTableHead>{t("common.status")}</DataTableHead>
                    <DataTableHead className="w-24">{t("common.actions")}</DataTableHead>
                  </DataTableRow>
                </DataTableHeader>
                <DataTableBody>
                  {customers.length === 0 && <DataTableEmpty colSpan={6} message={t("customers.empty")} />}
                  {customers.map((customer) => (
                    <DataTableRow key={customer.id}>
                      <DataTableCell>
                        <div className="flex items-center gap-3">
                          <div className="flex h-9 w-9 items-center justify-center rounded-lg border bg-muted/50">
                            {customer.kind === "organization" ? (
                              <Building2 className="h-4 w-4" />
                            ) : (
                              <UserRound className="h-4 w-4" />
                            )}
                          </div>
                          <div>
                            <p className="font-medium">{customer.name}</p>
                            <p className="text-xs text-muted-foreground">
                              {customer.company || t(`customers.${customer.kind}` as any)}
                            </p>
                          </div>
                        </div>
                      </DataTableCell>
                      <DataTableCell>{customer.primary_email}</DataTableCell>
                      <DataTableCell className="text-muted-foreground">{customer.phone || "—"}</DataTableCell>
                      <DataTableCell className="font-mono text-xs">
                        {customer.external_customer_id || "—"}
                      </DataTableCell>
                      <DataTableCell>
                        {customer.archived_at ? (
                          <Badge variant="outline">{t("customers.archived")}</Badge>
                        ) : (
                          <Badge className="bg-emerald-100 text-emerald-800">{t("customers.active")}</Badge>
                        )}
                      </DataTableCell>
                      <DataTableCell>
                        <div className="flex">
                          <Button variant="ghost" size="icon" onClick={() => setViewing(customer.id)}>
                            <Eye className="h-4 w-4" />
                          </Button>
                          <Button variant="ghost" size="icon" onClick={() => setEditing(customer)}>
                            <Pencil className="h-4 w-4" />
                          </Button>
                        </div>
                      </DataTableCell>
                    </DataTableRow>
                  ))}
                </DataTableBody>
              </DataTable>
              {total > 0 && (
                <DataTablePagination
                  page={page}
                  totalPages={Math.ceil(total / limit)}
                  total={total}
                  pageSize={limit}
                  onPageChange={setPage}
                />
              )}
            </>
          )}
        </CardContent>
      </Card>
      {editing && (
        <CustomerForm
          customer={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}
      {viewing && (
        <CustomerDetail
          customerId={viewing}
          onClose={() => setViewing(null)}
          onEdit={(customer) => {
            setViewing(null)
            setEditing(customer)
          }}
          onArchive={(customer) => archiveMut.mutate({ id: customer.id, restore: !!customer.archived_at })}
        />
      )}
    </div>
  )
}

function CustomerForm({
  customer,
  onClose,
  onSaved,
}: {
  customer?: Customer
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [form, setForm] = useState<CustomerInput>(
    customer
      ? {
          kind: customer.kind,
          name: customer.name,
          primary_email: customer.primary_email,
          phone: customer.phone || "",
          company: customer.company || "",
          notes: customer.notes || "",
          external_customer_id: customer.external_customer_id || "",
          stripe_customer_id: customer.stripe_customer_id || "",
        }
      : emptyCustomer,
  )
  const save = useMutation({
    mutationFn: () => (customer ? admin.updateCustomer(customer.id, form) : admin.createCustomer(form)),
    onSuccess: () => {
      showToast(t("customers.saved"), "success")
      onSaved()
    },
  })
  const field = (key: keyof CustomerInput, value: string) => setForm((old) => ({ ...old, [key]: value }))
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{customer ? t("customers.edit") : t("customers.new")}</DialogTitle>
          <DialogDescription>{t("customers.formDesc")}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault()
            save.mutate()
          }}
        >
          <div className="space-y-2">
            <Label>{t("customers.kind")}</Label>
            <select
              className="h-10 w-full rounded-md border bg-background px-3 text-sm"
              value={form.kind}
              onChange={(e) => field("kind", e.target.value)}
            >
              <option value="individual">{t("customers.individual")}</option>
              <option value="organization">{t("customers.organization")}</option>
            </select>
          </div>
          <div className="space-y-2">
            <Label>{t("customers.name")}</Label>
            <Input required maxLength={200} value={form.name} onChange={(e) => field("name", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>{t("common.email")}</Label>
            <Input
              required
              type="email"
              value={form.primary_email}
              onChange={(e) => field("primary_email", e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("customers.phone")}</Label>
            <Input maxLength={50} value={form.phone || ""} onChange={(e) => field("phone", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>{t("customers.company")}</Label>
            <Input maxLength={200} value={form.company || ""} onChange={(e) => field("company", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>{t("customers.externalId")}</Label>
            <Input
              maxLength={256}
              value={form.external_customer_id || ""}
              onChange={(e) => field("external_customer_id", e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("customers.stripeId")}</Label>
            <Input
              maxLength={256}
              value={form.stripe_customer_id || ""}
              onChange={(e) => field("stripe_customer_id", e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("customers.notes")}</Label>
            <Input maxLength={4000} value={form.notes || ""} onChange={(e) => field("notes", e.target.value)} />
          </div>
          <div className="flex justify-end gap-2 sm:col-span-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button disabled={save.isPending}>{t("common.save")}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function CustomerDetail({
  customerId,
  onClose,
  onEdit,
  onArchive,
}: {
  customerId: string
  onClose: () => void
  onEdit: (customer: Customer) => void
  onArchive: (customer: Customer) => void
}) {
  const { t } = useI18n()
  const { data, isLoading } = useQuery({
    queryKey: ["admin", "customer", customerId],
    queryFn: () => admin.getCustomer(customerId),
  })
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[88vh] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{data?.customer.name || t("customers.detail")}</DialogTitle>
          <DialogDescription>{data?.customer.primary_email || t("common.loading")}</DialogDescription>
        </DialogHeader>
        {isLoading || !data ? (
          <div className="h-72 animate-pulse rounded-lg bg-muted" />
        ) : (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-muted/30 p-4">
              <div>
                <p className="font-medium">{data.customer.company || t(`customers.${data.customer.kind}` as any)}</p>
                <p className="text-sm text-muted-foreground">{data.customer.phone || t("customers.noPhone")}</p>
              </div>
              <div className="flex gap-2">
                <Button variant="outline" onClick={() => onEdit(data.customer)}>
                  <Pencil className="mr-2 h-4 w-4" />
                  {t("customers.edit")}
                </Button>
                <Button variant="outline" onClick={() => onArchive(data.customer)}>
                  {data.customer.archived_at ? (
                    <RotateCcw className="mr-2 h-4 w-4" />
                  ) : (
                    <Archive className="mr-2 h-4 w-4" />
                  )}
                  {data.customer.archived_at ? t("customers.restore") : t("customers.archive")}
                </Button>
                {!data.customer.archived_at && (
                  <Button asChild>
                    <Link to={`/admin/licenses?customer_id=${data.customer.id}`}>
                      <Plus className="mr-2 h-4 w-4" />
                      {t("customers.issueLicense")}
                    </Link>
                  </Button>
                )}
              </div>
            </div>
            <Tabs defaultValue="licenses">
              <TabsList>
                <TabsTrigger value="licenses">
                  {t("customers.licenses")} ({data.licenses.length})
                </TabsTrigger>
                <TabsTrigger value="subscriptions">
                  {t("customers.subscriptions")} ({data.subscriptions.length})
                </TabsTrigger>
                <TabsTrigger value="overview">{t("customers.overview")}</TabsTrigger>
              </TabsList>
              <TabsContent value="licenses">
                <DataTable>
                  <DataTableHeader>
                    <DataTableRow>
                      <DataTableHead>{t("common.product")}</DataTableHead>
                      <DataTableHead>{t("common.plan")}</DataTableHead>
                      <DataTableHead>{t("common.email")}</DataTableHead>
                      <DataTableHead>{t("common.status")}</DataTableHead>
                      <DataTableHead>{t("common.created")}</DataTableHead>
                    </DataTableRow>
                  </DataTableHeader>
                  <DataTableBody>
                    {data.licenses.length === 0 && <DataTableEmpty colSpan={5} message={t("customers.noLicenses")} />}
                    {data.licenses.map((license) => (
                      <DataTableRow key={license.id}>
                        <DataTableCell>{license.product?.name || "—"}</DataTableCell>
                        <DataTableCell>{license.plan?.name || "—"}</DataTableCell>
                        <DataTableCell>{license.email}</DataTableCell>
                        <DataTableCell>
                          <Badge className={statusColor(license.status)}>{t(`status.${license.status}` as any)}</Badge>
                        </DataTableCell>
                        <DataTableCell>{formatDate(license.created_at)}</DataTableCell>
                      </DataTableRow>
                    ))}
                  </DataTableBody>
                </DataTable>
              </TabsContent>
              <TabsContent value="subscriptions">
                <DataTable>
                  <DataTableHeader>
                    <DataTableRow>
                      <DataTableHead>{t("common.plan")}</DataTableHead>
                      <DataTableHead>{t("common.status")}</DataTableHead>
                      <DataTableHead>{t("customers.provider")}</DataTableHead>
                      <DataTableHead>{t("customers.periodRange")}</DataTableHead>
                    </DataTableRow>
                  </DataTableHeader>
                  <DataTableBody>
                    {data.subscriptions.length === 0 && (
                      <DataTableEmpty colSpan={4} message={t("customers.noSubscriptions")} />
                    )}
                    {data.subscriptions.map((subscription) => (
                      <DataTableRow key={subscription.id}>
                        <DataTableCell>{subscription.plan?.name || "—"}</DataTableCell>
                        <DataTableCell>
                          <Badge className={statusColor(subscription.status)}>
                            {t(`status.${subscription.status}` as any)}
                          </Badge>
                        </DataTableCell>
                        <DataTableCell>{subscription.payment_provider || "—"}</DataTableCell>
                        <DataTableCell>
                          {subscription.current_period_start
                            ? `${formatDate(subscription.current_period_start)} — ${formatDate(subscription.current_period_end)}`
                            : "—"}
                        </DataTableCell>
                      </DataTableRow>
                    ))}
                  </DataTableBody>
                </DataTable>
              </TabsContent>
              <TabsContent value="overview">
                <div className="grid gap-3 sm:grid-cols-4">
                  {[
                    [t("analytics.totalLicenses"), data.licenses.length],
                    [t("customers.totalUsage"), data.total_usage],
                    [t("customers.activeSeats"), data.active_seats],
                    [t("analytics.activations"), data.activations],
                  ].map(([label, value]) => (
                    <Card key={String(label)}>
                      <CardContent className="pt-5">
                        <p className="text-2xl font-bold">{Number(value).toLocaleString()}</p>
                        <p className="text-xs text-muted-foreground">{label}</p>
                      </CardContent>
                    </Card>
                  ))}
                </div>
                {data.customer.notes && (
                  <p className="mt-4 whitespace-pre-wrap rounded-lg border p-4 text-sm">{data.customer.notes}</p>
                )}
              </TabsContent>
            </Tabs>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
