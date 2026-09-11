# Availability V2 可执行验收清单

基线：`5d51f4df969baf3c7cebfd5aec51bf4cab1a3aff`
当前基线结论：**V2 未实现，所有依赖 V2 API 的项目均为阻断（BLOCKED）**。
执行原则：每项都需要请求/响应、数据库记录、脱敏日志或可复现测试证据；浏览器 mock、上游目录、header allowlist 和旧 debug-test 不能单独作为通过证据。

状态约定：`[ ]` 待验收，`[x]` 仅表示本 QA 工作树已完成的离线前置检查；`BLOCKED` 表示基线缺失实现，不是“跳过”。

## 0. 测试前置与范围

- [x] `AC-0001` 测试目标是基线 `5d51f4df969baf3c7cebfd5aec51bf4cab1a3aff` 或经发布负责人明确记录的整合提交。
- [x] `AC-0002` 测试仅使用隔离测试环境、脱敏 fixture 和本地离线脚本；不访问 `8.213.128.190`、真实上游、生产账号或生产配置。
- [x] `AC-0003` QA 改动仅位于 `docs/availability-v2/**` 和 `tests/availability-v2/**`；实现代码由后端/前端负责人另行整合。
- [x] `AC-0004` 执行 `python3 tests/availability-v2/verify_contract.py`、记录其 `V2 NOT IMPLEMENTED`/实现状态和退出码；不得把默认模式输出写成集成通过。
- [x] `AC-0005` 执行 `python3 tests/availability-v2/verify_contract.py --require-v2`；基线缺少 V2 时必须非零退出。
- [x] `AC-0006` 不改变任何生产账号的透传、调度、凭证或其他配置；本清单只定义证据和门禁。

## 1. 管理端入口、认证与 envelope

- [ ] `AC-API-001` 服务端注册精确前缀 `/api/v1/admin/channel-test`，并位于既有 `/api/v1/admin` 管理端路由组；普通用户、未认证请求和跨管理员 owner 请求均被拒绝。
- [ ] `AC-API-002` 所有 HTTP 成功响应使用既有 JSON envelope：`code:number`、`message:string`、`data`；可选 `reason:string`、`metadata:object<string,string>` 的类型正确。
- [ ] `AC-API-003` 所有 HTTP 错误也使用同一 envelope，HTTP 状态、`code`、`message` 和安全错误原因一致，不泄露 token、header 或上游凭证。
- [ ] `AC-API-004` V2 的变更和敏感读取进入既有 admin audit；审计记录含安全的 owner、group、account、model、client request 关联信息，不含密钥原文。
- [ ] `AC-API-005` SSE 使用带认证的 `fetch` 流式请求；不依赖不能携带所需认证的原生 `EventSource`。

## 2. 分组、账号目录和指定账号归属

- [ ] `AC-GROUP-001` 页面顶部先选择分组；分组列表复用已有管理端 API。默认分组可自动路由。
- [ ] `AC-GROUP-002` `GET /catalog?group_id=N` 只返回该分组所属且符合当前平台/状态过滤的账号；账号卡片可搜索、滚动，显示 `id/name/platform/status/priority/load_factor/concurrency/eligible/rank`，`reason`（如有）为字符串。
- [ ] `AC-GROUP-003` `load_factor` 可为数字或 `null`，不能把未知值伪造为 0；并发、排队、可用性只有在真实 scheduler 能提供时展示。
- [ ] `AC-GROUP-004` `GET /catalog?group_id=N&account_id=A` 服务端验证 A 属于 N；跨组、已删除、不支持平台或无资格账号必须拒绝或明确 `eligible:false`，绝不静默改用其他账号。
- [ ] `AC-GROUP-005` simple mode 也执行上述 group membership 校验；不能使用“同平台全池”或 `account != nil` 代替归属检查。
- [ ] `AC-GROUP-006` 选定账号后发起测试，整个 turn 链不得自动换成其他账号；若指定账号失败，按契约报告失败，不得无提示切换。
- [ ] `AC-GROUP-007` 未选指定账号时允许真实 scheduler 在同组内故障转移，并在每次 attempt 记录实际账号；上下文不因同组 failover 丢失。

## 3. 模型目录、映射和实测历史

- [ ] `AC-MODEL-001` catalog 的 `models[]` 每项包含 `id`、`upstream_support`（仅 `declared|unknown|unsupported`）、`downstream_allowed`（`boolean|null`）、`source`、`account_ids`、`mapping_effective`；可选 `upstream_model` 为字符串。
- [ ] `AC-MODEL-002` 模型卡片独立标注上游声明支持、下游有效开放、未知；明确显示映射前模型和实际发送的映射后名称。
- [ ] `AC-MODEL-003` upstream catalog、`GetMappedModel`、`IsModelSupported`、透传 allowlist 都不能直接生成“实测成功”或虚构支持列表；实测状态仅来自该分组、实际账号、实际模型的持久测试记录。
- [ ] `AC-MODEL-004` `last_test` 如存在，包含 `at`（RFC3339）、`account_id/account_name/model/success`、`first_response_ms` 和 `total_ms`；未测到首字使用 `null`，禁止用 `0`。
- [ ] `AC-MODEL-005` 最近测试按 `group_id + actual_account_id + actual_model` 隔离；账号/模型/分组不匹配时不会显示为当前卡片结果。
- [ ] `AC-MODEL-006` 分组聚合明确列出实际账号和多次 attempt；聚合失败不能被单次成功或上游声明覆盖。

## 4. Conversation API、永久历史与 owner 隔离

- [ ] `AC-CONV-001` `POST /conversations` 接受 `{group_id, account_id?, model, title?}`；服务器验证分组、指定账号、模型和当前管理员 owner，返回 conversation。
- [ ] `AC-CONV-002` conversation 字段完整且类型正确：`id`、`group_id`、`account_id:number|null`、`model`、`title`、`created_at`、`updated_at`；ID 为数字，时间为 RFC3339。
- [ ] `AC-CONV-003` `GET /conversations?page=1&page_size=20` 返回 `{items:conversation[], total:number}`，支持分页和检索；不能只返回浏览器 localStorage 历史。
- [ ] `AC-CONV-004` `GET /conversations/:id` 返回 `{conversation,turns:turn[]}`，支持 turns 分页或等价的服务端分页参数；列表、详情、stream 均执行 owner 隔离。
- [ ] `AC-CONV-005` 对话和轮次永久存储，不因时间、刷新、重启或 WS sticky TTL 自动过期；新建 conversation 或 reset 不删除旧历史。
- [ ] `AC-CONV-006` 两个管理员/owner 不能读取、修改、stream 或通过详情推断彼此 conversation；同一 group、同一模型、同一上游 response ID 也必须隔离。
- [ ] `AC-CONV-007` 切换分组、模型或指定账号必定创建新 conversation；同组自动故障转移只创建新 attempt，不创建新 conversation。
- [ ] `AC-CONV-008` 历史页面支持分页、检索、窄屏堆叠；刷新详情只恢复服务端终态，不自动重复发送上一条 prompt。

## 5. Turn、上下文和尝试链

- [ ] `AC-TURN-001` 每轮服务端保存用户 `prompt`、完整回答 `content`、`status`、创建/完成时间、首字耗时、总耗时和完整 `attempts[]`。
- [ ] `AC-TURN-002` turn 状态严格为 `running|succeeded|failed|cancelled`；`completed_at` 对 running 为 `null`，终态有明确完成时间；耗时为非负毫秒数字或 `null`。
- [ ] `AC-TURN-003` attempt 字段完整：`id/index/account_id/account_name/requested_model/model/endpoint/status/started_at/completed_at/first_response_ms/total_ms`；可选 `status_code/error/reason` 类型正确。
- [ ] `AC-TURN-004` attempt 状态严格为 `running|succeeded|failed|cancelled`；终态 turn 中不能残留 running attempt；未收到首字的 attempt 使用 `null` 而非 0。
- [ ] `AC-TURN-005` 上下文由服务器从当前 owner、当前 conversation 的已完成成功轮次构造；失败或中断的半截回答不得自动带入下一轮。
- [ ] `AC-TURN-006` 不把上游 `response_id` 跨账号复用；HTTP owner 先例和现有 WS response/account 映射都不能替代 V2 的 conversation owner/actual account 校验。
- [ ] `AC-TURN-007` 成功、失败、取消、上游断流和服务重启后都能从详情恢复明确终态；不出现“客户端看到成功、数据库仍 running”或假成功重试。

## 6. SSE wire contract 与事件顺序

- [ ] `AC-SSE-001` 每个 SSE `data:` 行（或同一事件的多行 data 合并后）均为合法 JSON；禁止把 `[DONE]`、HTML 或裸文本当 V2 事件。
- [ ] `AC-SSE-002` 每个事件包含 `type/conversation_id/turn_id/seq/at`；`type` 仅为 `turn_started|routing|attempt_started|attempt_failed|content_delta|turn_completed|turn_failed|turn_cancelled`。
- [ ] `AC-SSE-003` `seq` 为每轮从 1 开始的严格递增序列；同一轮的 conversation/turn ID 不漂移；`at` 为 RFC3339 且事件时间不倒退。
- [ ] `AC-SSE-004` `attempt_started` 在调用上游前发布，包含实际 `account_id/account_name/requested_model/model/endpoint/status=running/started_at`；事件发生时数据库已有或可追踪的 running attempt。
- [ ] `AC-SSE-005` 上游失败后立即发布对应 `attempt_failed`，包含完整失败 attempt、结束时间、错误/原因和可选状态码；必须先有相同 `attempt_id` 的 `attempt_started`，随后才允许进入下一账号。
- [ ] `AC-SSE-006` `routing` 能区分选路、排队、已发出、等首字、输出、取消、完成等阶段；展示内容来自真实 scheduler/transport，不编造账号。
- [ ] `AC-SSE-007` `content_delta` 的 `delta` 为字符串；服务端持续更新回答但不把半截回答标成成功。
- [ ] `AC-SSE-008` 终态事件只能是最后一个事件且仅出现一次：`turn_completed` 对应 succeeded，`turn_failed` 对应 failed，`turn_cancelled` 对应 cancelled；终态必须携带完整持久 `turn`（含所有 attempts），并与 event 的 IDs 一致。
- [ ] `AC-SSE-009` 无可用账号时直接产生明确失败/不可用结果，不编造 `attempt_started`、账号或成功响应。
- [ ] `AC-SSE-010` 客户端取消优先使用 AbortController；对端中止后发布/持久 `turn_cancelled`，不会进入成功或重复 failover。

## 7. `client_request_id` 幂等和并发边界

- [ ] `AC-IDEMP-001` `POST .../turns/stream` 强制接收非空 `client_request_id`；长度、字符和 owner/conversation 绑定由服务端校验。
- [ ] `AC-IDEMP-002` 同一 owner + conversation + `client_request_id` 的并发重复请求最多创建一条 turn 和一条 attempt 链；第二次返回/复用已存状态，不重新调用上游。
- [ ] `AC-IDEMP-003` 断线后使用同一 request ID 不重复发送；刷新恢复详情时不自动重播；不同 owner 使用相同 request ID 不得命中对方记录。
- [ ] `AC-IDEMP-004` 账号并发槽位在成功、失败、取消、断流和重试路径均恰好释放；不存在泄漏、双释放或重复扣用。

## 8. 真实调度、排序展示和编辑保存

- [ ] `AC-SCHED-001` V2 复用现有 scheduler 的 previous response、guardian、session sticky、load balance、Top-K、tie-break、并发槽位和 DB recheck 约束；没有第二套调度算法。
- [ ] `AC-SCHED-002` catalog/事件显示实际候选 `rank`、候选总数、可用/过滤原因和预计顺序；`scheduling_note` 说明真实决策层，不以 load factor 单独造概率。
- [ ] `AC-SCHED-003` 账号卡片在数据可得时显示 priority、load factor、current concurrency、queue/waiting、可用状态、error rate、TTFT、reset/quota headroom/upstream cost 等因子；未知值保持 `null`/未知。
- [ ] `AC-SCHED-004` scheduler 的 `priority/load/queue/error rate/TTFT/reset/quota/upstream cost/previous/session sticky` 权重变化能在测试 fixture 中解释；权重口径复用现有负载因子和既有配置，不能把 score 当概率承诺，也不能在生产账号上改写这些配置。
- [ ] `AC-SCHED-005` 选定账号、自动路由、sticky 命中、failover 和等待计划在 attempt 链与审计中使用实际账号，且与 DB recheck 后结果一致。
- [ ] `AC-SCHED-006` priority/load factor 修改复用现有账号更新 API；前端仅发送改变字段，不能覆盖 credentials、groups 或其他无关配置。
- [ ] `AC-SCHED-007` 失焦/确认保存有 saving/success/failure 可见状态；失败可恢复并保留用户输入；成功后卡片按真实 scheduler 重新排序。
- [ ] `AC-SCHED-008` 保存操作有服务端鉴权、字段/分组校验和 audit；页面明确提示可能影响其他分组及生产流量。

## 9. 页面、无障碍和数据泄露

- [ ] `AC-UI-001` 顶部为分组选择；左侧账号卡和模型卡独立滚动；右侧为连续对话、实时路由和 attempt 状态；历史支持分页/检索。
- [ ] `AC-UI-002` 窄屏按合理顺序堆叠，不丢失选择、取消、错误和终态；滚动卡片不会触发重复请求。
- [ ] `AC-UI-003` 继承现有主题、亮暗模式和中英 i18n；所有新增文本、错误、saving 状态、空状态和未知/null 值均可翻译。
- [ ] `AC-UI-004` 页面、网络调试输出、历史和审计摘要不展示 Authorization、API key、OAuth token、credentials 或敏感 header；endpoint 仅显示已允许的安全摘要。
- [ ] `AC-UI-005` 屏幕阅读器、键盘焦点、加载/取消/失败状态有可识别文本或 aria 状态；这项必须在真实页面而非仅组件 mock 中验证。

## 10. 兼容性、离线门禁与交付证据

- [ ] `AC-COMP-001` 旧账号和分组 `test/debug-test` 入口仍存在并通过原有回归；不得为了 V2 删除旧接口。
- [ ] `AC-COMP-002` 默认离线脚本验证 envelope、catalog、conversation/list/detail、turn/attempt、SSE JSON、事件类型、seq、RFC3339、终态和 attempt 顺序。
- [ ] `AC-COMP-003` 脚本静态确认既有 envelope、admin auth/audit、旧 debug、被动 monitor、分组关系、simple mode 风险、scheduler、owner 和 header allowlist 证据；不访问网络。
- [ ] `AC-COMP-004` `--require-v2` 在没有 V2 路由、持久化、事件和脱敏集成 fixture 时非零退出；不能用内置 fixture 冒充生产集成。
- [ ] `AC-COMP-005` 运行 `python3 -m py_compile tests/availability-v2/verify_contract.py`、`git diff --check`；工作树 diff 只有允许目录。
- [ ] `AC-COMP-006` 最终报告包含 commit SHA、文件列表、命令及结果、未解决阻断项；没有生产访问、push、部署或高负载服务器操作。

## 11. 推荐最小场景矩阵

| 场景 | 必须观察的证据 |
| --- | --- |
| 同组自动路由成功 | catalog 的真实 rank/依据；`attempt_started` → content → `turn_completed`；完整持久 turn |
| 同组第一账号失败、第二账号成功 | 第一账号 `attempt_failed` 先于第二账号 `attempt_started`；上下文仍在同一 conversation；槽位释放 |
| 指定账号失败 | server group membership 通过；只尝试指定账号；不自动换号；turn_failed 明确原因 |
| 跨组指定账号 | catalog/创建/stream 均拒绝；无泄露账号详情和无伪造 attempt |
| simple mode 跨组账号 | V2 仍按 group fail closed；不能复现既有平台全池风险 |
| 两个 owner 同 response ID | conversation/turn/stream/历史均隔离；WS/HTTP 映射不串链 |
| 同一 `client_request_id` 双提交 | 只有一个持久 turn、attempt 链和上游调用；重试返回已存状态 |
| AbortController/浏览器断开 | turn_cancelled 或明确失败持久化；刷新恢复；不自动重复请求 |
| 未测到首字 | `first_response_ms:null`，不是 0；终态和总耗时仍可解释 |
| 模型映射/透传 | 显示 requested/upstream、声明/下游开放/unknown、mapping_effective 和实际 test；不从 allowlist 伪造成功 |
| 编辑 priority/load factor | 只发变更字段；服务端 audit/校验；保存中/失败/成功 UI；成功后按真实 scheduler 重排 |
