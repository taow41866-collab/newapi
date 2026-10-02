# 模型级调度验收记录

2026-10-02。工作目录 `F:/Projects/newapi`，分支 `codex/newapi-rc41-local`，基线 `49ee6e623e07ff749f453323d349462e807f9c27` 加未提交差异。**形成本地发布候选：本轮约定的本地放行测试通过，未提交、未推送、未构建发布镜像、未部署；不是已上线声明。** 原订阅计划、线上配置及用户数据均不在本阶段修改范围。生产shadow/灰度仍须另行满足发布清单。

## 源码边界

- `pkg/modelroute/`：共享评分、模型隔离、健康状态、租约、2/1/1探针预算；生产使用真实Redis，MemoryStore只用于测试。
- `model/model_routing.go`、候选查询入口：只在已有授权集合评分，显式主备分组仍可遵循静态优先级。
- `service/model_routing.go`、Relay和scanner调用点：每次转发独立观测，首有效文本/工具增量计时，取消/本地业务拒绝不归因上游。
- `controller/model_routing.go`、系统任务入口：15分钟探针，管理员只读诊断，Synthetic不写真实速度样本。
- 不修改原持久化渠道权重；健康系数与最终分配概率是不同概念。无schema/依赖升级。

## 已取得的运行证据

| 范围 | 用例与结果 | 限制 |
|---|---|---|
| 两败两胜、模型隔离、迟到/重复回报、过期 | `pkg/modelroute` 通过；5个固定样本精确断言评分和份额 | 单元测试不是HTTP验收替代 |
| 探针预算 | 2/1/1、最久未探测、去重、空位补齐通过 | 跨实例执行互斥复用系统任务锁 |
| 六种协议形态 | `TestModelRoutingHTTPProtocolsAndAccounting`：chat/responses/messages × 流式/非流式，真实鉴权→分发→本地上游，通过 | 只使用本地模拟服务 |
| 非零计费 | 每次$0.002，有限Token；上游到达前已预扣、成功精确1000quota、503最终退款 | 固定价格，不宣称所有表达式计费组合已E2E覆盖 |
| 实际随机分发 | chat非流式120请求，预定±15百分点；一次结果fast89/slow31，逐次预测合计95.89，通过 | 不是每次必须最快；其余协议按确定性评分区间验收 |
| 两个独立网关+Redis | 两败/两胜跨进程共享；并发Inflight=2、完成归零；故障回退一次转发、恢复后新样本，通过 | 任务runner未包含在网关fixture内 |
| Redis进程/租约 | 真实Redis子进程退出、重复Finish、租约时间推进过期，通过 | 旧代际覆盖另由确定性核心测试验证 |
| 数据库 | SQLite3.50.4、MySQL8.0.46、PostgreSQL16；受影响候选查询/探针权重矩阵通过 | 无迁移变更，不等于全应用三库所有功能矩阵 |
| 诊断权限 | ChannelRead路由校验与未鉴权HTTP401通过 | 无新增前端面板 |

## 本阶段发现和修复

1. 删除恢复50%的中间阶段，两次有效成功直接恢复健康系数1。
2. Synthetic探针不污染真实延迟/TPS/错误率评分，先红后绿。
3. 本地429拒绝不触发上游冷却，先红后绿。
4. HTTP200 SSE `response.failed/rate_limit_exceeded`此前live两次降级、probe未冷却；真实HTTP测试复现后，将live/probe统一为`service.ModelRoutingOutcome`。修复后live与probe通过，非零预扣/退款精确断言通过。
5. 独立审查曾误判普通HTTP429被包装500；完整追踪NewOpenAIError后已撤回，该构造器保留原错误状态。只记录SSE案例为实际缺陷。
6. 每渠道探针登记原先满64项后永久拒绝新组合；新增满额测试先失败，再改为保留近期登记并淘汰最旧项，测试通过。
7. 发布复核发现授权策略重建原来先删除后重建且无事务，失败可能留下空策略；现于同一事务内完成角色与策略更新，并用三库测试覆盖重复启动、旧受限策略、第二连接可见性和写入失败回滚。
8. MySQL 大小写不敏感及 PAD SPACE 排序规则使 SQL `= ''` / `= 'all'` 与授权加载器的精确比较不一致。隔离 `utf8mb4_unicode_ci` 库先复现受限策略被误删、管理员权限意外放开，以及大写策略类型漏补基线；现只把 SQL 当候选筛选，按 Go 精确语义筛选后删除/标记覆盖。SQLite、MySQL 8.0.46（PAD SPACE）、PostgreSQL 16.15 新库均通过。

## 同版本完整回归

最新源码的 `verify-local.ps1` 已成功退出0：完整Linux后端与relaykit测试、Linux构建、三库定向复验通过。无测试包的包正常列出，env-gated真实Redis/进程/性能入口在普通套件中跳过，另由专用脚本执行，不能计入普通套件的实际覆盖。首次运行到三库阶段时，WSL虽返回0但发出非致命NAT提示，PowerShell的Stop策略提前终止；脚本现按WSL实际退出码判断，三库单独运行及完整重跑均通过。

脚本前后源码指纹一致；最终完整Go源码manifest位于 `build/model-routing/source-manifest.json`，文件SHA256为 `883CA65DD9E5C67CF3C26A74EE9DF5BC4954FC9B5ED1D6F748FD7B97CAFF55FA`。Linux/relaykit/数据库日志分别为同目录 `linux-regression.log`、`relaykit-regression.log`、`database-matrix.log`。无提交产生，本基线加manifest比仅记HEAD更准确。CI工作流及发布脚本不在该Go指纹内；二者SHA256分别为 `22B93B24E7775E3009E5F01C45111A28558C32074E260E04BE610699983BBE49` 和 `1EC4BD14417D2DDD115A31C2623E6ED6D39F6F9646B6B89E2555816F8FEF7892`。

同源码真实Redis独立进程测试通过（0.09s）、双网关测试通过（3.21s），包含共享健康、并发在途及非零计费暂停回退/恢复。独占性能通过（56.88s），日志为 `build/model-routing/real-redis-final.log`；专用Redis由测试脚本退出清理。发布脚本17项单元测试通过。`git diff --check`、Go格式及改动范围凭据模式扫描通过。

## 最终非零计费性能（冻结版本，独立运行）

每模式预热10请求、三轮各100请求、固定30ms本地上游、单并发；测试worker使用$0.002价格、有限Token和实际SQLite预扣/结算，不使用零价格替代。执行时没有并行全量测试。阈值保持附加p95≤10ms及吞吐下降≤5%，两项均PASS。

| 模式 | p50/ms | p95/ms | p99/ms | req/s | 相对off |
|---|---:|---:|---:|---:|---|
| off | 58.073 | 60.746 | 66.328 | 17.242 | 基线 |
| shadow | 61.029 | 65.208 | 73.611 | 16.458 | p95 +4.462ms；吞吐 -4.55% |
| active | 59.576 | 64.674 | 71.795 | 16.732 | p95 +3.928ms；吞吐 -2.96% |

原始9轮和Redis commandstats：`build/model-routing/performance.json`；测试日志：`build/model-routing/real-redis-final.log`。本轮Redis `GET` / `EXEC` 各增加1200次、`MGET` 增加600次；这是整轮测量前后增量，未单独归因到每个模式。shadow吞吐余量仅0.45个百分点；本地结果满足约定门槛，但不替代生产同负载shadow和容量测试，不能解释为线上性能改善。

## 性能证据（最终冻结前测量）

同一WSL、真实Redis7.0.15、固定30ms模拟上游，三个独立进程，单并发；每模式预热10次，三轮各100次，顺序轮换。阈值始终为附加p95≤10ms、吞吐下降≤5%。

| 模式 | p50/ms | p95/ms | p99/ms | req/s | 对off |
|---|---:|---:|---:|---:|---|
| off | 42.973 | 47.903 | 51.025 | 23.094 | 基线 |
| shadow | 45.125 | 48.111 | 51.614 | 22.115 | p95+0.208ms；吞吐-4.24% |
| active | 44.870 | 48.065 | 51.228 | 22.191 | p95+0.162ms；吞吐-3.91% |

以上为早期零价fixture记录，仅保留追溯；当前`performance.json`已由最终非零计费结果替代。此表不能冒充最终版本结果。

非零计费fixture的一次测量与全量回归重叠，实际FAIL：off p95=87.744ms/RPS14.188、shadow252.358ms/9.082、active241.262ms/9.749。原始报告保留`http-performance-agent.json`；未放宽阈值。该次存在争用，不能当隔离性能放行依据，也不能假定失败全因争用，必须独占复测。

## 独立复核与证据边界

独立复核覆盖共享归因修复、Redis非零账务、流式失败，以及本轮授权策略/发布脚本新增差异，未发现新的确定阻断；定向service/modelroute/诊断权限测试通过。完整冻结结果以上述主线程执行记录为准。CI工作流尚未实际运行，不能以本地结果冒充CI通过。

- metadata-only超时和无用量SSE失败精确退款；503重试成功和Redis回退精确收费一次。
- 有输出的截断/取消检查钱包与Token一致、费用不超过每请求一次，但没有断言所有provider的精确应收费金额；不改变原计费规则。
- 诊断实际验证匿名401和ChannelRead声明，未补已登录缺权限403端到端；已有权限中间件保持不变。
- 跨实例任务锁的claim/续期/过期构件测试通过、调用链完成审查；未执行两个真实runner中途失锁的完整E2E演练。
- 进程退出由真实Redis独立worker验证，网关HTTP由两个独立网关验证；不称已完成生产副本退出演练。
- 单并发局部性能与双请求并发正确性不是高并发容量/长时间稳定性测试。

## 可复现路径

- `verify-local.ps1`：Windows交叉编译后在WSL执行完整根模块、relaykit、构建和受影响数据库矩阵，记录源码SHA256 manifest，拒绝运行期间源码变化。
- `test-real-redis.sh`：仅启动其拥有的隔离Redis16391，无AOF/快照，退出清理自己的PID；执行真实进程测试。Redis二进制来自Ubuntu官方包，仅解压到忽略的build目录，不注册服务。
- 实际Redis故障注入必须设置`MODEL_ROUTE_ALLOW_REDIS_PAUSE=1`且指向专用回环测试服务器，不得指向共享或生产Redis。
- 不启动Docker Desktop。日志/二进制位于忽略的`build/model-routing/`；测试源码与脚本留在Git工作区。

## 配置示例（仅供下一阶段，不在本阶段应用）

```dotenv
MODEL_ROUTING_MODE=shadow
MODEL_ROUTING_GROUPS=待核实的允许分组
MODEL_ROUTING_MODELS=待核实的精确模型ID
MODEL_ROUTING_PRIORITY_GROUPS=需要保留主备关系的分组
```

多个值用英文逗号分隔；不要把显示名称猜成模型ID，不建议初始全量`*`。所有承载请求的副本使用相同配置和共享Redis；启动参数变更需要受控发布。`off`保留旧选路，`shadow`观测但不改实际选路，`active`才启用白名单评分分发。只读诊断：`GET /api/channel/model-routing?group=...&model=...&endpoint=/v1/responses&stream=true`，沿用管理员与channel.read鉴权。

## 放行检查清单

- [x] 最终冻结版本完整Linux/relaykit/构建、三库定向测试、真实Redis及性能通过。
- [x] 超时、断流、取消、重试及Redis故障期间非零计费完成约定验收（精确金额范围见证据边界）。
- [x] 独立复核覆盖最后修复与新增测试，未发现权限/扣费/模型隔离/恢复确定阻断。
- [x] 收齐工作区源码差异、manifest和测试报告；未覆盖范围明确记录。manifest与运行后源码一致。
- [x] 生产只读核对模型、渠道、路由、副本与配置；专用内网Redis已配置，现有应用仍未连接。新旧master短暂重叠的已知无锁任务前提：无Codex渠道、无自动余额更新、无批处理；发布脚本会重复检查。
- [x] 发布脚本最终独立复核：本地17项守卫测试通过；stage/rollback异常路径已修，尚未在生产执行。Caddyfile若在写入途中中断而成为未知半写内容，回滚会拒绝覆盖并需按私密备份人工核对，不能称无条件自动回滚。
- [ ] Git→CI→不可变镜像，校验revision/digest；数据库备份与恢复验证，保留当前线上镜像/容器。
- [ ] shadow获得足够真实样本后小范围active；错误扣费、越权、认证异常或数据完整性问题立即停止。
- [ ] 回滚应用/路由不覆盖新账本，不使用旧数据库快照抹去消费记录。
