# 当前架构

更新：2026-10-04。本文描述已实现结构；未来产品约束见 [产品设计](product-design.md)。

```text
macOS
  Pot App（GPUI Kit）──── Swift bridge
     │                   ├─ tray / 窄侧栏 / 原生输入框
     │                   ├─ 配置与窗口生命周期
     │                   └─ WKWebView / noVNC ──────┐
     ├─ Unix IPC → Swift helper → Containerization │
     │                            Linux VM         │
     └─ bearer HTTP / SSE → Rust Runtime           │
                              ├─ SQLite / 请求账本 │
                              ├─ 主 Pi 会话         │
                              ├─ 任务队列 → Pi workers
                              └─ 定时调度           │
                                  桌面 / broker ←──┘
```

## 责任与代码入口

| 层 | 责任 | 入口 |
|---|---|---|
| App | 聊天、任务、计划、设置；缓存与后台同步 | `app/src/main.rs`、`client.rs`、`ui.rs`、`schedules_ui.rs` |
| 原生桥 | 同一 NSApplication/窗口、输入法、tray、WKWebView、配置写入 | `app/swift/Bridge.swift`、`SettingsPanel.swift` |
| Helper | 用户进程，管理 VM、资源和正常/异常关闭 | `helper/` |
| Runtime | 权威任务状态、消息事件、API、用量、恢复 | `runtime/src/main.rs`、`store.rs` |
| 编排/调度 | 单层委派、队列、完成回流、定时触发 | `tasks.rs`、`orchestration.rs`、`schedules.rs` |
| 引擎 | Pi RPC、独立原生 JSONL session、内置工具循环 | `pi.rs`、`pot-tools.mjs` |
| 协议/桌面 | 共享类型；guest 启动、浏览器与输入仲裁 | `protocol/`、`image/` |

GPUI 与 Swift 共用主窗口和事件循环，C ABI 负责桥接。普通页面复用 Kit；原生 NSTextView 负责聊天编辑，WKWebView 负责桌面。关闭窗口保留后台；Quit 正常关闭 Chromium，再停止 helper/VM。

## 主助手、任务和计划

主 Pi 根据目标选择直接回复或调用任务工具。Runtime 执行并发限制、幂等与状态转换；每个任务有独立目录和 Pi session。Worker 只获得明确任务说明，不复制主聊天历史，也不能通过编排 API 递归委派。

后台默认并发 5，超出 FIFO 排队；主聊天另占一个槽。同一任务串行，追加指导持久入队后在下一轮消费。`shared` 是显式协作锁，不是权限隔离。完成回报与状态一起保存；主会话空闲后合并最多 8 项报告进行汇总。

计划是持久定义，每次触发创建新任务。调度器负责时区、去重、错过策略及禁止重叠；模型负责检查目标和产出结构化结果。无变化时可静默，详见 [定时计划](scheduled-tasks.md)。

## 状态与恢复

- Runtime 保存产品消息/任务状态，Pi 保存原生执行上下文；binding 显式关联，不能把聊天记录当作引擎会话恢复。
- API 命令携带 request ID；消息事件有持久 cursor。Runtime 提供 SSE；当前 App 使用后台快照/分页轮询（约 500 ms），拒绝过期查询结果，切回对话先用缓存。
- SQLite 使用 DELETE journal + synchronous=EXTRA，避免已遇到的共享文件系统 WAL 丢失；在线检查走 Runtime API，不能由宿主打开活跃 guest 数据库。
- 任务与计划共同持久化为 `tasks.v2`，兼容读取 `tasks.v1`。重启保留排队项，运行中任务标为中断，不自动重放未知外部副作用。
- 模型请求先持久预留额度，再联网；工具续接和失败请求也计次。取消等待 Pi 收敛，不能回滚已完成的外部操作。

具体 API、分页、报告投递与 Pi 约束由 [Runtime README](../runtime/README.md)维护。

## 当前边界

实际使用本地 VM、Pi + DeepSeek。远程 Docker 是历史 POC，Node Manager、多节点和其他引擎未产品化。

VM 隔离宿主，但 Pi 与 Runtime 仍共享 guest 用户域；worker token 限制 API，不防止同用户文件访问。桌面 broker 只有合作式仲裁，尚无 shell/CDP/X11 全通道隔离。专用浏览器工具、依赖 DAG、长期记忆和服务授权仍见 [开发计划](development-plan.md)。
