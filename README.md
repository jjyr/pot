# pot

pot 是从 Mac 菜单栏进入的本地个人 Agent 工作环境。先 Setup 执行引擎，再进入聊天，由默认名为 **Pot** 的助手询问命名和偏好。除引擎 Setup 与沙箱设置外，交互以 Dot 为参照。

初期引擎目标：Codex、Claude、Pi、OpenCode、Google Antigravity。当前仓库包含产品设计、架构研究和已有实验代码，尚未实现完整 App。

## 从这里开始

1. [文档导航](docs/README.md)
2. [产品设计与用户旅程](docs/product-design.md)
3. [架构边界与当前技术选择](docs/architecture.md)
4. **[下一步 POC 实施计划](docs/poc-plan.md)**
5. [已完成实验、证据与限制](docs/validation/baseline-report.md)

当前重点是远程 Docker GUI、GPUI/Tray/macOS 交互，以及可嵌入运行时/helper 的集成。长期机制参考 OpenClaw/Hermes；其完整产品验收后续安排。

## 仓库结构

```text
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

文档从 Notes 复制并整理，原笔记保留。后续项目实现以此仓库为工作入口；迁入研究是带日期的背景材料，当前实施顺序以 POC 计划为准。
