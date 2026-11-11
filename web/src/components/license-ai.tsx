import { useMutation, useQuery } from "@tanstack/react-query"
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
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { type AILimits, licenseAI } from "@/lib/api"

const empty: AILimits = { enabled: false, group_id: 0, quota: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }
const labels = {
  group_id: "jiji 分组 ID",
  quota: "每月总额度（USD，必须大于 0）",
  rate_limit_5h: "5 小时限额（USD，0 不限制）",
  rate_limit_1d: "1 天限额（USD，0 不限制）",
  rate_limit_7d: "7 天限额（USD，0 不限制）",
}
const states: Record<string, string> = {
  pending: "处理中",
  running: "处理中",
  synced: "已同步",
  done: "已完成",
  failed: "失败",
  uncertain: "结果待确认",
  ready: "就绪",
  attention: "需要管理员处理",
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
  return (
    <div className="space-y-3">
      {Object.entries(labels).map(([key, label]) => (
        <Label key={key} className="grid gap-2">
          {label}
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
        jiji 账号
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
            <DialogTitle>jiji 管理账号</DialogTitle>
            <DialogDescription>令牌只留在服务器加密存储；留空保留，重新录入后恢复自动同步。</DialogDescription>
          </DialogHeader>
          <p>状态：{states[query.data?.state || ""] || query.data?.state || "未配置"}</p>
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
              启用后台同步
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
              Access token 到期时刻（本机时区）
              <Input
                type="datetime-local"
                required={!!(access || refresh)}
                value={expires}
                onChange={(e) => setExpires(e.target.value)}
              />
            </Label>
            <Button disabled={save.isPending || query.isPending || query.isError}>保存账号</Button>
            {save.isSuccess && <p role="status">已保存</p>}
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function AIPlanDialog({ id }: { id: string }) {
  const [open, setOpen] = useState(false),
    [value, setValue] = useState(empty)
  const query = useQuery({ queryKey: ["ai-plan", id], queryFn: () => licenseAI.plan(id), enabled: open, retry: false })
  useEffect(() => {
    if (query.data) setValue(query.data)
  }, [query.data])
  const save = useMutation({
    mutationFn: () => licenseAI.savePlan(id, value),
    onSuccess: () => {
      void query.refetch()
    },
  })
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        AI 默认配置
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>新许可证 AI 默认配置</DialogTitle>
            <DialogDescription>
              默认关闭。只影响之后签发的许可证；现有许可证不回填。新客户端就绪后再开启。
            </DialogDescription>
          </DialogHeader>
          <Failure error={query.error} />
          <Failure error={save.error} />
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              save.mutate()
            }}
          >
            <Label className="flex gap-2">
              <input
                type="checkbox"
                checked={value.enabled}
                onChange={(e) => setValue({ ...value, enabled: e.target.checked })}
              />
              新签发时自动创建专属 Key
            </Label>
            <Limits value={value} onChange={setValue} />
            <Button disabled={save.isPending || query.isPending || query.isError}>保存默认配置</Button>
            {save.isSuccess && <p role="status">已保存</p>}
          </form>
        </DialogContent>
      </Dialog>
    </>
  )
}

export function LicenseAICard({ id }: { id: string }) {
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
    if (limits) setValue(limits)
  }, [limits])
  const save = useMutation({
    mutationFn: () => licenseAI.save(id, value),
    onSuccess: () => {
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
    reset_quota: "重置总已用额度",
    reset_rate: "重置三个周期用量",
    rotate: "更换 Key",
    retry: "重试同步",
  }
  const busy = save.isPending || action.isPending
  return (
    <Card>
      <CardContent className="space-y-4 pt-6">
        <h3 className="font-semibold">AI 服务</h3>
        <Failure error={query.error} />
        <Failure error={save.error} />
        <Failure error={action.error} />
        {query.isPending ? (
          <p>读取中…</p>
        ) : !query.data?.enrolled ? (
          <p>此许可证未开通 AI 自动绑定，保持原有行为。</p>
        ) : (
          binding && (
            <>
              <p role="status">
                {states[binding.state] || binding.state} · {query.data.key_hint || "Key 准备中"} · 凭据版本{" "}
                {binding.revision}
              </p>
              <p className="text-sm">
                Key ID：{binding.remote_id || "—"}；到期：{binding.remote.expires_at || "—"}；已用：
                {binding.remote.quota_used ?? "—"} USD
              </p>
              {binding.last_error && (
                <p role="alert" className="text-destructive">
                  {binding.last_error}
                </p>
              )}
              <p className="text-sm text-muted-foreground">
                按 {binding.timezone} 每月重置，原始起点 {binding.anchor}。重置总额度不重置周期用量。
              </p>
              <details>
                <summary>调整此许可证额度</summary>
                <form
                  className="space-y-3 mt-3"
                  onSubmit={(e) => {
                    e.preventDefault()
                    save.mutate()
                  }}
                >
                  <Limits value={value} onChange={setValue} fixedGroup />
                  <Button disabled={busy}>保存额度并同步</Button>
                </form>
              </details>
              <div className="flex flex-wrap gap-2">
                {Object.entries(actions).map(([name, label]) => (
                  <Button key={name} variant="outline" disabled={busy} onClick={() => setConfirm(name)}>
                    {label}
                  </Button>
                ))}
              </div>
              <details>
                <summary>同步操作记录</summary>
                <ul className="text-xs space-y-2 mt-2">
                  {query.data.jobs?.map((j) => (
                    <li key={j.id}>
                      {actions[j.action] || j.action} · {states[j.state] || j.state} · {j.phase} {j.last_error}
                    </li>
                  ))}
                </ul>
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
              <AlertDialogTitle>{actions[confirm]}</AlertDialogTitle>
              <AlertDialogDescription>
                {confirm === "rotate"
                  ? "新 Key 验证后禁用旧 Key，再发布新绑定。本周期额度恢复至配置上限，客户端需刷新授权；失败模型请求不会自动重发。"
                  : confirm === "reset_quota"
                    ? "将总已用额度归零，恢复至配置上限；周期用量和会员有效期保持不变。"
                    : confirm === "reset_rate"
                      ? "清零三个周期用量；总已用额度和会员有效期保持不变。"
                      : "仅重试可安全恢复的操作。结果不明的重置须人工核对，不会自动重复执行。"}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <div className="flex gap-2 justify-end">
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction onClick={() => action.mutate(confirm)}>确认</AlertDialogAction>
            </div>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  )
}
