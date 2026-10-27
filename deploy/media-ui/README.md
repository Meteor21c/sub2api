# Meteor Media UI

独立静态图片/视频生成页面，默认挂载到 `https://api.meteor21c.fun/media/`。

## 设计边界

- OpenAI/Grok 图片提交使用 Sub2API `POST /v1/images/generations` 或 `POST /v1/images/edits`；Gemini 图片模型改走 Gemini 原生 `POST /v1beta/models/{model}:generateContent`，把提示词和本地参考图转换为 Gemini `contents[].parts`，并将 `inlineData` 图片结果转换为页面可展示格式。Gemini 的“图片数量”按张顺序调用，避免依赖模型不一定支持的 `candidateCount`。
- 视频提交使用 Sub2API 官方 `POST /v1/videos/generations`，状态使用官方 `GET /v1/videos/generations/{id}`。
- API Key、分组调度、倍率、亲合度、扣费都由 Sub2API 负责；页面不实现第二套计费规则。
- 盈合视频的模型可用性、场景与分辨率经认证的 `/v1/videos/pricing?catalog=true&model=...` 从实际路由账号所用密钥的模型目录读取；目录中没有的盈合模型不再显示，不用硬编码分辨率兜底。页面按用户 Key 缓存目录 60 秒，切换 Key 时立即失效；模型、声音和参考视频变更后重新匹配场景与报价。使用参考视频的 Kling Omni 按上游接口要求仅提供无声生成。
- 盈合 Seedance 仍按实际输出 Token 结算，先按预估额预扣；盈合 Kling 按实际秒数结算并按所选时长预扣。两者均使用当前密钥专属场景上游折后价加价 15%，不再叠加分组或用户倍率；成功后多退少补，失败退款。页面显示官方价、上游折后价、本站价、相对官方折扣、预扣额以及目录来源和读取时间。本站按用户指定的人民币／美元 1:1 数值口径换算。
- 生成前必须取得与当前参数一致的有效报价；服务端创建任务时重新取价并固定实际价格快照。历史任务只沿用该任务自己的快照，不套用当前表单的价格。完成后的实扣、实际折扣从本人 `/api/v1/usage` 记录按任务 ID 核对，预计金额不冒充最终账单；历史报价只保存白名单计费字段，不保存密钥或任意上游元数据。
- 盈合 Seedance 参考素材自动入库、等待审核后生成；相同素材可在当前用户与盈合账号内复用，7 天未使用的素材由服务端逐个清理，正在处理的引用会保留。视频页显示入库、审核、提交与生成进度，历史记录只保存经过校验的阶段与计数，不保存上游素材 ID、账号密钥或源文件。
- 素材审核期间每 10 秒查询状态，提交结果待确认时每 30 秒查询，普通视频生成维持每 5 秒。页面最多连续查询 60 分钟，离开页面不取消后台任务；重新打开本页可继续读取状态。提交待确认时不会自动重新创建视频。
- 视频完成后优先读取经过任务鉴权的短期播放地址，受保护的内容接口通过带鉴权的请求读取。读取失败会保留已完成状态并显示“重新读取视频”，重新打开页面也会恢复已有任务，不重新生成或再次扣费。部署时需同步新增 `video-results.js`。
- 广州交付使用 `https://media.meteor21c.fun/v1/video/{加密令牌}`。该精确域名与路径的播放器发生读取错误时，每个任务在当前登录与历史记录周期内只自动读取一次原有鉴权内容接口，并将播放和下载更新为内存 Blob；其它第三方视频地址不触发此回退。切换账号、清空历史或已失效的读取不会更新页面，临时 Blob 会释放；回退失败仍显示已完成和“重新读取视频”，不会创建新任务或再次扣费。
- 原生 Grok 视频保留分组 `video_model_prices` 与当前用户倍率的按秒报价；超过 15 秒或 4K 的请求不显示精确报价。
- 视频密钥下拉只列当前账号有效的视频分组密钥；缺少密钥时显示创建入口，不填入静态示例模型。
- `/mcp/image`、`/mcp/video` 仍由独立桥接层提供给第三方 MCP 客户端。只有本地视频参考素材需要通过桥接层的短期签名上传工具。
- 页面加载只自动读取当前用户、有效媒体密钥和模型列表，不自动生成。
- 图片档位取自当前密钥所属分组的 `image_price_1k/2k/4k` 配置，再由档位和画幅生成标准 `size` 参数；上游仍会最终校验模型是否支持该尺寸。
- `account-keys.js` 使用同源 `auth_token` 登录态，只读取本人的 Key；Key 只存于内存，选项中仅放 ID 和名称。退出或切换账号后清空。
- 默认按可见分组的图片能力或视频模型价格识别媒体密钥，不依赖固定分组 ID；旧部署若需固定分组，可在 `MeteorAccountKeys.createClient` 传入 `groups.image` / `groups.video`。
- 侧边栏使用原生 Markdown 内容页嵌入：将 `meteor-image.md`、`meteor-video.md` 放入 Sub2API 的 `data/pages/`，后台自定义菜单分别使用 `md:meteor-image` 和 `md:meteor-video`。不要换成外链菜单：官方版本会给外链自动附加登录 token。

## 部署

将本目录复制到新机 `/var/www/meteor-media-ui`，并在 `api.meteor21c.fun` 的 TLS server 中加入：

```nginx
location = /media { return 301 /media/; }
location ^~ /media/ {
    alias /var/www/meteor-media-ui/;
    index index.html;
    autoindex off;
    add_header Cache-Control "no-cache" always;
}
```

然后执行 `nginx -t && systemctl reload nginx`。该 location 与 Sub2API 容器解耦，后续更新官方容器不会覆盖页面。

本次动态目录与计价更新需要同步 `index.html`、`app.js`、`video-pricing.js`、`media-history.js`，并与提供目录及新计费响应的 Sub2API 后端一起更新。静态脚本的版本参数已更新，以便浏览器加载新文件。

业务配置：分组倍率独立于公共价格目录；文本使用自动更新的公共目录，媒体价格与模型映射单独审核。除图片/视频外的纯文本上游开启池模式，媒体或混合上游关闭。账号并发设为 0 表示不限；站内新用户默认并发为 10，可在后台按需调整；云端及上游资源限额仍存在。
