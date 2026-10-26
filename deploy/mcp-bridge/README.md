# meteor-mcp-bridge

独立的兼容层，用于保留第三方生图 MCP 和盈合（FZYinghe）视频 MCP
的 HTTP/JSON-RPC 合约，同时让 Sub2API 继续负责用户鉴权、分组调度和计费。

旧服务器上的 `/mcp/image`、`/mcp/video` 只是 New API 暴露的兼容入口，
并不是一个需要原样搬运的独立 MCP 容器。真正的上游仍是已配置的第三方
生图渠道和 `api-aigc.fzyinghe.com`；本服务把它们接到 Sub2API 的官方媒体
接口，不修改 Sub2API 核心源码。

## 兼容地址

- `POST /mcp/image`：`create_image`、`create_material_upload`
- `POST /mcp/video`：`create_video`、`get_video`、`create_material_upload`
- `POST /mcp`：三组工具的兼容入口
- `PUT/GET /mcp/assets/...`：短期签名素材和结果地址

客户端继续使用原来的 Bearer API key。桥接层不会把用户密钥写入日志。
创建素材前会向 Sub2API `/v1/models` 验证该 Key；每个 Key 临时素材/结果最多占用
64 MiB，桥接全局最多占用 512 MiB。远程参考和结果 URL 会逐次 DNS 解析并拒绝
内网/回环/云元数据地址，最多跟随 5 次重定向；视频响应限制为 256 MiB。
已过期的素材不再占用用户临时额度；桥接器每次计量以及后台定时检查时，
逐件回收过期素材。如果生成成功但临时下载链接无法保存，MCP 仍会返回
原生图片块，客户端无需重新发起计费请求。
生图上游等待上限为 5 分钟；达到上限时应先核对计费记录，不要自动重试。
视频状态把盈合 V3 返回的非负整数 Token 用量以 `provider_token_usage`
传给 Sub2API 和媒体页。豆包 Seedance 的专属计费由 Sub2API 以密钥折后
场景单价 × 1.15 × 实际输出 Token 结算；Kling 仍保持原有规则。
服务端补偿轮询携带 `X-Meteor-Billing-Only: 1`，桥接器只返回任务状态与
Token 用量，不下载视频文件。

## 上游路径

- 图片：转发到 Sub2API `/v1/images/generations` 或 `/v1/images/edits`，
  由 Sub2API 的图片账号调用 `image.hajimi.chat`、`api.gapi.cc`、
  `api.viapi.cc` 等已迁移的第三方渠道。
- 视频：转发到 Sub2API `/v1/videos/generations`，轮询其状态接口。
- FZYinghe：作为 Sub2API 的 Grok 兼容账号上游，桥接层的
  `/provider/fzyinghe/v1` 再适配 Kling 的 `/video/generation/tasks`
  和 Doubao Seedance 的 `/v3/video/tasks` 接口。

当前授权范围为 `doubao-seedance-2.0`、`doubao-seedance-2.0-fast`、
`doubao-seedance-2.0-mini`、`doubao-seedance-2.5`、
`doubao-seedance-1.5-pro`、`kling-v3`、`kling-v3-omni`。
生产账号的 `model_mapping` 应分别限定为豆包 5 个和 Kling 2 个模型；
不要继续发布已无授权的 cheap/海外 Seedance 别名。1.5 Pro 时长为 4–12 秒，
2.5 为 4–30 秒，其余模型按各自上游限制校验。

视频请求仍由 Sub2API 扣除用户余额；桥接层不直接改余额。
豆包 Seedance 创建前用视频分组的旧每秒配置计算可退还预扣额，
完成后按上游任务输出 Token 与创建时冻结的密钥专属场景单价结算，
失败释放预扣额；按秒价不再是最终售价。本站视频余额按用户指定的
人民币／美元 1:1 数值口径换算。密钥专属价格不可用时拒绝创建，避免误扣。

## 部署

韩国生产实例使用 GitHub Actions 预构建的固定 `mcpbridge-<commit>` 镜像，
镜像存放在现有的 `ghcr.io/meteor21c/sub2api` 包中。服务器只拉取并切换
`meteor-mcp-bridge` 服务，不在 2 核 / 2 GB 主机上构建。

下面的命令仅供本地开发或独立测试：

```sh
cp .env.example .env
openssl rand -hex 32 > .asset-secret
printf 'MCP_ASSET_SIGNING_SECRET=' > .env
cat .asset-secret >> .env
docker compose up -d --build
curl -fsS http://127.0.0.1:3102/health
```

容器只监听宿主机 `127.0.0.1:3102`；Nginx 的 `/mcp` 和 `/mcp/` location
负责对外暴露兼容地址。`data/` 需要由容器用户可写，里面只保存临时素材、任务元数据和
短期签名结果。

真实图片/视频生成会产生上游费用，部署验收只应使用 `/health`、MCP
`initialize`/`tools/list`、Sub2API `/v1/models` 和缺少 `model` 的校验请求。
