# pot

pot 是从 Mac 菜单栏进入的本地个人 Agent 工作环境。先 Setup 执行引擎，再进入聊天，由默认名为 **Pot** 的助手询问命名和偏好。除引擎 Setup 与沙箱设置外，交互以 Dot 为参照。

初期引擎目标：Codex、Claude、Pi、OpenCode、Google Antigravity。当前仓库包含产品设计、架构研究和已有实验代码，尚未实现完整 App。

## 从这里开始

1. [文档导航](docs/README.md)
2. [产品设计与用户旅程](docs/product-design.md)
3. [架构边界与当前技术选择](docs/architecture.md)
4. **[产品开发计划](docs/development-plan.md)**（已有实验见 [POC 计划](docs/poc-plan.md)）
5. [已完成实验、证据与限制](docs/validation/baseline-report.md)

当前已完成 Pi + DeepSeek 的 D1 MVP：Rust Runtime + SQLite、GPUI 流式聊天、VM 内 Pi 工具执行、取消与原生会话续接。随后逐步交付报告任务、网页接管、持久计划；签名公证暂缓。长期机制参考 OpenClaw/Hermes。

## 仓库结构

```text
app/                     Rust GPUI + Swift bridge
helper/                  Swift Containerization helper
runtime/                 Rust Runtime + SQLite + AGY CLI adapter
protocol/                共享 Control API 类型
image/                   guest 启动与桌面资源
scripts/pot              prepare/build/bundle/open 产品入口
docs/
  product-design.md        产品与交互约束
  architecture.md         组件边界、进程和通信
  poc-plan.md             分阶段实施、验证、决策条件
  research/               GUI、运行时、session/记忆参考
  validation/             历史实验报告和复现说明
  source-manifest.json    迁入文档来源与原文 SHA256
poc/
  baseline/               已有最小实验代码及 evidence
```

运行现有确定性测试（Python 3.9+）：

```sh
cd poc/baseline
PYTHONDONTWRITEBYTECODE=1 python3 test_runtime.py
PYTHONDONTWRITEBYTECODE=1 python3 test_adversarial.py
PYTHONDONTWRITEBYTECODE=1 python3 local_oauth_poc.py
```

预期 29 项通过。这些测试覆盖小型 Runtime/权限/接管逻辑及 OAuth fixture，不表示远程桌面和原生交互验收通过。原生/VM 实验依赖及历史固定路径见 [复现说明](docs/validation/baseline-reproduction.md)。已有二进制、VM 镜像、依赖缓存和用户凭据不纳入仓库。

文档从 Notes 复制并整理，原笔记保留。后续项目实现以此仓库为工作入口；迁入研究是带日期的背景材料，当前实施顺序以产品开发计划为准。

## MVP 开发入口

```sh
scripts/pot prepare
scripts/configure-pi
scripts/pot build
scripts/pot bundle
scripts/pot open
```

需要 Rust、cargo-zigbuild/Zig、Xcode/Swift；开发包 `.local/d1/Pot.app`。Pi 0.87.1 和 Node 22 在 Linux VM 内运行，首次联网准备依赖，随后复用缓存。`configure-pi` 只从本机 Pi 导入 DeepSeek 凭据到开发 guest 私有配置，不写入源码或 App 包，也不调用模型。

使用方式：输入任务后按 Enter；Pi 可使用 read/write/edit/bash/grep/find/ls。点击「停止任务」或按 Cmd+. 取消；「打开工作目录」按钮或同名原生菜单访问产物。关闭窗口保持运行，菜单栏可重开；Quit 停止 VM，重开恢复同一 Pi 会话。当前为单会话 MVP。

模型为 `deepseek-v4-flash`。默认每次最多 512 输出 token、每轮最多 6 次请求，数据目录累计请求上限 100 次；可用 `scripts/configure-pi --request-limit N --max-tokens N --requests-per-turn N` 显式调整后重开 App，既有用量不会清零。自动重试关闭；工具续接也计入用量。此前直连 smoke adapter 的 3 次记录单独保留。

已实测：原生聊天发起工具任务、文件生成、Quit 重开后的语义续聊、长命令取消与无延迟副作用。本轮新增 4 次模型请求。验收与当前边界见 [Pi MVP 验证](docs/validation/pi-mvp.md)。Setup、多会话、网页接管、持久计划、多引擎和发行签名仍属后续阶段。`poc/` 保留，不再增加功能。

Pot 的 tray 菜单仅含 Open / Hide。顶部应用菜单保留 Sandbox（Cmd+D 切换 VM 桌面）、打开工作目录和 Quit Pot。Sandbox 为当前本机 Linux VM 的内嵌 noVNC 查看；跨设备远程访问尚未产品化。图标矢量源为 `scripts/make-icons.swift`，生成资产位于 `app/assets/`。
