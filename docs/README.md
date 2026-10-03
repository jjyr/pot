# 文档导航

## 当前决策与实施入口

| 文档 | 用途 |
|---|---|
| [产品设计](product-design.md) | pot 定位、安装到使用、Dot 交互约束、初期引擎、完整产品边界 |
| [架构概览](architecture.md) | GPUI/Swift/macOS、桌面传输、Docker 与本地 helper 的关系 |
| [POC 实施计划](poc-plan.md) | 当前优先验证的技术未知项、实施步骤、证据与继续/调整条件 |

阅读冲突时：用户最新决定 → 产品设计与 POC 计划 → 架构概览 → 带日期的研究/历史实验。POC 计划决定当前顺序；历史报告里的完整验收清单不自动成为本轮工作范围。

## 技术研究

| 文档 | 当前用途 |
|---|---|
| [Docker GUI](research/docker-gui.md) | 远程桌面、headed browser、VNC/显示与自动化分层 |
| [Apple Containerization](research/apple-containerization.md) | 本地自带运行时、Swift helper、内核/init/镜像和签名边界 |
| [OpenClaw Session](research/openclaw-sessions.md) | 可参考的会话路由、上下文与后台工作机制 |
| [Hermes Session](research/hermes-sessions.md) | 可参考的长期身份、执行上下文和任务结果回流 |
| [Session 协调对比](research/session-coordination.md) | 原生 provider thread 与产品状态的边界 |
| [身份与记忆](research/identity-memory.md) | 后续产品实现参考；当前不重新展开算法 POC |

研究里的“最新”指该文记录的研究日期，不能自动视为当前上游版本。

## 验证与可追溯性

- [P3 同窗口桌面与控制权](validation/integrated-desktop.md)：真实远端 viewer、接管/交回与重连。
- [P4 本地 helper 与 App](validation/native-helper-bundle.md)：真实 VM、打包移动运行与故障清理。

- [远程 Docker GUI P2 最小切片](validation/remote-docker-gui.md)：真实 noVNC 输入、中文粘贴、重连与持久性，及未测边界。
- [macOS shell P0/P1 实测](validation/macos-shell.md)：运行入口、真实输入/窗口证据、修复与未测边界。

- [基线实验报告](validation/baseline-report.md)：已测、fixture、文档依据与未测范围。
- [基线复现说明](validation/baseline-reproduction.md)：依赖、命令和历史路径限制。
- [复验记录](validation/baseline-verification-notes.md)：已修复缺陷和验收边界。
- [现有代码与证据](../poc/baseline/)；[证据目录](../poc/baseline/evidence/)。
- [迁入来源清单](source-manifest.json)：笔记路径、原文 hash 和仓库位置。

新实验在 `docs/validation/` 新建报告，包含环境引用、版本、命令、观测、证据位置、失败与结论。机器连接信息与运行环境记录统一放 `~/Workspace/Machines`，不在本仓库保存机器账号或连接秘密。
