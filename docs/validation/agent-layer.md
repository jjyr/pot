# Agent layer 验证

日期：2026-10-04。范围：#2 的 Pi/Runtime 基础增量；不是多引擎完整验收。

## 已验证

- Runtime 56 个非忽略测试、protocol 1 个测试通过。
- 原有 6 个 `real_pi` 测试保持通过；新增第 7 个真实 Pi 测试，通过两个本地模拟
  provider 验证迁移、模型选择、handoff bootstrap、旧 native session 保留和 provider 归属。
- Runtime/protocol `cargo check --locked`、Rust 格式和 `git diff --check` 通过。
- 没有付费模型调用；没有读取宿主 Codex 登录态或复用宿主会话。

覆盖旧元数据保留、重启后的固定快照、损坏原生会话、重复/迟到/终止后事件过滤、
迁移每个持久阶段重启、候选会话失败、设置等待运行轮次、新任务继承/旧任务固定、
既有设置导入幂等性、未知协议透明转发与非零占位语义、MCP 汇总权限和重复调用冲突。

复现使用开发指南中的 `mise run test` 和真实 Pi 命令；真实 Pi 测试只连接测试启动的
本地模拟模型。生产额外 provider 仍强制 allow-listed HTTPS，不允许测试的 loopback HTTP。

## Codex 探测（未验收）

候选：`@agentclientprotocol/codex-acp` 2.1.1、`@openai/codex` 0.159.1，安装到临时目录。
单独创建空 Codex 配置目录，剔除 API-key 环境变量，仅发送 ACP `initialize` 和
`session/new`；未发送 prompt、未登录、未访问用户历史。

- initialize 成功，报告 HTTP MCP、load/resume 等能力。
- session/new 返回 `-32000 Authentication required`。
- 不把能力声明视为取消、后台资源收敛、线程隔离或安全恢复验收。
- 探测后停止整个进程组。候选依赖未加入 Sandbox 镜像。

源码参考固定到 [ca1d9717](https://github.com/agentclientprotocol/codex-acp/tree/ca1d97173ad37b471d5a4e5847725a4657d34e29)：
`CodexAcpClient.createSessionConfig` 将 MCP servers 传入线程配置；`cancel` 请求
turn interruption 并等待 Codex turn/completed。前者尚无跨 lane 的负向实测，后者
不能单凭源码证明所有后台终端与子资源已停止。因此未实现/启用 CodexDriver。

## 未测范围

未构建或启动新 Sandbox 镜像，未跑真实 Codex 模型、登录刷新、musl ARM64、MCP
多 lane 隔离、native subagent 禁用、Codex 后台命令停止及恢复。未验证完整 profile
设置 GUI；当前通用 profile 操作使用 Runtime API，原设置表单保留。driver fake
覆盖目前是事件 gate，不替代完整 fake backend 的生命周期/取消测试。尚无按 provider
预算和按 profile/provider 并发限制。详细进度仅在开发计划维护。
