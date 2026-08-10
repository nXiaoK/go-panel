# Third-party notices

go-panel 包含或引用多个第三方开源组件。各组件仍由其原作者持有版权，并受各自许可证约束。

## go-gost / go-gost x

- 上游项目：<https://github.com/go-gost/gost>、<https://github.com/go-gost/x>
- 用途：节点代理运行时、面板连接与升级逻辑。
- 状态：本仓库的 `go-gost/` 包含为 go-panel 节点连接、升级和上报所做的修改。
- 许可证：MIT
- 上游版权：Copyright (c) 2016 ginuerzh

完整许可证保存在 [`go-gost/LICENSE`](go-gost/LICENSE)。修改内容不代表上游项目对 go-panel 提供支持或背书。

## Watchtower

- 上游项目：<https://github.com/containrrr/watchtower>
- 用途：仅在用户显式启用 `compose.update.yml` 时，作为独立容器拉取并替换 go-panel 镜像。
- 分发方式：go-panel 不复制 Watchtower 源码或二进制；Docker 按用户指令从其独立镜像仓库拉取。
- 许可证：Apache License 2.0（另含其上游声明的 BSD 组件）

## Go 与 npm 依赖

其余直接和间接依赖记录在 `go.mod`、`go.sum`、`vite-frontend/package.json` 与 `vite-frontend/package-lock.json`。GitHub 发布镜像同时生成 SBOM，具体版本和许可证请以相应发布产物及上游仓库为准。
