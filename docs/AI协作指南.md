# Keygate AI 协作指南

本指南承接根目录 `AGENTS.md` 的导航。按任务读取对应章节，不要为了普通文案或小范围配置修改完整读取部署、备份和发布说明。

## 1. 工具与上下文

### CodeGraph

- 不熟悉的模块、跨文件调用链、依赖追踪、架构问题或影响范围分析，先调用 `codegraph_explore`，再读取图谱指向的最小代码片段。
- 图谱结果过期时运行 `codegraph sync`；大规模重命名、目录移动或分支级重构后运行 `codegraph index`。
- 已知文件、精确字符串、小范围编辑、SQL、Docker、YAML 和 Shell 配置优先使用原生工具。

### RTK

RTK 只用于嘈杂的、摘要导向的只读命令，例如：

- `rtk git status`、`rtk git log -n 10`
- `rtk go test ./...`、`rtk npm test`
- `rtk docker ps`、`rtk docker compose ps`

精确 diff、原始日志、完整配置、`rg`、`npm audit`、带管道/重定向的命令，以及安装、提交、推送、部署、数据库和备份操作使用原生命令。RTK 输出含义不清时重跑原命令或使用 `rtk proxy <原命令>`；不要用 `rtk read` 读取大型文件或日志。

### 上下文读取

1. 先执行 `git status --short`。
2. 使用 `rg -n` 定位入口、调用方和测试，再读取命中区域及直接依赖。
3. 跨多个任务类型时，只追加读取对应章节和直接引用的文档。
4. 不因小改动做全仓扫描、泛化重构、容器重建或浏览器回归。

## 2. 范围与许可证

- 本仓库保存自维护的 Keygate 源码、许可证约束和源码发布交接规则；厂商部署配置、WMS 业务代码和自研授权/控制平面服务位于其他仓库。
- 上游源码修改必须保留 AGPL v3、Section 7(b)、NOTICE、原 Go module 路径和可见 “Powered by Keygate” 署名。
- 不得删除、隐藏、改写 UI、API 配置响应、`X-Powered-By` 响应头、署名中间件、客户门户、登录页或邮件页脚中的署名，也不得帮助规避该要求。
- Keygate 只能从 `SeanChengN/keygate` 的经过审查的不可变标签构建；不要直接把未审查分支或 `latest` 用于生产。

## 3. 部署与网络

- 每个镜像必须同时固定显式版本和 manifest digest；生产环境只拉取镜像，不在现场复制源码或执行 `--build`。
- SafeLine 独占公网 TCP 80/443，Keygate 和 MinIO 只通过回环地址被代理；管理界面仅允许 WireGuard 管理 IP，MinIO 控制台不得公开。
- 保留既有路径与方法白名单、HTTPS/HSTS、客户端 IP 处理、上传大小限制和未知 Host 拒绝边界；不要直接修改 SafeLine 生成的内部 Nginx 配置绕过许可。
- Compose、Dockerfile、环境变量和网络规则变更前，按需读取 `README.md`、同级控制平面项目的 [deploy/keygate/README.md](../../digital-warehousing-control-plane/deploy/keygate/README.md)、Compose、Dockerfile 和 `.env.example` 的相关片段。
- 不为退役控制平面新增兼容服务，除非用户明确授权新的产品需求。

## 4. 数据与备份

- PostgreSQL 和 MinIO 数据是事实来源；备份变更必须保留加密异地副本、独立恢复流程和恢复验收记录。
- 不提交 `.env`、API key、许可证/签名密钥、数据库 dump、对象数据、备份身份、客户制品或生成的 WMS 文件。
- 数据库、密钥、备份、恢复和升级回滚脚本变更时，先定位脚本调用链和挂载边界，再做最小验证；禁止把真实凭据写入日志或示例文件。
- WMS 授权密钥、updater 制品和 Keygate 运行时数据的边界以同级控制平面项目的 [deploy/keygate/README.md](../../digital-warehousing-control-plane/deploy/keygate/README.md) 为准，不得让业务容器接触 Keygate 授权私密材料。

## 5. 验证与交付

### 常规代码验证

```text
npm test
npm audit
docker compose --env-file deploy/keygate/.env.example -f deploy/keygate/docker-compose.yml config --quiet
git diff --check
```

部署、升级、恢复验证和备份脚本变更时，另外执行对应 Shell 语法检查：

```text
bash -n deploy/keygate/deploy.sh deploy/keygate/upgrade-keygate.sh deploy/keygate/verify-backup.sh deploy/keygate/backup/backup.sh
```

### 版本交接

- Keygate 源码发布、不可变标签和上游交接只按 `docs/dw-release.md` 执行。
- 生产镜像构建、digest 记录、VPS 备份、升级、回滚和验收只按同级控制平面项目的 [deploy/keygate/README.md](../../digital-warehousing-control-plane/deploy/keygate/README.md) 执行。
- 交付前确认 `git status --short` 只包含本次改动，且没有密钥、数据、依赖目录、日志或构建产物。
