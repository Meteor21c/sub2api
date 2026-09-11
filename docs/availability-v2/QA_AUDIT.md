# Availability V2 QA 审计

审计基线：`5d51f4df969baf3c7cebfd5aec51bf4cab1a3aff`
审计日期：2026-09-11（Asia/Shanghai）
审计工作树：`/tmp/sub2api-availability-v2-qa`

## 结论

**阻断：基线没有 Availability V2 的后端 API、会话/轮次/尝试持久化或幂等实现。**

本轮只做静态代码审计和离线契约校验，没有访问生产服务器、真实上游、网络或运行集成流量。因此不能把内置 fixture 校验结果表述为 V2 集成通过。`tests/availability-v2/verify_contract.py` 的默认模式会明确报告 `V2 NOT IMPLEMENTED` 与 `INTEGRATION NOT RUN`；`--require-v2` 会在缺少实现证据时失败。

现有的旧 `debug-test` 可以继续保留，但它是一次性上游连通性探测；现有 `channel-monitor-v2` 是从真实使用事实聚合的被动监控。两者都不能替代本规格要求的主动 Availability V2。

## 审计范围与判断口径

- 规格来源：`/Users/meteor/Documents/中转监控/AVAILABILITY_V2_HANDOFF.md`。
- 只将可定位到文件和行号的代码作为证据；“没有找到”表示在基线的后端路由、处理器、服务、仓储和迁移中静态搜索未发现对应 V2 契约实现。
- 上游模型目录、账号模型映射、header allowlist 和旧监控数据都不等价于“该分组、该账号、该模型已实测成功”。
- 账号、分组、管理员和 API key 的身份边界按服务端事实判断，不能由前端卡片或客户端提交值推断。

## 精确代码证据

| 范围 | 代码证据 | 审计发现与 V2 影响 |
| --- | --- | --- |
| JSON envelope | `backend/internal/pkg/response/response.go:14-38` 的 `Response` 定义 `code`、`message`、可选 `reason`/`metadata`/`data`；`Success`/`Created` 从 `:42-56` 使用该结构 | HTTP V2 成功和错误响应必须解包到既有 envelope 的 `data`，不能另造一套顶层格式；SSE 的 `data:` 则直接承载 JSON。 |
| 管理端鉴权和审计 | `backend/internal/server/routes/admin.go:26-32`：`v1.Group("/admin")` 后依次挂 `adminAuth`、全局限流、`auditLog` 和合规 guard | `/api/v1/admin/channel-test` 必须注册在该管理端组下并继承鉴权/审计；不能只在前端暴露页面或绕过管理端中间件。 |
| 旧分组 debug-test | `backend/internal/server/routes/admin.go:337`：`groups.POST("/:id/debug-test", ...)`；`backend/internal/handler/admin/account_handler.go:1330-1367`：`DebugTestGroup` | 旧入口仍是兼容面，必须保留；它没有 V2 的 conversation、turn、attempt 链和 `client_request_id` 幂等语义。 |
| 旧账号 test/debug-test | `backend/internal/server/routes/admin.go:386-387`：账号 `/:id/test`、`/:id/debug-test`；`backend/internal/handler/admin/account_handler.go:1276-1328`：`Test`、`DebugTest` | `DebugTest` 调用 `TestAccountDebug`，返回紧凑的一次性结果，不能作为 V2 SSE 终态或永久历史的实现。 |
| 旧探测确实访问上游 | `backend/internal/service/account_test_debug.go:783-859` 的 `TestAccountDebug` 最终调用 `TestAccountConnection`，把捕获内容压缩为单个结果并生成一次 attempt | 可用于回归旧行为，但不能假称已经实现“调用前 attempt_started、失败立即 attempt_failed、下一账号重试、可恢复终态”等 V2 事件生命周期。 |
| 被动 channel monitor 路由 | `backend/internal/server/routes/admin.go:830-854` 的 `/channel-monitor-v2` 只提供 config、dimensions、snapshot、models、matrix、errors、users | 名称中的 V2 是被动监控版本，不是 `/api/v1/admin/channel-test` Availability V2；不能复用路由名称推断接口存在。 |
| 被动 channel monitor 存储 | `backend/migrations/194_channel_monitor_v2.sql:1-119` 创建 `channel_monitor_v2_*` 聚合、watermark 和 retention 相关表；`:116-119` 明确注释为从真实用户请求派生、**never from active probes** | 这些表没有 conversation、turn、attempt、owner 和 `client_request_id` 幂等实体，不能满足永久保存对话或逐次实测历史。 |
| 被动聚合与保留 | `backend/internal/repository/channel_monitor_v2_aggregation.go:14-31` 定义多级 retention；`:73-155` 执行清理、窗口重算和固定 rollup | 被动指标有自己的窗口和清理策略，不能拿来满足“不自动过期”的 V2 对话/轮次历史。 |
| V2 路由/处理器/持久化缺失 | 基线后端静态搜索没有 `/api/v1/admin/channel-test`、`/catalog`、`/conversations`、`/turns/stream` 的对应注册和 handler，也没有 Availability V2 的 conversation/turn/attempt 迁移或仓储；`frontend/src/router/index.ts:528` 的 `/admin/channel-test` 只是前端页面路由，不能证明后端 API 存在 | 这是当前发布门禁的首要阻断；默认脚本只报告，严格模式失败。 |
| 账号-分组关系 | `backend/ent/schema/account.go:207-215` 通过 `Through("account_groups", AccountGroup.Type)` 声明多对多；`backend/ent/schema/account_group.go:15-59` 定义 `(account_id, group_id)` 复合关系和关联 priority；`backend/internal/repository/account_repo.go:3080-3142` 通过 `group_id` 查询并去重账号 | 指定账号必须由服务端用该关系验证属于所选分组；不能相信前端传来的 `account_id`，也不能把同平台账号当作分组成员。 |
| simple mode 候选池 | `backend/internal/service/openai_gateway_scheduling.go:1487-1492` 在 simple mode 使用 `ListSchedulableByPlatform`，而非分组查询 | simple mode 可能跨分组复用账号。V2 的目录、指定账号执行、每轮 recheck 都必须额外做 group membership 校验并 fail closed。 |
| simple mode 粘性复核 | `backend/internal/service/openai_gateway_scheduling.go:1632-1637` 的 `openAIAccountMatchesSchedulingGroup` 在 simple mode 仅检查 `account != nil`；回归测试 `backend/internal/service/openai_guardian_affinity_test.go:392-402` 明确记录该行为 | 既有普通网关行为不能直接当作 V2 安全保证。V2 必须把 group、管理员 owner、指定 account 一并绑定到持久会话和每次请求。 |
| 真实调度分层 | `backend/internal/service/openai_account_scheduler.go:375-485` 依次处理 previous response、guardian parent、session sticky、load balance | V2 必须复用真实 scheduler 的选择结果和约束，不另写一套“看起来合理”的概率算法；`scheduling_note` 只能解释真实决策。 |
| Top-K 与 tie-break | `backend/internal/service/openai_account_scheduler.go:688-736` 实现 score、priority、load rate、waiting count、account ID 的比较和 Top-K；`:794-846` 对 Top-K 做加权随机顺序 | UI 的 `rank` 是当前候选顺序的可解释快照，不是承诺的概率；验收要覆盖并发、状态变化和 tie-break。 |
| 调度评分因子 | `backend/internal/service/openai_account_scheduler.go:900-1028` 读取 priority/load/queue/error rate/TTFT/reset/quota headroom/upstream cost，并叠加 previous/session sticky；`:2007-2019` 列出对应配置权重 | 账号卡片应展示“可获得时”的真实因子和依据，不能只显示 load factor 或把分数直接转成概率；不得引入第二套调度算法。 |
| 并发槽位与排队 | `backend/internal/service/openai_account_scheduler.go:1151-1237` 做 acquire、fresh/DB recheck、释放和 sticky 绑定；`backend/internal/service/openai_gateway_scheduling.go:1137-1237`、`:1504-1508` 使用 `AccountWaitPlan`/`AcquireAccountSlot`，`:1709-1723` 填充等待计划 | V2 的“预计顺序、并发/排队、可用状态”必须来自这些真实约束，并正确释放槽位；不能仅根据 load factor 计算展示值。 |
| 账号编辑 API | `backend/internal/service/account_service.go:191-204` 的 `UpdateAccountRequest` 支持 `concurrency`、`priority`、`group_ids` 等字段；`:316-400` 逐字段更新、先验证分组、持久化并绑定分组；路由为 `backend/internal/server/routes/admin.go:375` | V2 编辑只应复用现有账号更新 API 并发送修改字段；服务端校验、鉴权、审计和不覆盖无关配置必须可证明。变更可能影响其他分组和生产流量，页面需明确提示。 |
| 模型映射与有效性 | `backend/internal/service/gateway_forward.go:1019-1025` 通过 `GetMappedModel` 得出上游模型；`backend/internal/service/openai_gateway_model_availability.go:59-68` 以 `IsModelSupported` 诊断账号支持；`backend/internal/service/openai_codex_model_metadata.go:171-186` 同时处理 explicit claim、mapping 和 upstream metadata | `declared`、`downstream_allowed`、映射前后名称和实际测试结果必须分栏。`GetMappedModel`/`IsModelSupported` 只能作为静态可用性输入，不能伪造最近实测支持列表。 |
| 多套 header allowlist | `backend/internal/service/openai_gateway_service.go:73-106` 定义普通 OpenAI 与 passthrough 两套 allowlist；`backend/internal/service/openai_gateway_chat_completions_raw.go:19-36` 定义 CC raw 专用 allowlist；`backend/internal/service/gateway_service.go:426-449` 定义 Anthropic/common allowlist | 透传目录不是实测支持证明。V2 必须记录实际 endpoint、实际账号和实际模型；不得把某一套 allowlist 推广到所有上游信任域。 |
| header 实际使用与账号覆写 | `backend/internal/service/openai_gateway_forward.go:1407-1415`、`openai_gateway_passthrough.go:617-629`、`openai_gateway_cc_pipeline.go:204-212` 各自执行不同过滤；`backend/internal/service/account_header_override.go:172-195` 的 `ApplyHeaderOverrides` 另有账号级规则；WS 构造在 `backend/internal/service/openai_ws_forwarder_payload.go:78-197` | V2 不得展示敏感 header；测试记录只能保留安全的 endpoint/模型/状态摘要，认证 header、token 和凭证必须丢弃或脱敏。 |
| HTTP continuation owner | `backend/internal/handler/openai_gateway_handler.go:514-530` 校验 `previous_response_id`；`backend/internal/service/openai_gateway_response_handling.go:1401-1449` 以 `userID/apiKeyID` 绑定和校验 HTTP owner | 这是可复用的隔离先例，但 V2 仍须在 conversation/turn 层保存并校验管理员 owner，不能仅依赖上游 response ID。 |
| WS response/account 映射 | `backend/internal/service/openai_ws_state_store.go:62-73` 注释并声明 `response_id -> account_id`；`:198-220` 写入；`:238-269` 读取；`:641-644` 的本地 key 只有 `groupID:responseID` | 该映射没有 user/API-key owner 维度。V2 多轮不能直接复用它来决定跨管理员或跨 API key 的上下文归属；必须新增 owner 隔离并在每轮服务端 recheck。WS 会话 header 中的隔离也不能替代持久 owner 校验。 |

## 按固定接口的差距审计

| 契约 | 基线状态 | 必须补齐的 QA 证明 |
| --- | --- | --- |
| `GET /api/v1/admin/channel-test/catalog?group_id=N&account_id=N` | 阻断：后端路由/处理器不存在 | 返回 envelope；只列分组所属账号；指定账号仍须服务端验证；账号和模型字段完整；`scheduling_note` 可追溯到真实 scheduler；模型声明/下游开放/未知/映射/最近实测分离。 |
| `POST /conversations` | 阻断：无 V2 conversation API/表 | 保存 group、可选 account、model、title、owner 和时间；切换组/模型/指定账号新建会话。 |
| `GET /conversations`、`GET /conversations/:id` | 阻断 | 永久保存、owner 隔离、分页、检索；列表有 `items`/`total`；详情有 conversation 和完整 turn；刷新只读恢复，不重复发请求。 |
| `POST /conversations/:id/turns/stream` | 阻断 | 必须是带认证的 fetch SSE；校验 prompt 和 `client_request_id`；服务端建立 turn，按实际 attempt 发事件并持久化状态。 |
| SSE 事件 | 阻断 | 每条 `data:` 是 JSON；类型仅为固定枚举；`conversation_id`/`turn_id`/`seq`/`at`/`attempt`/`turn` 字段和生命周期可校验；终态最后且携带完整持久 turn。 |
| 取消与幂等 | 阻断 | 首选 AbortController；对端中止要持久 `cancelled`；同一 `client_request_id` 不得重复创建 turn/扣流量/重试链；断线不可假成功，重连或刷新只恢复已存终态。 |
| 旧 debug-test | 保留 | 旧分组/账号接口仍可运行；其兼容回归不能替代 V2 验收。 |

## 风险与后续实现验收重点

### API、持久化与一致性

1. 为 conversation、turn、attempt 建立专用持久模型和迁移。历史不能只放 Redis、浏览器 localStorage 或 WS TTL cache；不自动过期，分页查询要有 owner/group/time 索引。
2. turn 写入应先形成 `running` 状态，再将每次 attempt 和最终状态原子收口。`turn_completed`/`turn_failed`/`turn_cancelled` 必须携带与数据库一致的完整 turn；客户端断开、上游截断和服务重启不得假成功。
3. `client_request_id` 必须有服务端唯一约束或等价幂等记录，并在并发重复请求、刷新重放、网络重试下只产生一条轮次。
4. 上下文只能由服务器从该 owner、该 conversation 已完成的成功轮次构造；失败/中断的半截回答不能自动进入下一轮。切换组、模型或指定账号要创建新 conversation；同组自动故障转移只改变 attempt，不丢成功上下文，也不跨账号复用上游 `response_id`。

### 安全与隔离

1. 所有 catalog、conversation CRUD 和 stream 都要经过既有管理端鉴权/审计；每个查询、详情、写入和流式续接都要按管理员 owner 校验，不能只按 group ID。
2. `account_id` 非空时必须查询 account-group 关系并验证账号状态、平台、模型、调度资格；校验失败应拒绝或返回明确不可用原因，不得静默切换到其他账号。
3. simple mode 的平台全池行为和当前 WS `groupID + responseID` 映射不能成为 V2 的隔离依据。需要针对两个 owner、两个 API key、同 group/跨 group、同 response ID、自动 failover 设计回归用例。
4. 记录和 UI 只显示安全的账号名、ID、平台、模型、端点和状态摘要；不得显示 Authorization、API key、OAuth token、凭证原文或敏感 header 值。
5. 透传/自定义 endpoint 应沿用现有安全校验和 allowlist；测试结果必须注明实际 endpoint 和实际账号，不能从 upstream catalog 或 header allowlist 生成“支持”结论。

### 调度与展示

1. 调用现有 scheduler 的完整输入（previous response、guardian、session sticky、group、model、capability、transport、排队和并发约束），记录实际 selected account、candidate count、Top-K、rank 和拒绝原因。
2. 展示 priority、load、queue、error rate、TTFT、reset、quota headroom、upstream cost 等“可获得时”的真实因子和解释；不能将 score 直接翻译成承诺概率。
3. attempt_started 必须发生在调用上游之前，并包含实际账号、模型、endpoint 和时间；attempt_failed 后才能进入下一账号。无候选账号时不得伪造 attempt 或成功。
4. acquire 的槽位必须在成功、失败、取消和客户端断开路径释放；WaitPlan 仅表达真实调度器的等待策略。V2 不得引入与既有调度器冲突的新算法。
5. priority/load factor 编辑复用既有账号更新 API，只发送改变字段；保存中、失败可恢复和成功重排要可见，服务端验证、鉴权和审计要有证据。

## 当前阻断项清单

- **B1（P0）Availability V2 后端入口缺失**：没有 `/api/v1/admin/channel-test` 及其 catalog/conversation/stream handlers。
- **B2（P0）永久历史与幂等缺失**：没有 V2 conversation/turn/attempt 存储、owner 约束和 `client_request_id` 幂等记录。
- **B3（P0）实时 attempt 事件和断线终态缺失**：旧 debug-test 的单次结果不能满足事件顺序、立即 failover、取消和恢复契约。
- **B4（P0）指定账号归属风险**：simple mode 现有路径可按平台取全池，且 sticky recheck 在 simple mode 只检查非空账号。
- **B5（P0）多轮 WS owner 维度不足**：既有 response/account 映射没有 user/API-key 维度；V2 直接复用会有跨 owner 串链风险。
- **B6（P1）模型和透传口径风险**：既有 mapping/support 和多套 header allowlist 不能替代分组+实际账号+实际模型的实测结果。
- **B7（P1）集成验收未运行**：本 QA 工作树只提供离线契约门禁；待后端/前端整合后，必须在非生产测试环境提供脱敏 fixture 并运行严格门禁。

## 推荐整合后的门禁顺序

1. 检查工作树只包含允许目录，运行 `git diff --check`。
2. 运行默认离线契约校验，确认契约/静态既有证据通过，同时看到 `V2 NOT IMPLEMENTED` 或实际实现状态，不把它当集成成功。
3. 后端和前端整合后，生成不含密钥和敏感 header 的脱敏 SSE/JSON fixture，运行 `python3 tests/availability-v2/verify_contract.py --fixture <fixture> --require-v2`。
4. 在隔离测试环境执行 owner、分组归属、指定账号不切换、两次幂等、取消/断线恢复、自动 failover、模型映射和 header redaction 回归。
5. 最后才允许由发布负责人决定是否进行部署；本 QA 工作树不执行生产访问、部署、push 或生产构建。
