# Meteor Media UI

独立静态图片/视频生成页面，默认挂载到 `https://api.meteor21c.fun/media/`。

## 设计边界

- 图片提交使用 Sub2API 官方 `POST /v1/images/generations` 或 `POST /v1/images/edits`。
- 视频提交使用 Sub2API 官方 `POST /v1/videos/generations`，状态使用官方 `GET /v1/videos/generations/{id}`。
- API Key、分组调度、倍率、亲合度、扣费都由 Sub2API 负责；页面不实现第二套计费规则。
- 视频计价预览读取分组 `video_model_prices` 和当前用户倍率；完成后的实扣、实际折扣从本人 `/api/v1/usage` 记录按任务 ID 核对，预计金额不冒充最终账单。
- 盈合 V3 的 `provider_token_usage` 仅用于展示上游 Token 用量，不参与本站按秒扣费。任务接口不返回上游 Token 单价或授权折扣，页面不会据此推算上游成本。
- Sub2API 当前最多按 15 秒归一化视频计费，且没有 4K 独立价档；页面对超过 15 秒或 4K 的请求不显示精确报价。
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

业务配置：分组倍率独立于公共价格目录；文本使用自动更新的公共目录，媒体价格与模型映射单独审核。除图片/视频外的纯文本上游开启池模式，媒体或混合上游关闭。账号并发设为 0 表示不限；站内新用户默认并发为 10，可在后台按需调整；云端及上游资源限额仍存在。
