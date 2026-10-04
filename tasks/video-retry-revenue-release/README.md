# 视频重试与收入统计发布说明

## 本次范围

- 视频任务重试先查询原任务并同步状态；仅在明确失败且用户确认后创建新的收费任务。
- 待处理、未知状态、查询失败、结果保存失败均不会重复提交；重试沿用原任务参数快照。
- 收入报表读取消费与退款账本，按渠道和模型匹配采购价格，计算净销售额、已知成本和估算毛利。
- 异步任务未明确成功、缺少采购价或用量时标记为未覆盖，不把成本按零计算。

## 接口与配置

- `GET /api/revenue?start_timestamp=&end_timestamp=`：管理员查看，时间范围最多 31 天。
- `GET /api/revenue/prices`：Root 读取采购价规则。
- `PUT /api/revenue/prices`：Root 写入采购价规则；规则存入现有 `Option` 的 `RevenuePurchasePrices`，不新增迁移。
- 支持单位：`tokens`（输入/输出每百万 token）、`request`、`image`、`second`。
- 价格按 `channel_id + 精确 model + effective_at` 匹配，缺失价格不虚构利润。

## 验证

后端命令：`go test ./model ./controller -run 'Test(CalculateRevenue|ValidateRevenue|GetRevenue)' -count=1`、`go vet ./model ./controller`。

前端命令：`bun run typecheck`、定向 `oxlint`。视频前端聚焦测试为 24 tests / 56 assertions，通过。

## 发布前检查

1. 完整后端测试、前端测试和生产构建通过。
2. 只提交本次视频重试、收入统计及对应文档，保留工作区其他用户改动。
3. CI 成功后生成不可变镜像并记录 digest、revision 和 Actions URL。
4. 线上只读核对容器和配置，备份数据库并验证可读；保留当前容器和旧镜像。
5. 先更新隔离/灰度实例，验证视频查询重试、收入接口权限、账本和静态资源，再切换正式流量。

## 回滚

停止新实例，恢复发布前记录的旧镜像 digest 和原运行参数；保留新实例日志，不覆盖消费账本或用数据库快照回滚交易记录。

## 未覆盖项

- 尚未完成三数据库矩阵、完整全仓测试和线上部署验收时，不得宣称已发布。
- 采购价格必须由 Root 配置；缺规则的行只能显示销售额，毛利显示为不完整。
