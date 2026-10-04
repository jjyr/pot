# 历史 POC 索引

2026-10-03 的 P0–P4 实验记录，源码目录已移除；后续产品状态见 [开发计划](development-plan.md)。此页不作为当前开发待办。原分步实验方案可查 Git 历史，以下链接指向删除前的固定提交；历史命令需在该版本 checkout 中运行。

## 10. 执行记录

| 阶段 | 结论 | 已实测 | 未测 / 未通过 | 报告 |
|---|---|---|---|---|
| P0 | 有限通过 | doctor/build/run/status/stop、锁定依赖、任务根目录、嵌入资源；默认目录构建/运行；29 项 baseline fixture 回归 | 全新机器/冷 registry | [macOS shell](validation/macos-shell.md) |
| P1 | 有限通过 | 用户确认真实 tray 关闭后重开；GPUI/WebView 焦点、拼音提交/取消、导航键、单屏 2× 缩放、应用菜单 Quit；修复键路由、firstResponder、Edit 菜单、重复运行签名 | 最终构建 50 次 tray 循环、资源趋势、多屏、sleep/wake、完整输入法与快捷键矩阵 | [macOS shell](validation/macos-shell.md) |
| P2 | 有限通过 | 真实远程 Docker + TigerVNC/noVNC：基本键鼠、中文双向 Clipboard、同会话 driver、SSH 重连、同卷 restart/recreate、Chromium sandbox | 中文直输失败、原生 IME 未测；远端 resize 窗口适配有限；弱网性能矩阵 | [远程 Docker GUI](validation/remote-docker-gui.md) |
| P3 | 有限通过 | 真实 WKWebView → 远程 Docker、接管/交回、中文 Clipboard、断隧道保持归属、真实 RFB/CDP 仲裁检查 | 修复后完整 tray 连续演示、完整 IME/弱网矩阵、shell/CDP 绕过隔离 | [纵向集成](validation/integrated-desktop.md) |
| P4 | 有限通过 | 自有 Swift helper、App 移动后独立运行、真实本地桌面及共用控制、取消/guest/helper/GUI 崩溃恢复、Quit 清理 | **Chromium 干净退出未通过**（profile 仍为 Crashed）；本地 chrome://sandbox、公开分发、冷安装、完整下载中断矩阵 | [本地 helper](validation/native-helper-bundle.md) |


表中缺口是当时观测，不等于当前仍未修复：Chromium 正常退出已在 D1 修复；Pi 和任务能力的后续证据见 [Pi MVP](validation/pi-mvp.md)与[多任务](validation/multitask.md)。

## 11. 下一阶段

实施范围统一由 [开发计划](development-plan.md)维护。GPUI/tray 最小验证按用户决定收尾，不把 50 次循环、多屏和完整 IME 矩阵重新列为前置条件；有具体问题才补回归。远端 IME、弱网与完整隔离需在承诺对应能力前验收。

## 基线与复现

- [实验代码与各阶段入口](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/README.md)
- [原始基线报告](validation/baseline-report.md)、[复现说明](validation/baseline-reproduction.md)、[复验记录](validation/baseline-verification-notes.md)
- [原始 evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline/evidence)、[笔记来源清单](source-manifest.json)

历史 fixture、剪贴板路径和握手结果各有明确覆盖范围，不能替代完整模型/原生 UI/IME 验收。机器连接与部署信息统一在 `~/Workspace/Machines`。
