# 经营监测

后台每 5 分钟采集已有余额监控的只读快照。采样不调用模型，网页关闭后仍继续。
仅 `BUSINESS_MONITOR_OWNER_ID` 指定的管理员本人 JWT 会话可访问报表；普通用户、其他管理员和共享管理员 API Key 不可访问。

## 配置

- `BUSINESS_MONITOR_OWNER_ID`：所有者用户 ID；未配置时功能关闭。
- `BUSINESS_MONITOR_SNAPSHOT_URL`：固定的内部或 HTTPS 余额快照地址。生产使用现有 monitor `/api/snapshot`。
- `BUSINESS_MONITOR_ALIASES_JSON`：规范化 base_url 到钱包 base_url 的别名对象，例如不同 API 子域名共用钱包。没有别名时按规范化 base_url 分组。

币种依本站运营约定按人民币/美元记账单位 1:1 计算。余额和费用是运营监测口径，不代表真实货币结算审计。

## 口径

- 客户消费额：余额模式的本地 `actual_cost` 汇总；套餐用量另列。
- 预计成本：请求保存的 `COALESCE(account_stats_cost,total_cost) * account_rate_multiplier`。
- 余额消耗：期初余额 + 当日净入账 - 期末余额。
- 倍率变化记录“发现时间”，不假设它就是上游实际生效时间。首次读取建立基线。
- 入账基于正向余额变化推测，整数金额仅辅助判断。Viapi 小额增加标为签到候选。未确认入账必须保留推测状态。
- 人工修正的是整日净入账总额，不是在推测金额上追加；恢复自动后重新使用推测值。
- 容差 `max(0.2, min(预计成本 * 3%, max(客户消费 - 预计成本, 0) * 25%))`；倍率倒挂/疑似负毛利独立提示。
- 初次采样、采样缺口、缺失映射、失败或过期倍率均保留状态。失败余额不能写成零。
- 历史报表读按日汇总；从已有 usage_logs 按日小步补齐历史，无法补出从未保存的余额。

## 构建与更新

GitHub Actions `Build Business Monitor Image` 先运行财务计算/权限测试、PostgreSQL migration 校验和前端类型检查，再构建 `ghcr.io/meteor21c/sub2api:meteor-business-<sha>`。
生产服务器不得执行构建、编译或完整测试。更新顺序：

1. 确认当前容器、本机与公网健康，记录资源状态。
2. 备份 PostgreSQL、Compose，保留旧镜像并记录原镜像标签。
3. 旧服务运行期间拉取新镜像，核验 revision/digest。
4. 仅切换 `sub2api`，使用 `docker compose up -d --no-deps --force-recreate sub2api`。
5. 核验容器、本机/公网健康、首页、负载、内存、Swap、首次余额样本与权限拒绝。

回滚恢复原 Compose 和旧镜像，单独重建 `sub2api`。新增财务表不会更改现有计费表，回滚无需删除新表。
