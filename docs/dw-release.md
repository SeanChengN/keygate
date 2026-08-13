# WMS 自维护 Keygate 发布流程

本文只负责 `SeanChengN/keygate` 源码仓库中的检查、提交和不可变标签。生产镜像构建及 VPS 升级不在本仓库执行，后续步骤见 `digital-warehousing-control-plane/deploy/keygate/README.md`。

自维护修改必须继续保留 AGPL、NOTICE、原 Go module 路径及所有界面的 “Powered by Keygate” 署名。

## 1. 提交前验证

在本仓库根目录执行：

```powershell
cd .\keygate
go vet ./...
go test ./...
go build ./cmd/server
cd web
bun install --frozen-lockfile
bun run lint
bun run typecheck
bun run build
cd ..
git diff --check
git status --short
```

前端以仓库中的 `web/bun.lock` 为准，不要为了发版生成或提交 `package-lock.json`。如果本机没有 Bun，可先依赖 GitHub `CI` 工作流完成同样检查；只有 `main` 的后端和前端任务全部成功后才允许创建发布标签。

确认测试通过且 `git status --short` 只列出本次要发布的文件。不要提交 `.env`、密钥、数据库、对象存储数据、`node_modules` 或构建产物。

## 2. 先提交并推送代码

通过正常提交或拉取请求把代码合并到远端 `main`。标签必须指向已经推送的发布提交，不能指向修改前的旧提交：

```powershell
git switch main
git pull --ff-only origin main
git status --short
git log -1 --oneline
```

`git status --short` 应没有输出；记录 `git log -1` 显示的发布提交。若代码尚未推送，先完成提交及 `git push origin main`，再继续。

## 3. 最后创建不可变标签

自维护标签格式固定为 `v<上游版本>-dw.<递增序号>`，例如 `v0.1.2-dw.3`。先查看现有标签，再选择未使用的新序号：

```powershell
git tag --sort=-version:refname
$Tag = 'v0.1.2-dw.3' # 示例：替换为本次实际的新标签
git tag -a $Tag -m "Keygate $Tag"
git show --no-patch --oneline $Tag
git push origin $Tag
```

`git show` 中的提交必须就是第二步记录的发布提交。最后一条 `git push origin $Tag` 会自动触发本仓库 `.github/workflows/release.yml` 中的 `Release` 工作流；该工作流调用 `softprops/action-gh-release` 自动创建同名 GitHub 源码 Release，不需要再到 GitHub 页面手工点击“Create a new release”。它不会构建生产容器镜像。

可以在命令行查看并等待这次自动工作流：

```powershell
gh run list --workflow release.yml --limit 5
$RunId = '<上一步显示的本次运行ID>'
gh run watch $RunId --exit-status
gh release view $Tag
```

如果工作流失败，先用 `gh run view $RunId --log-failed` 查看原因；修复仓库权限或临时运行问题后执行 `gh run rerun $RunId --failed`。不要为了重跑 Release 删除、移动或重新推送同名标签。control-plane 构建生产镜像只依赖远端标签存在且能解析到正确提交，不依赖 GitHub Release 页面；但仍建议确认自动 Release 工作流成功后再交接。

如果标签仅在本地创建且指错提交，可在推送前执行 `git tag -d $Tag`，然后重新创建。标签一旦推送到远端，就不要移动、强制覆盖或复用；修复代码后创建下一个 `dw` 序号。

## 4. 交接给镜像与部署仓库

确认 GitHub 中该标签的 `Release` 工作流成功后，转到：

```powershell
cd E:\VSCode\Project\digital-warehousing-control-plane
```

后续只按该仓库 `deploy/keygate/README.md` 的“Keygate 代码发布后的完整升级流程”构建多架构镜像、记录 digest、备份、升级及验收。不要直接使用本仓库 `docker-compose.yml` 中的 `latest` 部署生产环境。
