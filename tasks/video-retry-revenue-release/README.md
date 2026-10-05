# 视频重试与收入统计发布记录

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

## 本次发布证据

- New API 提交：`ecd287cf959348f8b47fc11788f9bf075932d766`
- CI：`https://github.com/taow41866-collab/newapi/actions/runs/37246797790`
- 镜像：`ghcr.io/taow41866-collab/new-api@sha256:7557417128adfeca4852bb121e5fb97878d58c720d99ae8f44b452c8705c5e66`
- 镜像 OCI revision：`ecd287cf959348f8b47fc11788f9bf075932d766`，架构 `amd64`
- 视频前端提交：`750fdc8498d333c4eb4f356add263680bf2a9207`
- 视频源码分支：`taow41866-collab/newapi:codex/canvas-video-retry-release`（静态应用独立于后端镜像）
- 视频静态包 SHA256：`95406ba36c6aa3d64c42605a63c53b521be238e458a84ed633eae46a3993a0e4`
- 线上后端 revision：`ecd287cf959348f8b47fc11788f9bf075932d766`
- 线上前端 header：`newapi-revenue-video-ecd287cf9593`
- 线上视频静态 header：`canvas-video-95406ba36c6a`
- 发布目录：`/srv/new-api/releases/revenue-video-ecd287cf9593`
- 旧容器：`new-api-modelroute-07b82aea-master`、`new-api-modelroute-07b82aea-slave`，已停止但保留；旧镜像为 `ghcr.io/taow41866-collab/new-api@sha256:13325a1d84b4ef285d1f71f5700dcfa91bf90d29e59ad3381c2d885d69c22f47`
- 新容器：`new-api-revenue-video-ecd287cf-master`、`new-api-revenue-video-ecd287cf-slave`
- 新备份：`database-preswitch.sql.gz`，SHA256 `87063d931e8353f7506d0f958b81d9c5321b7ae4733eba7aba6a03d6ebce13b8`，`gzip -t` 通过；恢复库 `modelroute_verify_ecd287cf9593`，40 张表，schema/账本两次启动未变
- Caddy 与 watchdog 已切换；邮件认证容器未重启，模型调度仍为 `shadow`

## 验证

后端命令：`go test ./model ./controller -run 'Test(CalculateRevenue|ValidateRevenue|GetRevenue)' -count=1`、`go vet ./model ./controller`。

前端命令：`bun run typecheck`、`bun test`（42 tests / 88 assertions，通过）、`bun run build`（通过）。线上浏览器打开 `/canvas/video`，DOM 渲染正常，控制台 error/warning 为 0。

生产隔离验收：

- rehearsal 健康、`/docs/`、匿名鉴权和 `/v1/models` 通过。
- 恢复库上管理员收入读取通过；Root 采购价读写通过；普通管理员读写均返回 403；非法负价格返回 400 且原规则不变。
- 未发送真实付费视频生成请求；公网视频页只做页面和资源验证。
- 公共 `/api/status`、`/docs/`、`/dashboard`、`/canvas/video` 及页面引用的 JS/CSS 均返回 200；静态资源内容与候选版本一致。

## 发布前检查

1. 完整后端 CI、前端测试和生产构建通过。
2. 只提交本次视频重试、收入统计及对应文档，保留工作区其他用户改动。
3. CI 成功后生成不可变镜像并记录 digest、revision 和 Actions URL。
4. 线上只读核对容器和配置，备份数据库并验证可读；保留当前容器和旧镜像。
5. 先更新隔离/灰度实例，验证视频查询重试、收入接口权限、账本和静态资源，再切换正式流量。

## 回滚

在服务器执行：

```sh
python3 /srv/new-api/releases/revenue-video-ecd287cf9593/deploy.py rollback \
  ghcr.io/taow41866-collab/new-api@sha256:7557417128adfeca4852bb121e5fb97878d58c720d99ae8f44b452c8705c5e66 \
  ecd287cf959348f8b47fc11788f9bf075932d766
```

脚本只恢复应用容器、Caddy 和 watchdog，保留新日志，不覆盖消费账本；旧容器和旧镜像均未删除。

## 未覆盖项

- 三数据库完整矩阵和一小时持续观测未在本次发布中完成；CI 的 MySQL/PostgreSQL 相关门禁通过，但本机 Windows 全仓测试不作为 Linux 生产证据。
- 采购价格必须由 Root 配置；缺规则的行只能显示销售额，毛利显示为不完整。
- 发布后首小时已设置本聊天的 15 分钟只读巡检，结束后暂停；设置巡检不等于一小时观测已经完成。
