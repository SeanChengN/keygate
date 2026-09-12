# Release 制品类型与下载契约

Keygate 的一个 Release 可以在同一平台下保存两类制品：

- `application`：默认类型，供现有应用更新、公开 feed 和旧客户端使用。
- `help-media`：独立帮助媒体制品，只能通过受授权下载接口按明确来源版本获取。

数据库唯一槽位为 `(release_id, platform, artifact_kind)`。迁移前的全部记录会因列默认值成为 `application`；同一 Release 可以新增同平台的 `help-media`，但同平台同类型仍会冲突。回退迁移在存在非应用制品时拒绝执行，避免静默丢失媒体记录。

## 管理发布

`POST /api/v1/admin/releases/:id/artifacts` 请求可增加：

```json
{
  "platform": "linux-x64",
  "artifact_kind": "help-media",
  "content_type": "application/vnd.zfg.wms-help-media",
  "expected_size": 47185920,
  "filename": "wms-linux-x64.wmsmedia"
}
```

`artifact_kind` 省略时为 `application`。上传、服务端 SHA-256 复核、签名、原子发布与 yank 规则对两类制品相同；管理界面按“类型 + 平台”显示和限制槽位。对象仍保存在私有 S3/MinIO 中，只通过短效预签名 URL 上传或下载。

## 授权下载

`POST /api/v1/license/download` 请求新增可选 `artifact_kind`：

```json
{
  "license_key": "<license key>",
  "identifier": "<installation id>",
  "platform": "linux-x64",
  "channel": "stable",
  "version": "0.21.13",
  "artifact_kind": "help-media",
  "device_proof": "<fresh proof fields>"
}
```

- 省略 `artifact_kind` 时仍选择 `application`，设备证明动作仍为 `download`。
- `help-media` 必须提供 `version`，不允许使用 latest，设备证明动作固定为 `download_help_media`。
- 证明继续绑定许可证、安装实例、产品、平台、通道和请求版本；应用证明不能跨类型重放为媒体下载。
- 成功响应回显 `version`、`platform`、`artifact_kind`、`file_size` 和 `sha256`。客户端必须全部核对后才能使用短效 URL。
- 公开 Sparkle、Velopack 和 Tauri feed 只返回 `application`，不会公开媒体对象或 registry。

## 兼容性验收

发布前至少验证：旧请求体可下载应用包；同一 Release/平台可同时上传两类制品；媒体缺少版本、错误平台、错误类型和跨动作证明均被拒绝；响应类型、大小和 SHA-256 与数据库记录一致；SafeLine 管理面、MinIO 私有控制台和短效 URL 边界保持不变。
