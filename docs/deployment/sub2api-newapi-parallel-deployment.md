# Sub2API 与 New API 并行部署方案

## 目标

在现有服务器上部署 Sub2API，同时保持当前 New API 业务、数据库、Caddy 入口和容器不受影响。验证完成后，再把 New API 作为下游接入 Sub2API；接入过程可单独关闭和回滚。

## 已确认的线上基线

- 服务器：4 vCPU、3.8 GiB 内存、无 Swap，磁盘剩余约 26 GiB。
- New API 项目目录：`/srv/new-api/cn2-20260921`。
- New API 容器使用宿主机回环端口 `14001`、`14002`、`14003`。
- Caddy 占用公网 `80/443`，当前 New API 健康检查返回 HTTP 200。
- New API 使用独立 MySQL 和 Docker 网络；目前没有 Sub2API、PostgreSQL、Redis 容器。

## 总体架构

```text
公网
  |
现有 Caddy :80/:443
  |-- 现有域名/路径 ------> New API master/slave/canary
  |
  `-- 独立 Sub2API 域名 --> Sub2API :8080
                              |-- PostgreSQL（仅内部网络）
                              `-- Redis（仅内部网络）

New API --(OpenAI 兼容 HTTPS 上游渠道)--> Sub2API /v1
```

关键隔离规则：

1. 不修改现有 New API 的 `compose.json`、MySQL、数据目录和业务容器。
2. 不让 Sub2API 连接 New API 的 MySQL；Sub2API 只使用自己的 PostgreSQL 和 Redis。
3. 不让 Sub2API 自带 Caddy；公网入口继续由现有 Caddy 统一处理。
4. Sub2API 应用只加入自己的内部网络；需要反代时，再单独加入现有 Caddy 所在的 `new-api-cn2_egress` 网络。数据库和 Redis 不加入该网络。

## 阶段 0：部署前冻结和备份

**目的：** 建立回滚点，避免把新服务变更和 New API 变更混在一起。

- 记录 `docker ps`、`docker stats`、`df -h`、New API 三个本地健康接口和公网 `/healthz`。
- 确认最近一次 New API MySQL 备份可读，至少保留一个离线副本。
- 确认 DNS 计划使用独立域名，例如 `sub2api.example.com`，不要复用现有 New API 域名路径。
- 预留目录：`/srv/sub2api/prod`、`/srv/sub2api/backups`。

验收条件：现有 New API 三个实例和公网入口仍为 200；没有执行任何 `down`、删卷或重启 New API 的操作。

## 阶段 1：准备独立 Compose 项目

使用官方 `deploy/docker-compose.yml` 作为基础，但生产环境复制为自己的文件并固定镜像版本或 digest，不直接跟随 `latest` 自动更新。

建议项目名：`sub2api-prod`。

建议调整：

```yaml
services:
  sub2api:
    container_name: sub2api-prod-app
    # 首次验证阶段不发布宿主机端口，通过容器网络和 healthcheck 检查
    # 后续由现有 Caddy 通过 Docker 网络访问 sub2api-prod-app:8080
    networks:
      - sub2api-network
      - new-api-cn2_egress
    mem_limit: 768m
    cpus: "1.50"

  postgres:
    container_name: sub2api-prod-postgres
    mem_limit: 512m
    cpus: "1.00"

  redis:
    container_name: sub2api-prod-redis
    mem_limit: 256m
    cpus: "0.50"

networks:
  sub2api-network:
    driver: bridge
  new-api-cn2_egress:
    external: true
```

如果首阶段必须从宿主机测试，可临时使用 `127.0.0.1:14004:8080`；禁止使用默认的 `0.0.0.0:8080:8080`。

`.env` 至少设置：

- 独立的 PostgreSQL 用户、数据库和随机密码。
- 独立的 Redis 密码。
- 固定的 `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY`，避免容器重启导致会话或 2FA 失效。
- 固定 `TZ=Asia/Shanghai`。
- `POSTGRES_MAX_CONNECTIONS=50`、`POSTGRES_SHARED_BUFFERS=64MB`。
- `DATABASE_MAX_OPEN_CONNS=20`、`DATABASE_MAX_IDLE_CONNS=5`、`REDIS_POOL_SIZE=64`。

`.env` 权限设为 `600`，不要复用 New API 的数据库密码或密钥。

验收条件：

- `docker compose -p sub2api-prod config` 成功。
- 解析后的端口没有 `80`、`443`、`14001`、`14002`、`14003` 或 MySQL 宿主机端口。
- Compose 项目只声明自己的卷和网络。

## 阶段 2：分层启动和内部验证

按以下顺序启动，避免一次性启动造成问题难以定位：

1. 只拉取镜像，不停止现有容器。
2. 启动 PostgreSQL，等待 `pg_isready` 健康。
3. 启动 Redis，等待 `redis-cli ping` 健康。
4. 启动 Sub2API 应用，等待 `/health` 返回 200。
5. 检查 `docker stats`、宿主机可用内存、磁盘增长和应用日志。

重点验证：

- Sub2API 能完成数据库迁移并创建初始管理员。
- PostgreSQL 和 Redis 只在 `sub2api-network` 内监听，没有宿主机端口暴露。
- New API 的三个容器、公网 Caddy、MySQL 健康状态和响应时间与阶段 0 基线一致。
- 服务器没有明显 OOM、磁盘快速增长或 CPU 长时间满载。

## 阶段 3：接入现有 Caddy

为 Sub2API 配置独立域名和 DNS 记录，例如 `sub2api.example.com`。在现有 Caddy 配置中新增独立站点，反代到 `sub2api-prod-app:8080`。

变更顺序：

1. 先备份现有 Caddyfile。
2. 执行 `caddy validate`，验证完整配置。
3. 仅 reload Caddy，不重启 New API 容器。
4. 验证新域名 HTTPS、登录页、`/health` 和 `/v1/models`。
5. 再次验证原 New API 域名首页、`/healthz`、`/v1/models` 和一个只读 API 请求。

验收条件：新增域名可用，原域名和原路由无 4xx/5xx 回归，Caddy reload 不导致现有容器重建。

## 阶段 4：让 New API 接入 Sub2API

这里按“New API 作为下游，调用 Sub2API 的 OpenAI 兼容入口”设计：

1. 在 Sub2API 创建专用 API Key，只授予测试分组和必要模型。
2. 在 New API 后台新增一个独立的 OpenAI 兼容渠道：
   - Base URL：`https://sub2api.example.com/v1`
   - API Key：Sub2API 专用 Key
   - 模型映射：先只配置一个测试模型
   - 独立分组：例如 `sub2api-canary`
3. 不修改现有生产分组的渠道权重，不替换现有 New API 直连渠道。
4. 使用一个测试 Key 完成模型列表、非流式、流式、鉴权失败和超时场景验证。
5. 观察至少一个完整业务高峰或 30-60 分钟，再决定是否逐步增加流量。

如果实际意图是“Sub2API 调用 New API”，则方向相反：在 Sub2API 中把 New API 的 HTTPS `/v1` 地址配置成上游，并使用独立的 New API 只读/专用 Key；不要让两套系统共享数据库。

## 阶段 5：备份、监控和日常运维

- PostgreSQL 每日 `pg_dump` 到 `/srv/sub2api/backups`，保留至少 7 份，并定期做一次临时库恢复。
- Redis 已启用 AOF，但不把 Redis 当作唯一业务数据源。
- 记录 Sub2API 应用、PostgreSQL、Redis 的容器健康和资源使用。
- 对磁盘、内存和 OOM 事件设置告警；这台机器无 Swap，内存告警阈值建议设在可用内存低于 800 MiB。
- 镜像升级采用“拉取新镜像、校验、停止 Sub2API 应用、启动新版本、健康检查”的单独窗口，不执行 `docker compose down -v`。

## 回滚方案

### Sub2API 本身异常

只停止 Sub2API 项目：

```bash
docker compose -p sub2api-prod stop sub2api
```

保留 PostgreSQL、Redis 和卷，便于继续排查。不要执行 `down -v`。

### New API 接入后异常

1. 在 New API 中禁用 `sub2api-canary` 渠道或将权重设为 0。
2. 保留 Sub2API 运行，确认问题是否来自上游协议或模型映射。
3. 必要时删除该渠道配置，但不触碰 New API 现有数据库和容器。

### Caddy 配置异常

恢复 Caddyfile 备份，重新执行配置校验后 reload。不要用重启整机或重建 New API Compose 项目来处理 Caddy 配置问题。

## 上线判定

满足以下条件才进入正式流量：

- Sub2API 独立健康运行 24 小时。
- New API 原有入口连续验证正常，没有新增 5xx 或明显延迟。
- Sub2API PostgreSQL 备份和恢复演练通过。
- New API 到 Sub2API 的测试渠道通过鉴权、模型、流式和失败回退验证。
- 已确认可以通过禁用渠道在 1 分钟内停止新流量。

## 当前建议

先执行阶段 0-2，暂不改 New API 和现有 Caddy 业务路由。Sub2API 内部健康、资源和数据持久化验证通过后，再提供一个独立域名并接入 Caddy；最后只新增 New API 测试渠道，采用灰度方式接入。
