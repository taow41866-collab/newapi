# New API 无限画布集成

## 发布范围

- 上游：`basketikun/infinite-canvas`，固定提交 `dab19adc0847e32e39b7fc8ff90cb392561fb826`，显示版本 `v0.19.0`。
- New API 基线：`a5a347f137d35601bdfcb97fc0747f0933f9966d`。不修改后端源码、镜像、渠道、计费或数据库结构。
- 部署日期：2026-10-03，香港服务器现有 Caddy 静态站点目录；不新增服务容器，不重启 New API。
- 直接入口：`https://lpss.online/canvas/canvas`；登录后通过侧栏「游乐场」旁的一级入口「无限画布」进入。当前链接为 `/chat/10`，前端按预设动态定位索引，不固定写死 10；聊天子菜单不再重复显示画布。

## 文件与契约

- `upstream.patch`：子路径路由、图标、插件、默认网关和 URL 片段凭据导入，以及 6 项回归测试。锁文件只替换下载源域名为官方 npm，版本及 integrity 不变。
- `deploy.py`：本次香港部署脚本，依赖现有 Docker 容器名称、MySQL 配置和站点目录，仅适用于此固定部署的首次接入；已存在画布路由时拒绝执行。
- `deploy-sidebar.py`：发布 New API 一级画布导航的静态前端，不替换后端镜像；保留旧嵌入前端的 `/static/*` 资源回退，避免已打开页面及文档的旧哈希资源失效。
- `LICENSE.upstream`：保留上游 MIT 许可；产物中也包含 `LICENSE`。New API 原有许可及归属信息不变。
- `verification.json`：实际验收结果及边界，不代表真实收费生成已验收。

New API 的 `Chats` 预设为 `https://lpss.online/canvas/canvas#baseUrl={address}&apiKey={key}`。沿用登录用户第一个启用令牌；无启用令牌时由既有 New API 界面提示创建令牌。片段不发送给服务器，导入后立即清除；沿用上游浏览器本地配置保存机制，不分发管理员 Key。共享电脑使用后应清理配置。画布数据保存在当前浏览器，不是 New API 账户云存储。

画布默认协议为 OpenAI 兼容，API 地址为 `https://lpss.online`。首次进入在渠道编辑中拉取模型，再按实际启用能力选择生图、文本或视频模型。配置了名称不代表模型可用，视频生成尤其需要对应上游支持。

## 干净副本复现

在 `F:\Projects` 下使用干净的上游副本，检出固定提交后执行：

```powershell
git -C F:\Projects\infinite-canvas apply --check F:\Projects\newapi\integrations\infinite-canvas\upstream.patch
git -C F:\Projects\infinite-canvas apply F:\Projects\newapi\integrations\infinite-canvas\upstream.patch
Set-Location F:\Projects\infinite-canvas\web
bun install --frozen-lockfile
bun test
$env:VITE_BASE='/canvas/'
$env:VITE_GATEWAY_URL='https://lpss.online'
bun run build
Copy-Item ..\LICENSE dist\LICENSE
```

本机首次依赖安装受到证书信任与镜像下载影响；使用导出的 Windows 已信任根证书配合 Bun `--cafile` 恢复，没有关闭 TLS 校验，也没有更新依赖版本。npm 备用安装因上游 peer 约束及陈旧 package-lock 失败，最终使用 Bun 固定锁文件安装和构建。

## 实际验证

- `bun test`：9 项通过，包括新增 6 项凭据解析测试和已有 3 项缩略图测试。
- `bun run build`：退出 0；保留上游静态 config.js、混合导入和大包提示。
- `bun run typecheck`：退出 1，未修改的 `web/src/components/layout/model-script-editor.tsx:58` 存在 Ant Design `styles.content` 类型错误。本次不顺手修改无关上游问题。
- 线上页面、图标、脚本、插件清单访问正常；浏览器无未捕获错误和失败资源响应。
- 新建画布、文本节点及刷新保留已通过；URL 片段导入后清除，请求 URL 不含测试令牌。
- New API `/chat/10` 内嵌流程在浏览器模拟登录及模拟个人令牌下通过；两个域名均可嵌入，未使用真实账户或收费调用。
- 两个 New API 副本均同步新预设，原 10 个预设保留；主页、`/api/status`、`/healthz` 返回 200，业务副本无重启。

仍未验收：真实用户的付费生图、生视频、音频及复杂插件调用；不宣称全仓学习或所有能力可用。

## 一级导航追加发布

2026-10-03 将画布提升为独立一级入口，复用既有侧栏链接、移动端关闭逻辑及 `/chat/$chatId` 的认证和个人令牌接线。通过已配置的画布 URL 路径识别预设，不依赖名称或固定数组索引；非法 URL 或缺少预设时不展示损坏链接，其他聊天预设保持原位置。新增七种界面语言文案。

实际结果：新增 7 项导航回归测试及既有 13 项侧栏测试共 20 项通过；New API `bun run typecheck`、改动文件 oxlint/oxfmt 检查、`bun run build` 均通过。模拟登录浏览器实测独立一级链接只出现一次、桌面与手机点击进入画布，且无失败资源和未捕获错误。模拟登录不代表真实收费生成验收。

新前端归档 SHA256：`f29ae12048c8bae4c15720cadeea4eaaf7dec81683de4736c58ad37eb2b43975`。发布目录为 `/srv/new-api/cn2-20260921/original-config/site/newapi-canvas-nav-f29ae12048c8`，配置备份为 `/srv/new-api/cn2-20260921/canvas-navigation/20261003-013524`。若需回滚导航，先检查期间是否有其他配置修改，再原位恢复该目录的 `Caddyfile.before` 并发送 SIGUSR1；此备份保留已上线的画布挂载和 Chats 预设，不回滚整个画布。

## 线上位置与回滚

- 静态目录：`/srv/new-api/cn2-20260921/original-config/site/canvas-newapi-dab19adc`。
- 配置备份和收据：`/srv/new-api/cn2-20260921/canvas-integration/20261003-005004`。
- 产物归档 SHA256：`9f3de4986fb457a09a6645ca72915bd11188111f3ab3ee3694b54a803e114026`。

回滚前比较当前 Caddy 配置和备份 `receipt.json` 的校验和，存在其他部署修改时先合并，不直接覆盖。恢复 `Caddyfile.before` 时原位写入已绑定的文件，验证后对 Caddy 发送 SIGUSR1；不要用重命名替换其挂载 inode。数据库只移除本次新增的「无限画布」预设，保留同期其他修改；等待既有 60 秒配置同步。保留静态目录作为备用，不删除用户或业务数据。

画布使用 CSP 限定允许嵌入的两个域名；其他 New API 页面继续保留 SAMEORIGIN。外部插件可以执行浏览器代码，应只启用可信插件，不把画布当作隔离的凭据保险箱。
