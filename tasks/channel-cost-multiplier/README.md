# 渠道模型成本倍率估算

## 依据与边界

- 参考 Sub2API `production` 分支提交 `fdd376481856182ee3142fd013c4593ea85640d7`（v2.9.5）的账号成本倍率与收入/基础成本分离做法。
- New API 没有与 Sub2API `account_stats_cost` 等价的通用上游实际成本记录；不能把模型售价或用户扣费冒充供应商账单。
- 因此本功能仅提供 Root 可配置的渠道+精确模型倍率估算，精确采购报价规则继续作为优先来源；估算结果必须显式计数并保持缺数据未知。
- 不改变用户计费、不读取或修改生产、不自动探测供应商账单、不新增数据库迁移。

## 源码定位与公式

- Sub2API `backend/internal/service/account_cost.go:17`：`Account.CostMultiplier` 从账号 `extra.cost_multiplier` 读取估算倍率，未配置时默认为 0.1；该默认值不适合直接移植到 New API，因为这里没有相同的账号实际成本基数。
- Sub2API `backend/internal/repository/priority_scheduling.go:13`：`ReadPrioritySchedulingSignals` 按账号/请求模型聚合实际收入与 `account_stats_cost`/`total_cost` 基础成本。`backend/internal/service/priority_scheduling.go:319`：`scorePriorityCandidate` 计算基础成本乘倍率、利润与毛利率。
- New API `service/log_info_generate.go:100`：`GenerateTextOtherInfo` 在请求时写入 `model_ratio`、`completion_ratio` 等价格快照；`model/revenue.go:131`：`CalculateRevenue` 汇总报表，`:246` 的 `findPurchasePriceRule` 处理精确价优先，`:267` 的 `purchaseCost` 计算采购成本。
- 倍率估算公式：`((prompt_tokens * model_ratio) + (completion_tokens * model_ratio * completion_ratio)) / quota_per_usd * cost_multiplier`。这是平台模型基准价估算，不是渠道实际采购账单。
- Root 在收入页填渠道 ID、模型、倍率并追加草稿；`0.75` 表示基准成本的 75%。保存后规则 `unit` 为 `model_multiplier`、`unit_price` 为倍率、`source` 为 `manual estimate`，`effective_at` 为添加时间的 Unix 秒数。已有精确规则优先。
- 规则等价示例（收入页的快捷输入会生成，不需要手动填时间）：

```json
{
  "channel_id": 32,
  "model": "example-model",
  "unit": "model_multiplier",
  "unit_price": 0.75,
  "effective_at": 0,
  "source": "manual estimate"
}
```

- 适用边界：必须有正的 prompt token、请求时模型倍率和输出倍率快照，且不能是缓存/分层/固定价/音频/图片/异步任务记录；不满足时仍计为成本未覆盖。

## 实施与验收

- [x] 为标准 token 记录增加 `model_multiplier` 采购成本规则，基于日志快照模型倍率、输出倍率和 Token 数计算平台基准价，再乘成本倍率。
- [x] 缺失倍率/用量、缓存、分层计费、固定价、音频、图片和异步任务维持未知，不将成本当作零。
- [x] 报表增加估算成本条数和毛利率；管理 UI 提供渠道/模型/倍率快捷录入并明确估算属性，保留精确采购价配置。
- [x] 针对精确规则优先、估算公式及拒绝无依据推算编写回归测试。
- [x] 对新增差异做权限、公式、缺失数据与受影响回归审查；确认收入规则读写仍由既有 Root 路由保护，不新增数据库访问路径。
- [x] 验证：`go test ./controller -run 'Test(CalculateRevenue|ValidateRevenue|GetRevenue)' -count=1`、`go vet ./model ./controller`、收入页 7 项 Vitest、`bun run typecheck`、`bun run build` 均通过。
- [ ] 全量 `go test ./model ./controller -count=1` 未全绿：model 包通过；controller 包中出现既有审计/SQLite 数据库矩阵及 billing expression 测试失败（如 `TestAuditDatabaseMatrix`、`TestPreConsumePolicyDatabaseMatrix`、`TestAPITokenAuditDatabaseMatrix`、`TestUpdateOptionAliasBillingExprUsesPluginSchema`）。收入定向测试仍通过；尚未对这些未改动模块做根因修复。

## 当前状态

- Sub2API 源码定向追踪：`F:\Projects\sub2api`，commit `fdd376481856182ee3142fd013c4593ea85640d7`；实际读过成本倍率解析/校验、使用信号 SQL 聚合、利润估算及权重评分分支。没有运行 Sub2API 测试或验证其线上数据。
- New API 工作树包含用户已有的其他未提交修改；本任务仅更改本 README 之外明确列出的收入模块文件，不整理或还原其他差异。
