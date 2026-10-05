# 渠道上游成本与数据栏毛利

## 目标与边界

Root 在渠道编辑页维护上游成本规则；管理员在模型数据统计栏直接查看所选时段的净销售额、已知成本、估算毛利和毛利率。收入详情页仅做只读报表。

- 复用 `RevenuePurchasePrices` Option 存储，不新增数据库迁移。
- 成本只用于报表，不影响用户扣费、请求路由或订阅结算。
- 精确单价使用 USD，按输入/输出及可选缓存读/写 Token（每百万 Token）、请求、图片或秒填写；精确单价优先于模型倍率估算。
- `*` 是渠道默认倍率；精确模型倍率覆盖默认倍率。倍率 `0.75` 表示 New API 已保存模型基准价的 75%，不是供应商账单。
- 新规则从服务端当前时间起生效；新增历史只能追加，不能修改或删除已保存规则。过去请求仍按其发生时间匹配当时有效规则。
- 缺少成本或用量时显示未知，不把成本按零计算。统计最多 31 天；更长区间明确提示调整时间范围。

## 实现

- `model/revenue.go`：`ValidatePurchasePrices` 校验规则；`findPurchasePriceRule` 选择精确模型采购价、精确模型倍率、渠道默认倍率；`AppendPurchasePriceRule` 事务追加单条规则并按模型递增生效时间；`AppendPurchasePriceRules` 为旧全量 PUT 客户端提供只追加兼容，拒绝改写/删除已有规则。
- `controller/revenue.go` 与 `router/api-router.go`：Root-only 的 `GET /api/revenue/prices`、只追加兼容的 `PUT /api/revenue/prices`、Root-only 原子追加 `POST /api/revenue/prices/rules`；管理员只读 `GET /api/revenue`。
- `web/src/features/channels/components/drawers/channel-cost-configuration.tsx`：仅 Root 在已有渠道编辑页维护成本；规则列表按模型和计价类型显示当前生效值。
- `web/src/features/dashboard/revenue.tsx`：只读收入报表。
- `web/src/features/dashboard/components/models/log-stat-cards.tsx`：管理员打开模型数据页即可看到四项盈利指标；超 31 天、接口失败或成本不全均不会伪报毛利。

## 公式和限制

标准 Token 倍率估算为：`((prompt_tokens * model_ratio) + (completion_tokens * model_ratio * completion_ratio)) / quota_per_usd * cost_multiplier`。精确 Token 采购价按每百万 Token 配置，并可单独填写缓存读/写价；发生缓存用量却缺少对应单价时，该记录仍为未覆盖。倍率估算不处理缓存、分层/固定价、多媒体或异步任务。缺少有效用量/快照时，毛利显示未知。估值不等于渠道真实结算单。

模型数据栏的盈利汇总使用当前时间范围，并沿用该栏的用户名筛选；非管理员不请求、不展示这些指标。成本规则仅 Root 可维护。

## 验证记录

- RED/GREEN：新增追加规则测试起初因 `AppendPurchasePriceRule` 尚不存在而编译失败，添加事务追加实现后通过。
- 后端定向：`go test ./controller -run 'Test(AppendPurchasePrice|CalculateRevenue|ValidateRevenue|GetRevenue)' -count=1` 通过；`go vet ./model ./controller` 通过。
- 后端全量：`go test ./model ./controller -count=1` 中 model 通过；controller 在 `TestAPITokenAuditDatabaseMatrix/sqlite` 清理临时数据库时遇到 Windows 文件仍被占用。该失败属于未修改的访问令牌审计数据库测试；本地全量未全绿，不能作为本次功能全量通过的证据。
- 前端定向：渠道成本、模型数据栏、只读收入页 3 个测试文件共 7 项通过。
- 前端全量：180 个测试文件、2,197 项断言通过；渠道成本表单、模型数据栏和收入页定向测试 9 项通过。
- `bun run typecheck`、`bun run build` 和本次 UI 文件定向 Oxlint 通过。全仓 `bun run lint` 仍有多处既有无关错误，本次改动文件无 lint error。
- Go 收入/成本定向测试与 `go vet ./model ./controller` 通过；`go test ./model` 全量通过。Windows 下 `go test ./controller` 全量仍被无关的 `TestAPITokenAuditDatabaseMatrix` SQLite 临时库句柄清理失败阻断，Linux CI 是最终全量门禁。
- 尚未推送、尚无本次功能 CI/镜像/生产发布证据。必须等待所发布提交的 CI 与不可变镜像验证后才能开始线上切换。

## 发布门禁

1. 只提交本功能及本记录，不把工作区其他未提交改动带入。
2. 对发布提交运行完整 CI；镜像 OCI revision 必须等于该提交。
3. 只读核对生产容器、挂载、配置、静态资源路径和当前镜像；先做并验证数据库备份，保留可回滚容器和镜像。
4. 在隔离/灰度环境验证 Root 写成本、普通管理员无权读写采购规则、管理员读数据栏、无成本时毛利未知，以及原用户计费不变。
5. 切换后验证健康、错误率、数据库和静态资源；失败时恢复应用版本但不回滚消费账本。
6. 发布成功后按每个生产服务只保留一个实际运行版本镜像清理其余镜像；确认分支已合并、非保护且无在用后再删除远端无用分支。不得删除仍在用的回滚镜像或含未合并工作分支。
