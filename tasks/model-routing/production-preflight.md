# 生产只读核对与上线阻断

## 2026-10-02 后续状态（优先于下方历史阻断）

已按用户批准建立仅供模型调度的认证内网Redis，未打开全局Redis或改变原缓存；下方“没有共享Redis”是初次检查时的历史状态，不再是当前阻断。现有new-api应用尚未连接新Redis、未切流。

本次只读补查：生产数据库 `channels.type=57` 为0；master/slave 均无 `CHANNEL_UPDATE_FREQUENCY`，且 `BATCH_UPDATE_ENABLED=false`。发布脚本针对双master暂时重叠增加上述条件守卫，任何条件变化都停止stage或switch并清理候选；候选节点使用独立 `NODE_NAME`。这不代表其它生产条件已验收，也不等于已执行候选部署。

检查时间：2026-10-02 16:03 北京时间。通过既有 `ssh -o BatchMode=yes hk-newapi`，仅执行容器/镜像检查、状态GET和只读SQL；未推送、未修改环境变量/路由/渠道/账本、未重启容器。可重复检查脚本为production-preflight.py，输出白名单字段，不输出凭据或用户正文。

## 已确认

- 模型节点为`new-api-probe-49ee6e6-master/slave`，revision均`49ee6e623e07ff749f453323d349462e807f9c27`，running、重启数0、status=200/success=true。
- 当前镜像不可变引用为`ghcr.io/taow41866-collab/new-api@sha256:a999b6000f05a21f71c8bfc48cb26ad90ee00f90390dae0a25f432c14d0ba679`。这是当前线上回滚基线，不是本地新功能镜像。
- 两个email节点保持revision7ee6c3a55，running、重启数0、status健康；Caddy四条认证路由存在。
- Caddy SHA256：`dca29ba9211a18e7d1c548c956908e0500c63328c1fd633aa596916b9d5d429d`。
- 旧探针15分钟、并发2、失败/成功阈值2/2已配置；模型级新环境变量均未配置，新功能未上线。
- 渠道30仍在原福利池分组且禁用，本次未变动。
- pro-x20有渠道9/13/18；官方key有21/25。只有同分组同请求模型且有权限的候选才能互相比速，不跨组强行汇总。
- `gpt-6-luna`当前仅出现在渠道20的gpt超低倍率组；不能将它与另一模型`gpt-5.6-luna`混为一谈或自动改名比较。

## 发布阻断：没有共享Redis，直接打开全局开关会扩大行为变更

1. master/slave的`REDIS_CONN_STRING`均未配置，`MEMORY_CACHE_ENABLED=false`。
2. 本次容器清单没有Redis；宿主机6379无监听，redis/redis-server服务inactive。未发现已配置可复用的共享Redis；不因此断言用户没有其他外部Redis服务。
3. 新模型路由生产默认`RedisStore{}`依赖`common.RDB`；没有连接时回退原选路，不能宣称动态调度已生效。
4. `main.go`在`common.RedisEnabled`时强制`common.MemoryCacheEnabled=true`，即使现有环境写false。直接加全局`REDIS_CONN_STRING`会同时改变原有缓存路径，不属于仅启用模型评分存储的变更。启动时Redis Ping失败还会触发FatalLog；不能把本地仅调度Redis故障回退测试当作全局Redis故障兼容证明。

## 推荐方案，等待用户确认范围

- 新增仅内网、无公网端口、有资源上限及认证的模型调度专用Redis，不更换现有MySQL。
- 本地新增模型调度独立Redis连接配置，供两个模型节点共享；不打开全局Redis开关，不修改原有内存缓存和认证节点。
- 配置、初始化和故障回退变化先补测试，再更新冻结源码及相关验收。不能沿用旧manifest冒称新版本全部通过。
- 通过后再提交/推送Git、CI、不可变镜像；备份与恢复验证后先shadow，按精确模型/分组白名单小范围active。
- 默认保留官方key显式主备优先级；如要把该组也改为纯模型速度竞争，需要明确调整此前主备业务意图。pro-x20可以作为动态比较的初始候选组。

当前决策：HOLD在上线准备；本地已完成验收结论仍有效，但不足以直接部署到这个无Redis的生产环境。没有执行生产写入或新增服务。
