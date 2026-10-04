# SD2.5 视频插件修复发布流程

本文记录提交 `a887a173`（本次发布分支提交为 `07b82aea83766a03742f3269687f8b60d9b7dce0`）的安全发布流程，供后续 Codex 复用。修复内容位于 `plugins/tasks/sora/plugin.js` 和 `plugins/sora_responses_test.go`：将 `SD2.5特价900-线路四` 绑定到 Sora 的 `openai_video` 协议，并识别 `images`、首帧和尾帧输入。

## 发布原则

- 不从带有未提交实验改动的工作区直接构建。
- 以线上正在运行的不可变镜像 revision 为基线，只 cherry-pick 目标修复。
- CI 成功后才构建镜像；生产只使用完整 digest，不使用 `latest` 或可变 tag。
- 先备份数据库并恢复到临时库验证，再启动候选副本；候选通过后才切换 Caddy。
- 旧容器、旧镜像、数据库备份和部署记录必须保留，回滚应用不覆盖消费账本。

## 可复用步骤

以下命令使用 Windows PowerShell、本机 Git 凭据和现有 SSH 别名 `hk-newapi`。不要在命令或日志中输出 Token、密码、私钥、数据库正文或 Redis 密码。

### 1. 创建隔离发布 worktree

```powershell
git -C F:\Projects\newapi worktree add -b codex/sd25-video-release-v2 `
  F:\Projects\newapi-sd25-video-release-v2 <线上当前 revision>
git -C F:\Projects\newapi-sd25-video-release-v2 cherry-pick a887a173
```

先检查：

```powershell
git -C F:\Projects\newapi-sd25-video-release-v2 diff HEAD^ HEAD --name-only
go test ./plugins -run 'Sora|sora|SD2' -count=1
```

### 2. 推送并等待 CI

```powershell
git -C F:\Projects\newapi-sd25-video-release-v2 push -u origin codex/sd25-video-release-v2
python tasks/model-routing/github-release.py dispatch --workflow ci.yml --ref codex/sd25-video-release-v2
python tasks/model-routing/github-release.py status --workflow ci.yml --ref codex/sd25-video-release-v2
```

只有对应提交 SHA 的 CI `conclusion=success` 才能继续。

### 3. 构建和核对不可变镜像

```powershell
python tasks/model-routing/github-release.py dispatch --workflow color-fix-image.yml --ref codex/sd25-video-release-v2
python tasks/model-routing/github-release.py status --workflow color-fix-image.yml --ref codex/sd25-video-release-v2
```

生产主机拉取 `color-fix-<commit-sha>` 后，记录并核对：

```bash
docker image inspect ghcr.io/taow41866-collab/new-api:color-fix-<commit-sha> \
  --format '{{.Architecture}}|{{json .Config.Labels}}|{{json .RepoDigests}}'
```

必须满足 `Architecture=amd64`、`org.opencontainers.image.revision=<commit-sha>`，并将 `RepoDigests` 中的完整值作为唯一部署镜像。

### 4. 生产只读核对和灰度

目标主机源码目录是 `/opt/newapi-v1`，但线上应用由 GHCR 镜像和 `/srv/new-api` 的灰度脚本管理。先核对实际容器、挂载、网络、Caddy 路由和当前镜像；不要假设源码目录就是运行目录。

本项目的守卫脚本：

```bash
python3 /tmp/deploy-shadow.py stage \
  ghcr.io/taow41866-collab/new-api@sha256:<new-digest> \
  <new-commit-sha> \
  <current-master-container> <current-slave-container>
python3 /tmp/deploy-shadow.py switch \
  ghcr.io/taow41866-collab/new-api@sha256:<new-digest> \
  <new-commit-sha> \
  <current-master-container> <current-slave-container>
```

`stage` 必须完成数据库 gzip 备份、临时库恢复、两次启动、schema/账本不变、候选 `/api/status`、`/docs/`、未鉴权接口和专用 Redis 检查。线上副本需要同时包含 `new-api-cn2_database`、`new-api-cn2_egress`，以及可选的 `new-api-model-routing` 网络；未知网络应停止发布。

`switch` 会保存旧 Caddy 配置和 watchdog，先切换路由并检查公网 revision，再停止旧应用容器。切换后至少检查：

```bash
curl -i https://lpss.online/api/status
curl -I https://lpss.online/docs/
curl -i -X POST https://lpss.online/v1/videos \
  -H 'Content-Type: application/json' \
  -d '{"model":"SD2.5特价900-线路四","prompt":"test"}'
```

视频请求未认证时预期为 `401`；这证明路由和认证边界存在，不代表已调用上游。带有效用户/API Key 的真实视频请求应在低风险窗口单独验收。

## 本次发布记录

- 发布提交：`07b82aea83766a03742f3269687f8b60d9b7dce0`
- CI：运行 `37206740491`，成功
- 镜像构建：运行 `37207272704`，成功
- 线上镜像：`ghcr.io/taow41866-collab/new-api@sha256:13325a1d84b4ef285d1f71f5700dcfa91bf90d29e59ad3381c2d885d69c22f47`
- 线上 revision：`07b82aea83766a03742f3269687f8b60d9b7dce0`
- 生产候选：`new-api-modelroute-07b82aea-master`、`new-api-modelroute-07b82aea-slave`
- 回滚容器：`new-api-modelroute-d8f473fb-master`、`new-api-modelroute-d8f473fb-slave`
- 备份记录：`/srv/new-api/releases/model-route-07b82aea8376/`
- 验收：新副本 running、restart count 0、`/api/status=200`、`/docs/=200`、视频未鉴权请求 `401`

## 回滚

发生健康检查失败、错误率升高、认证异常、视频任务异常或数据完整性问题时，停止新副本，恢复该 release 目录内的 `Caddyfile.before`、watchdog 和旧容器路由。保留新日志和账本，不恢复旧数据库快照覆盖新消费记录。
