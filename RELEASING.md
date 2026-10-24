# 发布与签名维护手册

[返回项目首页](README.md) · [部署与运维指南](OPERATIONS.md)

本文面向仓库维护者，说明如何发布带签名更新清单的版本。面板的“软更新”只会安装能用内置公钥验证签名的版本；如果没有配置签名，发布流程会在推送镜像之前失败，不会产生缺少签名的公开版本。

## 发布产物与信任链

推送 `vX.Y.Z` 标签后，`ci-and-release` 工作流按以下顺序执行：

1. `release-assets`（运行在受保护的 `release` 环境中）：构建两个架构的压缩包，生成 `update-manifest.json`，用私钥签名得到 `update-manifest.json.sig`，再用源码内置的公钥复验签名、压缩包和其中每个文件的哈希，最后执行 amd64 面板程序的 `version --json`，确认版本、commit 和兼容声明一致。
2. `image`：只有上一步成功后才构建并推送 GHCR 镜像（含 `latest`）。
3. `release`：上传上一步已经签名并复验过的产物，创建 GitHub Release。

任何一步失败，后续步骤都不会执行。面板的验证链为：内置公钥 → 清单签名 → 清单记录的压缩包哈希与逐文件哈希。签名消息带域前缀 `go-panel-update-manifest-v1\n`，格式见 `internal/selfupdate/manifest`。

## 一次性准备

### 1. 生成发布密钥

在可信的本机执行，不要在 CI 或共享服务器上生成：

```bash
go run ./tools/releasetool keygen --out ~/go-panel-release.key
```

命令把私钥 seed 写入 `~/go-panel-release.key`（权限 0600，不会覆盖已有文件），只在终端打印公钥、keyId 和一行 Go 代码。私钥 seed 是 32 字节的 base64 编码，任何能读取它的人都能签发面板会信任的版本。

### 2. 创建受保护环境并保存私钥

在 GitHub 仓库的 **Settings → Environments** 中创建 `release` 环境，并设置：

- **Deployment branches and tags**：只允许匹配 `v*.*.*` 的标签。
- **Required reviewers**：至少一名维护者审批。审批时确认标签对应的 commit 正确。

然后把私钥保存为该环境的 secret：

```bash
gh secret set GO_PANEL_RELEASE_SIGNING_KEY --env release < ~/go-panel-release.key
```

保存后，把本地明文转入离线介质（如加密 U 盘或密码管理器），然后删除 `~/go-panel-release.key`。

### 3. 内置公钥

把 `keygen` 打印的那行代码追加到 `internal/selfupdate/trust/keys.go` 的 `releaseKeys` 中，通过 Pull Request 合并。审查时核对 keyId 与生成时打印的是否一致。这个文件是软更新的信任根，任何修改都必须经过审查。

在公钥合并之前，CI 的 `verify` 步骤会以“面板未内置任何发布公钥”失败，这是预期行为。

## 每次发布

### 发布前检查

- [ ] **数据库结构纪元**：本次修改过 GORM 模型或迁移逻辑时，`schema_fingerprint_test.go` 会失败。请按 `internal/model/schema_epoch.go` 的规则判断：兼容变更只提升 `SchemaEpoch`；破坏性变更还要令 `MinReaderEpoch = SchemaEpoch`。破坏性变更会使旧版本无法读取迁移后的数据库，回滚需要恢复快照，必须在 Release Notes 中写明。
- [ ] **RuntimeABI**：修改了 Dockerfile 运行阶段（基础镜像、系统包、目录布局、入口命令），且新程序依赖这些修改时，递增 `internal/buildinfo.RuntimeABI`。递增后旧镜像无法软更新到此版本，界面会提示更新镜像，Release Notes 需写明。
- [ ] **MinUpgradeFrom**：只有要求用户先经过某个中间版本时，才提高 `internal/buildinfo.MinUpgradeFrom`。
- [ ] **supervisor 修复**：修改了 `internal/selfupdate/supervisor` 或 `protocol` 时，在 Release Notes 中写明“需要更新镜像才能生效”。supervisor 只随镜像更新。
- [ ] 本地执行 `bash scripts/verify.sh` 通过。
- [ ] 修改了软更新、supervisor 或镜像入口时，本地执行容器端到端测试（见下文）。

### 打标签

只使用 `vX.Y.Z` 形式的稳定版本标签。`v*.*.*` 标签会触发正式发布，并移动 `latest` 镜像与 GitHub 的 Latest Release。`v1.2.3-rc.1` 这类预发布标签会在生成清单时失败。

```bash
git tag -a v0.2.0 -m "go-panel v0.2.0"
git push origin v0.2.0
```

工作流进入 `release-assets` 时等待审批，审批通过后自动完成签名、推送镜像和发布。

### 人工核验已发布的版本

```bash
mkdir -p /tmp/go-panel-verify && cd /tmp/go-panel-verify
gh release download v0.2.0 -R nXiaoK/go-panel
cd -  # 回到仓库目录
go run ./tools/releasetool verify --manifest /tmp/go-panel-verify/update-manifest.json \
  --sig /tmp/go-panel-verify/update-manifest.json.sig --release-dir /tmp/go-panel-verify
```

## 本地容器端到端测试

`scripts/e2e-soft-update.sh` 在本机 Docker 中用真实镜像、supervisor 与管理 API 验证六个场景：

1. 软更新到新版本、通过试运行后正式启用，节点程序随之切换；
2. 重建容器后保留软更新版本，`PANEL_USE_BUILTIN` 应急开关生效；
3. 启动即失败的版本自动回滚并被隔离；
4. 永不就绪的版本在就绪超时后回滚；
5. 在面板中回滚到上一版本；
6. 改用更新的镜像后，以镜像为准并清理软更新版本。

```bash
docker build -t go-panel:soft-update-test .            # 可选：复用已构建的镜像
E2E_BASE_IMAGE=go-panel:soft-update-test bash scripts/e2e-soft-update.sh
```

测试版本用 `selfupdate_e2e` 构建标签编译：信任一把只存在于临时目录的测试公钥，并从容器网络内的假 Release 服务器下载；脚本结束时清理全部容器、数据卷与镜像。正式构建不得使用该标签，CI 会检查构建脚本。

## 在 fork 中演练

**不要向主仓库推送测试标签。** 演练步骤：

1. Fork 仓库，在 fork 中按上文创建 `release` 环境，并用**单独生成的测试密钥**设置 secret。
2. 在 fork 的分支上把测试公钥加入 `releaseKeys`（这个改动不要合并回主仓库）。
3. 在 fork 中推送标签（例如 `v9.0.0`），确认 Release 中有 `update-manifest.json` 与 `.sig`，并且镜像推送到了 fork 的 GHCR。`build-release.sh` 会把 `GITHUB_REPOSITORY` 写入清单，所以测试面板需要把 `UPDATE_REPOSITORY` 设为 fork 的仓库名。
4. 用 `scripts/e2e-soft-update.sh` 在本机容器中验证完整的软更新链路。

## 密钥轮换

1. 生成新密钥，在 `release` 环境中设置 `GO_PANEL_RELEASE_SIGNING_KEY_NEXT`，并把新公钥追加到 `releaseKeys`（保留旧公钥）。
2. 此后的发布会同时使用两把私钥签名，旧镜像和新镜像都能验证。
3. 至少经过一个发布周期，确认用户已普遍更新到内置新公钥的镜像后，把新私钥移到 `GO_PANEL_RELEASE_SIGNING_KEY`，删除 `_NEXT`，并从 `releaseKeys` 中删除旧公钥。

旧镜像只认识旧公钥。如果过早删除旧私钥，这些镜像就无法软更新，只能手动执行 `docker compose pull`。

## 私钥泄露应急

1. 立即从 `release` 环境中删除泄露的 secret，暂停发布。
2. 生成新密钥。在 `releaseKeys` 中**删除**泄露的公钥、加入新公钥，并尽快用新私钥发布一个修复版本。
3. 在公告中提醒用户手动执行 `docker compose pull && docker compose up -d`：旧镜像仍信任泄露的公钥，只有更新镜像才能撤销这份信任。
4. 检查泄露期间是否存在非预期的 Release 或资产替换。
