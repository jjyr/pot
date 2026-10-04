# Pi + DeepSeek MVP 验证

日期：2026-10-03。状态：**单会话 MVP 已完成，未提交**。按用户最新决定，Pi + DeepSeek 作为首个切面，AGY/Codex 后移。本报告覆盖 D1，不代表 D2–D6 或公开发行完成。

## 可操作流程

1. `scripts/pot prepare` 准备固定资源；`scripts/configure-pi` 从本机 Pi 定向导入 DeepSeek 凭据，不发送请求。
2. `scripts/pot build && scripts/pot bundle && scripts/pot open` 构建并打开开发包。
3. 在原生聊天输入任务，Enter 发送。Pi 可使用 read/write/edit/bash/grep/find/ls，工具在 Linux VM 内以普通用户运行。
4. 查看流式回复和工具开始/完成状态；通过按钮或原生菜单「打开工作目录」访问生成文件。
5. 「停止任务」或 Cmd+. 取消当前任务。Runtime 等待 Pi 的 abort 确认和进程退出；停止未确认时要求重开环境，不继续接收任务。
6. 关闭窗口保持运行；Quit 停止 VM。重开保留产品历史、工作文件和同一个 Pi 原生会话。

Pi 0.87.1、Node 22.23.2、guest APK 与 npm integrity lock 固定；首次启动联网安装，之后复用缓存。Runtime 仍为 Rust + SQLite，Pi 通过 JSONL RPC 接入。Pi 原生会话与历史 smoke adapter 的模型上下文分开，旧界面记录不会上传给 Pi 模型。

## 实测结果

| 检查 | 结果 |
|---|---|
| 原生聊天 → VM → Pi → DeepSeek | 通过，模型真实返回，使用 SSE 更新 UI |
| 真实工具 | Pi 调用 bash 创建 `mvp-check.md`，读取输出后回复 `DONE`；文件可通过 Finder 访问 |
| Quit / 重开 | helper 正常停止，Chromium profile `Normal`；重开后历史、文件、额度保留 |
| 原生会话与语义续聊 | 重开前后 Pi session ID 相同；询问上一轮 codeword，正确回复 `apricot`，未在新提示中透露答案 |
| 真实取消 | Pi 执行 `sleep 30` 后写文件的命令，Cmd+. 后返回 `Command aborted`，用户消息为 Cancelled，超过 30 秒后目标文件不存在，模型请求数没有继续增长 |
| 取消状态显示 | 实测暴露 Pi 把底层 abort 包装为错误；已按当前取消状态映射为 Cancelled，本地真实 Pi 集成测试确认取消轮不再产生 Failed 状态 |
| 工作文件入口 | 原生菜单打开 Finder，生成文件可见 |
| 普通自动化测试 | App 4 + protocol 1 + Runtime 8 = 13 项通过 |
| 真实 Pi / 模拟模型 | 另 1 项通过：write/bash、重启 Pi 续接、取消后 bash 子进程退出、取消后再发、请求 cap、会话文件缺失时拒绝静默重建 |
| 构建 | macOS App、ARM64 musl Runtime、Swift helper 构建；App ad hoc deep/strict 校验通过 |

真实验证额外消耗 **4 次** DeepSeek 请求，8719 输入 token，其中 6016 缓存命中；122 输出 token。工具任务 2 次、重开续聊 1 次、取消验证 1 次。没有自动重试。没有估算货币费用。此前 smoke adapter 的 3 次请求记录保留，未重置。

忽略目录中的本轮证据：`.local/d1/pi-mvp-verification.json`、`pi-tests.log`、`pi-integration.log`、`pi-build.log`。机器部署与当前运行状态在 Machines 中维护。

## 自动化入口

```sh
cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" cargo test --locked -p pot-runtime real_pi_tools_resume_cancel_and_budget -- --ignored --nocapture
```

第二项需要本机安装 Pi 0.87.1。它使用独立临时目录、虚拟凭据和本地模拟 provider，不读取本机 Pi 认证，也不调用真实模型。正常测试默认跳过该项，避免要求所有开发机都安装 Pi。

## 当前边界

- 一个持久会话；多会话、任务卡、附件、结构化产物预览仍在后续阶段。
- 为节省用量，当前配置每次最多 512 输出 token、每轮 6 次模型请求、累计 100 次；上下文请求体上限 96 KB，关闭自动重试和自动压缩。到达上限显式失败，不静默丢历史。参数可通过 `configure-pi` 调整后重开，历史 ledger 不清零。
- Pi 原生工具可操作 guest 内普通用户可访问的文件及网络。尚未实现 D4 的独立 Agent/controller UID 与 CDP/X11 隔离，不能宣称可承载不可信网页接管。
- Setup 向导、下载续传、跨机器冷安装和发行签名未交付。开发包仍用 ad hoc 签名。
- 故障恢复保留产品部分输出，并把中断消息标为失败；不自动重放用户提示。不支持通过本 MVP UI 修复损坏/缺失的 Pi session 文件。


## Sandbox 浏览器生命周期回归（2026-10-03）

修复 `desktop.sh` 以 Chromium 退出作为 VM 退出条件的问题：浏览器关闭后继续等待 Runtime，保留桌面和 noVNC。统一 `open-browser.sh` 启动入口记录浏览器 PID；Fluxbox 桌面右键菜单提供 Chromium / Terminal。退出 Pot 时仍通过 CDP 正常关闭当前浏览器，浏览器已经关闭时直接确认停止。

真实 VM 手工回归（不调用模型）：

1. Take over 后点击 Chromium 关闭按钮：保留空桌面，Pi Ready；helper 为 running，Runtime snapshot HTTP 200。
2. 桌面空白处右键 → Chromium：浏览器重新打开，原 profile/session 保留。
3. 重开浏览器后退出 Pot：收到 browser-stopped，profile.exit_type 为 Normal。
4. 浏览器已关闭时退出 Pot：同样收到停止确认，profile 为 Normal；再次启动连接正常。

`sh -n`、产品构建和 bundle 完成。本轮模型请求为 0。
