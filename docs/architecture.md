# 当前架构

更新：2026-10-05。本文描述已实现结构；未来产品约束见 [产品设计](product-design.md)。

```text
macOS
  Pot App（GPUI Kit）──── Swift bridge
     │                   ├─ tray / 窄侧栏 / 原生输入框
     │                   ├─ 配置与窗口生命周期
     │                   └─ WKWebView / noVNC + xterm ──────┐
     ├─ Unix IPC → Swift helper → Containerization │
     │                            Linux VM         │
     └─ bearer HTTP / SSE → Rust Runtime           │
                              ├─ SQLite / 请求账本 │
                              ├─ Agent binding → 主 Pi 会话         │
                              ├─ 任务队列 → Pi workers
                              └─ 定时调度           │
                                  桌面 / broker ←──┘
```

## Backend 拆分设计

Issue [#5](https://github.com/jjyr/pot/issues/5) 在独立 worktree 按 [Backend crate 拆分](backend-crates.md) 调整 Core/Storage/Driver/Gateway/Runtime/Server 边界。Server 独立启动后端；App 通过协议访问，不暴露远程部署产品功能。以下既有运行语义在重构中保持兼容。

## 责任与代码入口

| 层 | 责任 | 入口 |
|---|---|---|
| App | 聊天、任务、计划、设置；缓存与后台同步 | `app/src/main.rs`、`client.rs`、`ui.rs`、`schedules_ui.rs` |
| 原生桥 | 同一 NSApplication/窗口、输入法、tray、WKWebView、配置写入 | `app/swift/Bridge.swift`、`SettingsPanel.swift` |
| Helper | 用户进程，管理 VM、资源和正常/异常关闭 | `helper/` |
| Runtime | 权威任务状态、消息事件、API、用量、恢复 | `runtime/src/main.rs`、`store.rs` |
| 编排/调度 | 单层委派、队列、完成回流、定时触发 | `tasks.rs`、`orchestration.rs`、`schedules.rs` |
| Agent / 引擎 | Profile 快照、epoch 绑定与迁移；DriverHost / SessionHandle；Pi RPC、Codex App Server、Claude 原生 CLI 与各自上下文 | `agent/`、`pi.rs` |
| 网关 / 工具桥 | provider 路由、请求预留、协议 codec；Pi / MCP 共享工具定义和 dispatcher | `gateway.rs`、`pot-tools.json`、`tool_bridge.rs`、`orchestration.rs` |
| 协议/桌面 | 共享类型；guest 启动、浏览器与输入仲裁 | `protocol/`、`image/` |

GPUI 与 Swift 共用主窗口和事件循环，C ABI 负责桥接。普通页面复用 Kit；原生 NSTextView 负责聊天编辑，WKWebView 分别负责桌面和 bundled xterm 终端。终端通过 Swift URLSessionWebSocketTask 连接 Runtime PTY；bearer 仅保留在原生网络层，网页禁止联网和外部导航。关闭窗口保留后台；Quit 正常关闭 Chromium，再停止 helper/VM。

Sandbox 的系统依赖由构建 VM 预装成 OCI 镜像，随 App 发布；Helper 只导入本地镜像并启动，不在启动时安装包。用户数据独立挂载到 `/data`。桌面、字体和镜像大小见 [Sandbox 镜像](sandbox-image.md)。

## 主助手、任务和计划

主 Pi 根据目标选择直接回复或调用任务工具。Runtime 执行并发限制、幂等与状态转换；每个任务有独立目录和 Pi session。Worker 只获得明确任务说明，不复制主聊天历史，也不能通过编排 API 递归委派。

后台默认并发 5，同时执行用户配置的 profile/provider 上限；可运行任务按 FIFO 排队，受限 provider 不阻塞其他 provider。主聊天另占一个槽。同一任务串行，追加指导持久入队后在下一轮消费。`shared` 是显式协作锁，不是权限隔离。完成回报与状态一起保存；主会话空闲后合并最多 8 项报告进行汇总。

计划是持久定义，每次触发创建新任务。调度器负责时区、去重、错过策略及禁止重叠；模型负责检查目标和产出结构化结果。无变化时可静默，详见 [定时计划](scheduled-tasks.md)。

## 状态与恢复

- Runtime 保存产品消息/任务状态，各 CLI 保存原生执行上下文。`agent.bindings.v1` 为每个 owner 保存不可变 ResolvedSpec、SHA-256、epoch 和 native_ref；旧 Pi 元数据幂等导入 epoch 1，仍保留旧字段。恢复只使用绑定快照。
- Profile 设置通过持久迁移操作切换主聊天，运行中等待本轮停止；既有任务默认固定，新任务解析当前 worker 默认值。外部 handoff 与新会话先落盘，随后在同一 SQLite 事务归档旧绑定、激活新绑定并插入对话边界。确定性 handoff 有界、脱敏，下一次真实输入引导读取，不额外调用模型。
- Pi transport 事件转换为带 epoch/turn/seq 的驱动事件后投影到产品记录。取消仍须等待 Pi 退出，未确认停止禁止再启动该 owner；其他引擎不能仅凭实现 enum 声称支持。
- API 命令携带 request ID；消息事件有持久 cursor。Runtime 提供 SSE；当前 App 使用后台快照/分页轮询（约 500 ms），拒绝过期查询结果，切回对话先用缓存。
- SQLite 使用 DELETE journal + synchronous=EXTRA，避免已遇到的共享文件系统 WAL 丢失；在线检查走 Runtime API，不能由宿主打开活跃 guest 数据库。
- 任务与计划共同持久化为 `tasks.v2`，兼容读取 `tasks.v1`。重启保留排队项，运行中任务标为中断，不自动重放未知外部副作用。
- Token 用量使用共享 SQLite 请求账本，支持时间聚合、任务归属和 Pot 周期限额；额度不足的任务在 Pi 确认停止后保存恢复检查点，进入 waiting 并释放执行槽位。细节见 [Token 用量](token-usage.md)。

模型请求先持久预留额度，再联网；工具续接和失败请求也计次。取消等待 Pi 收敛，不能回滚已完成的外部操作。

具体 API、分页、报告投递与 Pi 约束由 [Runtime README](../runtime/README.md)维护。

## 当前边界

实际使用本地 VM、Pi + DeepSeek。远程 Docker 是历史 POC，Node Manager、多节点和其他引擎未产品化。

VM 隔离宿主，但 Pi 与 Runtime 仍共享 guest 用户域；worker token 限制 API，不防止同用户文件访问。桌面 broker 只有合作式仲裁，尚无 shell/CDP/X11 全通道隔离。专用浏览器工具、依赖 DAG、长期记忆和服务授权仍见 [开发计划](development-plan.md)。

## Terminal 与命令配置

`runtime/src/terminal.rs` 管理最多 8 个内存 PTY，每个最多一个连接、64 KiB 回放缓存。
连接断开只 detach，关闭或 Runtime 正常终止会结束会话。终端输入输出不写产品数据库、日志或磁盘 scrollback；默认 Shell 禁用 HISTFILE。
Terminal 与 Sandbox 是两个独立侧栏入口，导航与重连复用同一 PTY，不向 Shell 或忙碌 CLI 注入命令。Pot 不维护登录命令或专用登录终端，只查询官方登录状态；认证由用户自行在 Terminal 处理。HTTP(S) 链接仅由明确点击打开 Mac 浏览器，guest localhost OAuth 回调没有自动转发。

CLI 与 Runtime 继承同一个 guest 用户和持久 HOME；`~/.local/bin`、`~/.npm-global/bin` 加入共享 PATH。
`discovery.rs` 仅在显式调用时检查 executable 并查询已支持的官方状态接口；身份、安装、兼容性和运行准备状态分开。
Profile 的 launch_spec/protocol_contract 进入不可变绑定和 checksum；诊断版本不进入新 checksum，也不启动版本探测。旧快照继续验证原 checksum；兼容升级不因版本字符串而迁移。

Claude 每轮运行一个原版 CLI 进程，以固定 UUID 恢复。严格 MCP 配置与内置工具白名单
按 lane 限制工具；summary 与 worker 的 Runtime token 不允许编排。
结果事件之后仍等待后台任务清理与进程退出；无法确认停止时保留占用并阻止新轮次。
官方 auth status 只查询登录状态，不读取或导出凭据。用量按 CLI 轮次累计报告归账。
