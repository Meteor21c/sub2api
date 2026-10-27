# meteor-mcp-bridge

独立的兼容层，用于保留第三方生图 MCP 和盈合（FZYinghe）视频 MCP
的 HTTP/JSON-RPC 合约，同时让 Sub2API 继续负责用户鉴权、分组调度和计费。

旧服务器上的 `/mcp/image`、`/mcp/video` 只是 New API 暴露的兼容入口，
并不是一个需要原样搬运的独立 MCP 容器。真正的上游仍是已配置的第三方
生图渠道和 `api-aigc.fzyinghe.com`；本服务把它们接到 Sub2API 的官方媒体
接口。Sub2API 在转发盈合请求时补充可信用户身份与预扣单号，桥接层不直接修改用户余额。

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
场景单价 × 1.15 × 实际输出 Token 结算；盈合 Kling 使用同一密钥目录的
音频、参考视频及分辨率场景秒价 × 1.15 × 实际视频秒数结算。
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

桥接器支持的模型为 `doubao-seedance-2.0`、`doubao-seedance-2.0-fast`、
`doubao-seedance-2.0-mini`、`doubao-seedance-2.5`、
`doubao-seedance-1.5-pro`、`kling-v3`、`kling-v3-omni`。
实际可用模型和分辨率还须通过当前密钥的上游价格目录校验；目录缺少价格的模型不发布。
生产账号的 `model_mapping` 应分别限定为豆包和 Kling 对应模型；
不要继续发布已无授权的 cheap/海外 Seedance 别名。1.5 Pro 时长为 4–12 秒，
2.5 为 4–30 秒，其余模型按各自上游限制校验。

视频请求仍由 Sub2API 扣除用户余额；桥接层不直接改余额。
豆包 Seedance 创建前用视频分组的旧每秒配置计算可退还预扣额，
完成后按上游任务输出 Token 与创建时冻结的密钥专属场景单价结算，
失败释放预扣额；豆包预扣秒价不作为最终售价。盈合 Kling 则按创建时冻结的
上游折后秒价加价 15% 预扣和结算，不叠加分组倍率。素材和音频组合由上游文档约束，
Omni 参考视频不支持同时生成音频。本站视频余额按用户指定的
人民币／美元 1:1 数值口径换算。密钥专属价格不可用时拒绝创建，避免误扣。

## Seedance 自动素材库

默认启用 `FZY_AUTO_ASSETS_ENABLED=true`。Seedance 有参考素材时，沿用现有预扣，
立即返回持久化的 `meteorasset-<UUID>` 任务号；后台依次创建 V3 虚拟人像素材组、
提交素材并查询审核，通过后才创建视频。纯文字任务与 Kling 继续原有流程。
素材接口使用上游默认审核策略，不请求跳过审核；是否接受素材由上游决定。

- 素材按照可信用户 ID 与实际上游密钥隔离；相同文件内容可复用已通过的素材，
  远程参考按素材类型和完整 URL 复用。相同预扣单号只能入队一次。
- 网页和 MCP 返回 `stage`、`asset_progress`，分别显示入库、审核、生成及提交待确认。
  审核最长等待 30 分钟；明确失败由 Sub2API 原有退款流程释放预扣。
- 上游素材下载超时与内容审核拒绝分别显示；`error.type` 和 `error.code` 提供安全的失败分类，
  不返回源文件签名链接或上游错误里的私密信息。只有已确认失败且明确属于临时下载超时的素材，
  才在 30 秒、60 秒后各重新入库一次；次数和幂等标识持久化，重启不重置。
  审核拒绝、权限不足和永久下载失败不自动重试；已失败并释放预扣的原任务不自动恢复。
  这些重试仅针对素材入库，付费视频始终只提交一次。被替换的失败素材仍按保留期逐件清理。
- 本地上传链接供 PUT 使用，15 分钟内有效；上传完成后的素材读取链接约 24 小时内有效，
  长于 30 分钟的审核等待上限。临时下载超时不会被误报为审核违规。
- 付费视频提交发生超时、5xx、408/409、缺少任务号或提交后本地落盘失败时，
  保留 `submission_unknown`，不自动重发或退款。管理员应核对上游任务与账单，
  用户应继续查询原任务，避免重复提交产生费用。
- 每天扫描 7 天未使用的素材，每次逐个删除；审核、生成或提交待确认的任务引用
  会阻止清理。任务终态后重新计时，拒绝删除的素材稍后重试，空素材组也逐个清理。
  这是上游素材库保留期，本站短期上传/结果签名链接仍按原有效期过期。

队列保存在 `data/fzy-asset-queue/encrypted-state.json`，包括加密的上游凭据与待处理请求，
使用 `MCP_ASSET_SIGNING_SECRET` 派生的 AES-GCM 密钥。必须持久化并备份该目录，
保持签名密钥稳定；密钥错误或状态无法解密时启动会失败并保留原文件。
服务重启会恢复审核；已进入付费提交但结果未落盘的任务转为待确认，不再次提交。
队列单进程运行，每 5 秒最多推进一次上游操作，并限制排队数与状态文件大小。

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
负责对外暴露兼容地址。`data/` 需要由容器用户可写，保存临时素材、任务元数据、
短期签名结果及加密的自动素材队列。

升级自动素材功能时，先更新 Sub2API 后端，使其传入可信身份头，再更新桥接器和
`deploy/media-ui` 静态页。静态页需包含新增 `video-asset-status.js`，按 `index.html`
顺序加载。后端与桥接镜像均由 GitHub Actions 构建；生产主机只拉取固定版本并
单服务切换。切换前备份数据库、编排配置、桥接数据和稳定签名密钥，并保留旧镜像。

真实图片/视频生成会产生上游费用，部署验收只应使用 `/health`、MCP
`initialize`/`tools/list`、Sub2API `/v1/models` 和缺少 `model` 的校验请求。

## Conservative delivery and storage limits

Video playback/download stays behind this bridge on the current production
host. It never redirects a browser to the provider's storage. Public-address
validation is repeated on every redirect, and the validated DNS address is
pinned to the actual HTTP(S) connection.

- Complete videos stream to a separate `/data/video-cache` directory, with a
  **5 GiB global**, **512 MiB per authenticated owner**, **256 MiB per file**
  maximum. Only one full fill runs at a time; repeated fills for the same asset
  share one promise, and other assets bypass a busy cache without queuing.
- Cache entries expire after at most **24 hours**, independently of download
  signatures. Every minute, TTL/LRU cleanup unlinks individually validated
  cache paths; active readers/writers remain protected. An interrupted `.part`
  never becomes readable, and crash leftovers are reclaimed after 10 minutes.
- Below **10 GiB free disk** or above **80% used**, new caching is bypassed.
  This cache is disposable and **must be excluded from backup archives**.
- A normal authenticated successful status read can prefetch a complete video;
  a billing-only status read never downloads it. A cold GET streams immediately
  while one background fill prepares later hits. Cache failure keeps bounded live streaming and
  never resubmits or changes billing for a completed generation.
- Every cache hit is behind the existing signed asset or authenticated task
  check. Signed links last **30 minutes** and can be renewed by an authenticated
  task lookup. Holding a still-valid capability permits download until its
  expiry; key revocation/logout does not instantly revoke an issued capability.
- Live downloads and disk reads stop when clients disconnect or the two-minute
  delivery deadline expires. Up to **8 GET** downloads and **2 HEAD** probes
  run at once. HEAD uses HEAD upstream, or a one-byte probe if unsupported.
  Cached files support a single byte Range, including suffix/open-ended forms.
- Temporary uploads/generated images retain their **24-hour** expiry and
  periodic cleanup, with configurable budgets capped at **256 MiB per key**
  and **2 GiB globally**. The single image input limit remains 10 MiB and the
  supported upload formats remain JPEG, PNG, and WebP.
- Image requests are limited to one concurrent paid submission, uploads to two;
  upstream image JSON is checked at declared and actual **48 MiB**, control
  JSON at **2 MiB**, and decoded images at **32 MiB**. Busy image requests are
  rejected before submission and should be retried by the user.
- Compose defaults to **640 MiB** hard memory, **256 MiB** Node heap, **1 CPU**
  and **128 PIDs**. These are ceilings, not preallocated RAM. Keep other apps'
  headroom in mind before raising limits.
- `/health` is liveness; internal `/ready` checks initialized storage, writable
  data and a minimal disk reserve and exposes aggregate cache counters. Do not
  proxy `/ready` publicly. Docker's healthcheck uses `/ready` without a paid call.
- Upstream material-library idle expiry remains **7 days** with a daily scan;
  active job references are retained. Terminal queue records retain the existing
  30-day bound. `FZY_ASSET_CONCURRENCY=2` permits two distinct scopes while each
  scope, persistence and destructive upstream cleanup keep their safety locks.

Disable only the optional disk cache with `MCP_VIDEO_CACHE_ENABLED=false`.
Limits can be reduced with `MCP_VIDEO_CACHE_MAX_BYTES`,
`MCP_VIDEO_CACHE_OWNER_BYTES`, `MCP_VIDEO_CACHE_FILE_BYTES`,
`MCP_VIDEO_CACHE_TTL_SECONDS`, `MCP_OWNER_STORAGE_BYTES`, and
`MCP_GLOBAL_STORAGE_BYTES`; code caps prevent accidental oversized budgets.
`MCP_VIDEO_CACHE_MIN_FREE_BYTES` can increase the free-space reserve.
