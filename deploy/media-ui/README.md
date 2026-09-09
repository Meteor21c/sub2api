# Meteor Media UI

独立静态图片/视频生成页面，默认挂载到 `https://api.meteor21c.fun/media/`。

## 设计边界

- 图片提交使用 Sub2API 官方 `POST /v1/images/generations` 或 `POST /v1/images/edits`。
- 视频提交使用 Sub2API 官方 `POST /v1/videos/generations`，状态使用官方 `GET /v1/videos/generations/{id}`。
- API Key、分组调度、倍率、亲合度、扣费都由 Sub2API 负责；页面不实现第二套计费规则。
- `/mcp/image`、`/mcp/video` 仍由独立桥接层提供给第三方 MCP 客户端。只有本地视频参考素材需要通过桥接层的短期签名上传工具。
- 页面加载只自动读取当前登录用户和有效媒体 Key、查询模型列表，不自动生成。
- 图片档位取自当前密钥所属分组的 `image_price_1k/2k/4k` 配置，再由档位和画幅生成标准 `size` 参数；上游仍会最终校验模型是否支持该尺寸。
- `account-keys.js` 使用现有同源 `auth_token` 登录态，通过官方用户接口读取本人 Key；Key 只存于内存，选项中仅放 ID 和名称。退出或切换账号后清空；缺少 Key 时由用户自行创建。
- 默认按当前用户可见分组的图片能力或视频定价字段识别媒体分组，不依赖固定分组 ID；旧部署如需固定分组，可在 `MeteorAccountKeys.createClient` 传入 `groups.image` / `groups.video`。
- 侧边栏使用原生 Markdown 内容页嵌入：将 `meteor-image.md`、`meteor-video.md` 放入 Sub2API 的 `data/pages/`，再在后台自定义菜单中分别创建 `md:meteor-image` 和 `md:meteor-video` 页面。不要换成外链菜单：当前官方版本会给外链自动附加登录 token。

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
