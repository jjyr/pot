# Backend crates / 独立 Server 验证

日期：2026-10-05。对应 Issue [#5](https://github.com/jjyr/pot/issues/5)；设计见 [Backend crate 拆分](../backend-crates.md)。先提交设计，再实现；验证在独立 worktree、临时数据目录和固定 mise 工具链执行。

## 结果

| 验证 | 结果 |
|---|---|
| Workspace check | 全部 8 个 crates 通过；Server 后端依赖不包含 App/GPUI/Swift/Helper |
| lint | Rust/Swift/Python/Shell 格式与脚本检查通过 |
| 后端常规回归 | Core、Storage、Driver、Gateway、Runtime、Server、Protocol 通过；包含 checksum、原数据库兼容、请求/额度、任务/计划与取消 |
| App | 11 项测试通过，缓存、事件、分页、指导确认和发送禁用态保持兼容 |
| 真实 Pi + 本地模拟 provider | 8 项通过：工具/原生续聊、取消、额度恢复/压缩、多任务、自动编排、计划、provider 迁移和 native codecs |
| 独立 Server | 3 项通过：bearer、mode-0600 endpoint、设置持久化/重启、旧 token 拒绝、SSE 关闭；监听冲突不初始化存储；关闭时活跃主会话和 worker 均确认进程退出 |
| Driver 反馈边界 | 外来 request、旧 epoch、已结束 turn 被拒绝，不污染当前 binding |
| Codex 原生 App Server | 独立 Server + 官方 CLI + 本地模拟模型通过续聊、迁移、MCP/只读汇总、独立 worker/计划、实际 Runtime 时钟、取消及后台进程收敛；35 个模拟请求，0 付费调用 |
| AGY | 模拟传输通过原生身份、重启、取消后续聊、command recovery、诊断脱敏、用量、worker/计划/汇总；0 模型调用 |
| Claude | 独立 Driver + fixture 子进程通过身份/用量/消息事件、恢复和取消；不依赖 Runtime 或数据库。原版 CLI 契约尝试在能力检查处拒绝缺失必需控制的 executable，无模型请求，未计为原版通过 |
| Linux ARM64 | `pot-server` 的 `aarch64-unknown-linux-musl` zigbuild 通过 |

命令入口由 [开发指南](../development.md) 与 [Runtime 契约](../../runtime/README.md) 维护。Server integration 与 Driver fixture 在 `mise run test` 中执行。机器特定环境记录放 `~/Workspace/Machines`。

## 范围与局限

保持当前 SQLite 布局、DELETE + synchronous=EXTRA、旧 metadata、原生 session 和请求额度。Control API v1、MCP/PTY 路径与 endpoint 文件契约保持兼容。工具 schema 通过 Core crate API 共享，避免跨 crate source include。

Runtime 保留现有 Axum handler 参数与错误类型，Server 集中组装路由和生命周期；本次没有同时完成所有 handler 的传输无关化。Gateway 网络请求在 Runtime 持久预留及 turn/lane 校验后发生。Driver 反馈是编译期职责边界，不是新的 guest 安全域。

未重建完整 OCI 镜像/App bundle，未启动或修改已有 App/VM 数据，未做 guest 冷安装、GUI/真实账户矩阵或远程部署产品验收。跨机器端点发现、TLS、身份配置、产物传输和桌面强隔离未新增。
