# 节点程序产物

正式 Docker 镜像和 GitHub Release 会在 GitHub Actions 中自动构建下列文件，并把它们封装进发布产物：

- `gost-amd64`
- `gost-arm64`
- `nft_agent_amd64`
- `nft_agent_arm64`
- `nft_rule_payload_amd64`
- `nft_rule_payload_arm64`
- `nft_flow_reporter_amd64`
- `nft_flow_reporter_arm64`

节点通过面板的 `/api/v1/node/assets/<filename>` 下载对应程序。生产部署不需要在服务器创建或挂载本目录，也不需要 Go 工具链。

只有本地开发、测试节点安装流程时才需要手动执行：

```bash
./scripts/build-node-assets.sh
```

生成的二进制会保留在本目录但被 Git 忽略，不能提交到源码仓库。
