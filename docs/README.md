# 文档导航

## 当前决策与实施入口

| 文档 | 用途 |
|---|---|
| [UI / UX 设计](ui-ux-design.md) | 已确认的窄侧栏、聊天风格、Sandbox、Agent/Setup、Pot 状态图标与 tray 未读规则 |
| [产品设计](product-design.md) | pot 定位、安装到使用、Dot 交互约束、初期引擎、完整产品边界 |
| [产品开发计划](development-plan.md) | POC 后的 D1–D6 实施顺序（纵向骨架优先）、交付物与三个场景验收 |
| [架构概览](architecture.md) | GPUI/Swift/macOS、桌面传输、Docker 与本地 helper 的关系 |
| [POC 实施计划](poc-plan.md) | 集成未知项、各阶段方法与通过条件、执行进度与下一阶段门槛 |

阅读冲突时：用户最新决定 → 产品设计与产品开发计划 → POC 计划与架构概览 → 带日期的研究/历史实验。本轮具体界面决策见 [UI / UX 设计](ui-ux-design.md)，其明确覆盖的布局与入口以该文为准。产品开发计划决定后续顺序；POC 计划保留原实验方法与前置门槛。

## 当前状态（2026-10-04）

多任务切面已实现并有限通过验收，见 [多任务实现与验收](validation/multitask.md)：独立任务对话、双任务并发、持久指导和中断恢复；自动语义路由、桌面强制互斥仍未实现。

**D1 Pi + DeepSeek MVP 已完成**：真实工具、取消、Quit 重开语义续聊通过。见 [Pi MVP 验证](validation/pi-mvp.md)。下表为历史 POC 结论。

| 阶段 | 结论 | 报告 | 主要未完成 |
|---|---|---|---|
| P0 可复现基线 | 有限通过 | [macOS shell](validation/macos-shell.md) | 全新机器/冷 registry |
| P1 GPUI + tray + macOS | 有限通过（按用户决定收尾） | [macOS shell](validation/macos-shell.md) | 50 次 tray 循环、多屏、sleep/wake、完整 IME 矩阵 |
| P2 远程 Docker GUI | 有限通过 | [远程 Docker GUI](validation/remote-docker-gui.md) | 中文直输/原生 IME、resize 适配、弱网性能 |
| P3 同窗口桌面与控制权 | 有限通过 | [纵向集成](validation/integrated-desktop.md) | tray 连续演示、shell/CDP 绕过隔离 |
| P4 本地 helper 与 App | 有限通过 | [本地 helper](validation/native-helper-bundle.md) | Chromium 干净退出、下载中断、签名分发 |

保留技术路线：GPUI + Swift bridge + WKWebView、TigerVNC/noVNC、用户进程 Swift helper + Containerization。进入产品骨架前的门槛见 [POC 计划 · 下一阶段](poc-plan.md#11-下一阶段)。

## 技术研究

| 文档 | 当前用途 |
|---|---|
| [GPUI Kit 评估](research/gpui-kit-evaluation.md) | 当前 GPUI 版本、交互/刷新诊断与组件迁移建议 |
| [Docker GUI](research/docker-gui.md) | 远程桌面、headed browser、VNC/显示与自动化分层 |
| [Apple Containerization](research/apple-containerization.md) | 本地自带运行时、Swift helper、内核/init/镜像和签名边界 |
| [OpenClaw Session](research/openclaw-sessions.md) | 可参考的会话路由、上下文与后台工作机制 |
| [Hermes Session](research/hermes-sessions.md) | 可参考的长期身份、执行上下文和任务结果回流 |
| [Session 协调对比](research/session-coordination.md) | 原生 provider thread 与产品状态的边界 |
| [身份与记忆](research/identity-memory.md) | 后续产品实现参考；当前不重新展开算法 POC |

研究里的“最新”指该文记录的研究日期，不能自动视为当前上游版本。

## 验证与可追溯性

当前产品界面：[UI / UX 改版验证](validation/ui-ux.md)，包含真实 Sandbox、设置、消息发送及用量证据。

本轮集成 POC（按阶段）：

- [macOS shell P0/P1](validation/macos-shell.md)：运行入口、真实输入/窗口证据、修复与未测边界。
- [远程 Docker GUI P2](validation/remote-docker-gui.md)：真实 noVNC 输入、中文粘贴、重连与持久性，及未测边界。
- [同窗口桌面与控制权 P3](validation/integrated-desktop.md)：真实远端 viewer、接管/交回与重连。
- [本地 helper 与 App P4](validation/native-helper-bundle.md)：真实 VM、打包移动运行与故障清理。

历史基线（从 Notes 迁入）：

- [基线实验报告](validation/baseline-report.md)：已测、fixture、文档依据与未测范围。
- [基线复现说明](validation/baseline-reproduction.md)：依赖、命令和历史路径限制。
- [复验记录](validation/baseline-verification-notes.md)：已修复缺陷和验收边界。
- [现有代码](../poc/baseline/)与[证据目录](../poc/baseline/evidence/)。
- [迁入来源清单](source-manifest.json)：笔记路径、原文 hash 和仓库位置。

代码入口总览见 [poc/README](../poc/README.md)。

## 编写新报告

新实验在 `docs/validation/` 新建报告，至少包含：待验证问题、日期与结论、固定版本、机器引用、启动与清理命令、实际操作记录、证据位置、失败与修复、结论覆盖范围。状态只用 `未测 / 通过 / 失败 / 有限通过`，有限通过须列出缺口；粘贴、fixture、协议检查不能冒充原生 IME 或原生 UI 验收。

报告是带日期的记录：后续变化以“后续状态更新”追加，不改写原观测。阶段状态变化时同步更新本页状态表与 [POC 计划](poc-plan.md) 的进度表。

机器连接信息与运行环境记录统一放 `~/Workspace/Machines`，不在本仓库保存机器账号或连接秘密。
