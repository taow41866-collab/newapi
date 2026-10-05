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
- 功能提交 `5b63edd970c6eabe67725774f00c9b5678629899` 的 CI `37281120945` 与镜像构建 `37281806214` 成功；最终审查又发现并修复通用 Option API 可覆盖历史规则的问题，必须以新增修复提交重新验收、构建，不发布旧候选镜像。
- 2026-10-05 最终审查：新增回归先复现通用 `PUT /api/option/` 将历史规则清空（200），修复后返回 409 且历史不变；追加事务改为先幂等建立规则行，再使用统一 `lockForUpdate` 加锁。
- 两个独立进程并发首次追加通过：SQLite 3.50.4、MySQL 8.0.46、PostgreSQL 16.15；两条规则均保留且生效时间递增。WSL 的 Go 工具链下载超时，采用 Windows 交叉编译 Linux 测试二进制，临时账号和数据库清理完成。
- 发布前定向 Go 测试、`go vet ./model ./controller`、路由权限测试通过；3 个前端测试文件 9 项通过，`bun run typecheck` 通过。最终提交 `fe6b323a4b32022b40fd4a41a1275275a11d9741` 的 Linux 完整 CI 已通过，见下方发布证据。
- 发布脚本复用现有 Canvas 文件，仅更新镜像内 New API 前端与后端；隔离恢复库验证 Root 追加、管理员拒绝写入、历史不可覆盖和无效规则拒绝，不写生产采购价、不调用付费上游。
- 修复提交 `901ac68201a52a57e2b1d6d9b2d4931041b2ff99` 的首次 CI `37287838213` 前端全绿，后端在既有 `TestSecurityAccountDeletionConcurrentRequestsHaveOneWinner` 遇到 SQLite 锁冲突；Linux 单独复核该测试通过。没有绕过门禁。发布前另修正隔离验收脚本导入路径为 `/tmp/deploy-channel-cost.py`；最终提交重新通过 CI 和镜像构建。

## 正式发布证据

- 分支：`codex/sd25-video-release`；实际发布源码提交：`fe6b323a4b32022b40fd4a41a1275275a11d9741`。后续发布记录提交不改变线上版本，不重发镜像。
- CI：[37289529950](https://github.com/taow41866-collab/newapi/actions/runs/37289529950)，镜像构建：[37289535444](https://github.com/taow41866-collab/newapi/actions/runs/37289535444)。2026-10-05 09:53 UTC 再次查询两者均 completed/success，head_sha 与发布提交一致。
- 不可变镜像：`ghcr.io/taow41866-collab/new-api@sha256:1377de554212e69be2611d64690893d980ceecf27515bb8102f84eb60cff2e7d`，OCI revision 与上述完整提交一致。
- 切换时间：2026-10-05 09:36:22 UTC；发布目录：`/srv/new-api/releases/revenue-video-fe6b323a4b32`。
- master：`new-api-revenue-video-fe6b323a-master`，ID `e7112c1cef994e0dcd6f8701f12245bc84f7c917c83c80922d49cb6119253ba4`。
- slave：`new-api-revenue-video-fe6b323a-slave`，ID `657c90e7925b325f61b73484ab9bd91c3bf39fec8cd2dc9748f9e42d59dcb062`。
- 备份：发布目录内 `database-preswitch.sql.gz`，SHA256 `7fb032eede5d7fbbf1284fc79c6e686679d6a6792664f2d2404b96966f2644bf`；可读检查与恢复验证通过，恢复库 `modelroute_verify_fe6b323a4b32` 有 40 张表，两次启动 schema/账本不变。
- 恢复库隔离验收：Root 追加通过，管理员采购规则访问 403，通用 Option 历史覆盖 409，负倍率 400。未写生产采购规则，未请求付费上游。
- Caddy/watchdog 已切换；邮件认证实例未重启，模型调度仍为 `shadow`。Canvas 文件复用，不覆盖现有视频静态应用。
- 线上浏览器 `/dashboard/models` 已看到四个新增指标；当时成本未覆盖 2,904 条，毛利/毛利率为未知，符合缺成本不能报零的约定。刷新解决发布前旧页面的 ChunkLoadError，发布后检查未见新增控制台错误。

## 发布后只读巡检与清理

- 2026-10-05 09:50:29 UTC 检查：公共健康、鉴权、前端及 Canvas 静态资源内容校验通过；两个新实例均运行，重启次数为 0，旧回滚实例停止但保留。
- 从切换时间起每实例最多读取 2,000 行日志，仅输出错误计数；实际 master 427 行、slave 284 行，匹配数据库连接/锁/死锁错误均为 0。该有界检查不是完整流量错误率或延迟测量。
- 采购规则仍为 0 条，近一小时任务状态汇总为空；没有真实付费请求，不能把这些检查当作视频生成或盈利金额的端到端验收。首小时观察尚未完成，不宣称已完成一小时巡检。
- 持久回滚脚本保存为发布目录内 `deploy.py`，权限 0600；SHA256 `3247fb62e2e2394ea18dbe5f2bea93684800366c905380c278eed71f470d12e8`，与本地及原 `/tmp/deploy-channel-cost.py` 一致。
- 重新核对全部容器引用后，无 force 删除三个未引用候选镜像：`bbf41257e51b7082da1d84c8b7d0d580de07d6b778051edc07fb37d71f8f26a2`、`ed7b02c9ab80f4beab3d6e4b1a750a9ab188580a93d4655711731e5dcc616b4c`、`956bb22bc355e963bd9a6d8a0648fceebd7b6f2c89531ffeafc5fa785a670952`。未删除 GHCR 制品，需要时可按 digest 重拉。
- 当前运行与当前回滚镜像保留，其余历史镜像因停止容器仍引用而保留；未删除任何容器、其他服务或账本。
- 已合并分支中当前发布分支、默认 `color-fix-staging`、audit/baseline 均有保留用途；其他工作树分支也保留。没有确认无用且满足删除条件的分支，因此未删除分支。

## 回滚入口

仅在明确触发回滚条件时执行以下命令，不用它做只读检查：

```sh
ssh hk-newapi
python3 /srv/new-api/releases/revenue-video-fe6b323a4b32/deploy.py rollback \
  ghcr.io/taow41866-collab/new-api@sha256:1377de554212e69be2611d64690893d980ceecf27515bb8102f84eb60cff2e7d \
  fe6b323a4b32022b40fd4a41a1275275a11d9741
```

脚本恢复之前的 `new-api-revenue-video-90707e3f-master/slave` 及原 Caddy/watchdog；旧镜像为 `sha256:9d77c62efc2d18ee4ce2e1c4cd83ce6887b0642d07efda64f6754c3164dc5ea6`。保留新日志和消费账本，不以旧数据库快照覆盖新交易。只读资源复核使用相同命令参数并将 action 改为 `verify`。

## 发布门禁

1. 只提交本功能及本记录，不把工作区其他未提交改动带入。
2. 对发布提交运行完整 CI；镜像 OCI revision 必须等于该提交。
3. 只读核对生产容器、挂载、配置、静态资源路径和当前镜像；先做并验证数据库备份，保留可回滚容器和镜像。
4. 在隔离/灰度环境验证 Root 写成本、普通管理员无权读写采购规则、管理员读数据栏、无成本时毛利未知，以及原用户计费不变。
5. 切换后验证健康、错误率、数据库和静态资源；失败时恢复应用版本但不回滚消费账本。
6. 发布成功后保留实际运行版和当前回滚版，仅清理无容器引用且已确认不再需要的候选镜像；确认分支已合并、非保护且无在用后再删除远端无用分支。不得强删停止容器引用的历史镜像或含未合并工作的分支。
