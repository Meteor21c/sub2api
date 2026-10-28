# 创作画布试用 API

此 API 仅保存工程元数据，使用本站既有 JWT、所有者配置和统一钱包。试用入口为 `/studio/canvas`，由独立 Canvas 前端提供；原图片、视频页面保留。

## 访问与限制

- 所有接口位于 `/api/v1/canvas`，必须经过本站 JWT 验证。
- 仅 `BUSINESS_MONITOR_OWNER_ID` 指定的站点所有者，且当前数据库角色为 `admin` 可以读写。共享管理 API Key 不支持。
- 每个所有者最多 20 个工程，单工程 1 MiB，合计 10 MiB；每工程最多 300 个节点和 600 条连线。
- 当前试用仅支持 `text`、`image`、`group` 节点。工程不能包含密钥、登录凭据或内嵌 data URL。
- 媒体仍由原图片、视频服务管理；保存工程不延长缓存有效期。工程保存接口不下载远程素材、不创建或重试生成任务。

## 接口

| 方法与路径 | 请求 | 返回 data |
| --- | --- | --- |
| `GET /access` | 无 | `{enabled, allowed, limits:{maxProjects,maxProjectBytes,maxOwnerBytes}}` |
| `GET /projects` | 无 | 工程 JSON 数组 |
| `POST /projects` | `{data:工程}` | 已保存工程（包含新的 `revision`） |
| `POST /projects/sync` | `{projects:工程数组}` | 保存成功后的所有工程数组 |
| `POST /projects/delete` | `{ids:工程ID数组}` | `{deleted:true}` |

所有响应使用本站 `{code,message,data}` 格式，并设置 `Cache-Control: no-store` 与 `Vary: Authorization`。

新工程的 `revision` 为 0 或省略；更新时必须提交最后一次成功保存返回的版本号。服务器每次保存将版本号加一，更新 `updatedAt`。版本不匹配返回 HTTP 409，前端须保留本地编辑并提示重新加载或导出，不能静默覆盖。

批量同步为数据库事务，单个项目无效、版本冲突或额度超限均不会部分保存。每个所有者的写操作由事务锁串行处理，读写 SQL 均带 `owner_id` 条件。

表为 `canvas_pilot_projects`，迁移文件 `249_canvas_pilot_projects.sql`。工程行随用户删除而级联清理，单独删除工程只删除该工程元数据；不会取消上游生成或触发退款。
