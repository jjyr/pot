# GPUI Kit 适用性评估

> 历史研究：版本与“当前”均指原文核对日期。实施现状见 [开发计划](../development-plan.md)，不是本报告中的建议或旧状态。

核对日期：2026-10-04。结论：建议分阶段采用 GPUI Kit 的组件、文本和交互基础，同时修复 Pot 自己的更新与窗口集成方式。本轮只修改 tray 点击行为，没有升级 UI 依赖；未做新旧框架实测帧率对比。

## 版本事实

| 项目 | 本次查询结果 |
|---|---|
| Pot 当前依赖 | `gpui = "=0.2.2"`，Cargo.lock 一致 |
| crates.io `gpui` 最新稳定版 | 0.2.2，发布于 2025-10-22 |
| `gpui-kit` 最新稳定版 | 0.7.0，发布于 2026-09-28 |
| gpui-kit 0.7.0 已发布依赖 | `gpui-pre = "=0.3.7"`、匹配 platform/web 包，另有 base/component 0.7 |
| `gpui-pre` 最新稳定包版本 | 0.3.7，发布于 2026-09-28；这是快照包名，不等于 Zed 官方 gpui 0.3.7 稳定版 |
| 研究的 Kit main | `3a39e9dfc97d8b12cf3c116708ada36bee02220b`，2026-10-03 更新 |

因此“当前 GPUI 是不是最新版”需要区分：Pot 是同名 crates.io 包的最新稳定版，但没有采用 Kit 当前使用的更新快照。Kit main 的特性与修复不自动等同已经发布的 0.7.0 内容；例如当前 main 顶端还有输入框横向滚动与中文软换行修复。

## Pot 当前体验的可解释因素

以下为源码事实与诊断假设，不是 GPU 帧率测量结果：

- Rust Shell 每 200ms 消费 Swift 事件，随后无条件 `cx.notify()`。原生侧栏、发送等跨桥事件因此可能等待下一次 tick；无状态变化时也会重绘。
- Client 每 500ms 拉取任务目录、主聊天与当前任务快照。模型文本会按这个节奏可见，UI 框架升级不能自行消除这段延迟。
- 每次 render 克隆视图/消息；Markdown 在 render 路径重新解析；消息列表尚未虚拟化。长会话需要缓存与可见区域渲染。
- Swift 每 100ms 重画 tray/侧栏状态图标，处理动画实际只有约 10 次每秒更新。
- GPUI 管理的 contentView 被 Swift 重新挂到 HostView 内并平移 56px；原生视图与 GPUI 分别管理布局、焦点、鼠标及窗口菜单。该边界需要专项核对坐标、tracking area、滚动和失焦。此前已实际遇到原生菜单 validation 与 GPUI mutex 冲突。
- 普通操作按钮多数是手写 div + on_click，缺少统一 hover/pressed/focus/disabled 状态；基础 Markdown 没有完整的鼠标文本选择体验。
- 当前安装脚本使用 debug 构建，workspace dev profile 仅关闭 debug info，没有设置优化等级。需要用优化构建对照后再判断框架性能。
- GPUI 0.2.2 的 macOS 实现本身有 DisplayLink 和输入事件驱动的绘制路径。200ms 的业务 tick **不能解释成整个 GPUI 只有 5 FPS**。

## 为什么适合引入 Kit

Kit 仍基于 GPUI GPU 绘制，不是 AppKit 原生控件替代层。收益主要是已有的组件行为与状态管理：

| Pot 区域 | Kit 候选能力 | 预期收益 |
|---|---|---|
| 任务操作、设置 | Button、表单、弹层、主题和焦点基础 | 统一鼠标/键盘反馈，减少手写状态 |
| 聊天正文 | TextView/TextViewState、可选择 Markdown、流式增量更新 | 改善选择、复制、文本渲染与流式状态管理 |
| 长对话与任务列表 | 虚拟列表（含可变高度列表） | 避免每次渲染全部内容 |
| UI 验证 | test-support、交互与可访问性检查 | 自动验证焦点、输入、点击与重绘后状态 |
| 性能诊断 | profiler、gpui-fps | 为帧时间测量提供工具，不是性能保证 |

README 的“120 FPS”是项目能力声明，不能作为 Pot 已达到 120 FPS 的证据。其 AccessKit 支持也需在具体控件和当前 macOS 桥接方式下复测。

## 迁移边界与建议顺序

1. **先修更新路径**：将原生事件变成即时主线程通知，模型消息恢复增量推送；仅在状态变化时重绘。缓存 Markdown 状态，随后虚拟化历史。测量空闲 CPU、点击到反馈延迟、滚动帧时间与流式文本延迟。
2. **统一 GPUI 版本并做薄切片**：使用 `gpui-kit = "=0.7.0"` 的匹配 GPUI 类型/入口，替换当前直接依赖的 `gpui 0.2.2`。不能让业务同时混用两套 Entity/Window/App 类型。先验证单窗口、任务卡片和可选择聊天正文。
3. **收敛混合窗口边界**：让 Kit/GPUI 拥有其 Root、布局和交互树；Swift 保留 tray、生命周期及必要的 WKWebView 接入。输入框现有 NSTextView 的中文 IME 已有使用基础，先保留，Kit Input/textarea 经拼音组合、候选、换行和焦点专项验证后再决定替换。
4. **继续保留现有 noVNC/WKWebView**：Kit 的 `gpui-wry` 文档明确标为 experimental，且原生 WebView 会覆盖其矩形下的 GPUI 内容。不能假定改用它就解决 Sandbox 叠层、焦点或性能问题。
5. 在同一台机器、同一数据集、同一缩放和窗口大小、同一优化构建下对照。覆盖长聊天、鼠标 hover/点击、触控板惯性滚动、文本选择、中文输入、Sandbox 切换、Hide/reopen 与未读。帧时间目标按屏幕刷新率确定：60Hz 约 16.7ms，120Hz 约 8.3ms；分别记录 p50/p95，不凭主观感觉或定时器频率声称达标。

无需引入 JS 扩展运行时、完整 IDE 编辑器或 dock 系统来完成当前聊天产品。Rust API、Pi 调度和数据存储可以保留。

## 本轮 tray 改动

删除 NSStatusItem 的 Open / Hide 菜单；左键松开直接调用已有 `showWindow`：显示当前窗口并激活 App。不切换当前页，不清空草稿；未读数字保留。隐藏仍可用关闭窗口或 Cmd+W，退出用 Cmd+Q。

## 来源

- [GPUI 当前包元数据](https://crates.io/api/v1/crates/gpui)
- [Kit 当前包元数据](https://crates.io/api/v1/crates/gpui-kit)
- [Kit 0.7.0 已发布依赖](https://crates.io/api/v1/crates/gpui-kit/0.7.0/dependencies)
- [GPUI 快照包元数据](https://crates.io/api/v1/crates/gpui-pre)
- [Kit README（固定研究版本）](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/README.md)
- [Kit workspace 版本与优化配置](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/Cargo.toml)
- [流式 Markdown 示例](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/examples/stream-markdown/src/main.rs)
- [WebView 限制](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/crates/webview/README.md)
- 本地检查：`app/src/main.rs`、`app/src/client.rs`、`app/src/markdown.rs`、`app/swift/Bridge.swift`、`scripts/pot` 与 GPUI 0.2.2 macOS 平台源码。

## 本轮验证

Pot App 编译、bundle deep/strict 签名验证通过，安装包已更新且启动成功。关闭窗口隐藏行为已实际操作；当前 CUA 只暴露应用窗口，不暴露该应用的菜单栏 status item，隐藏后返回 `noWindowsAvailable`，因此没有把 tray 实际点击记为已自动化验证。本轮没有模型请求，也没有帧时间基准测量。

## 2026-10-04：发布版组件研究与迁移落地

本轮以 crates.io **0.7.0 实际源码**为准，不以 main 的新增接口实施。统一依赖 `gpui-kit = "=0.7.0"`，锁文件使用 `gpui-pre 0.3.7`；旧 `gpui 0.2.2` 已移除。

| 组件 | 实际采用与边界 |
|---|---|
| application / init / open_window / Assets | 使用 Kit 平台入口和内置 Root；同一套 GPUI 类型贯穿窗口与组件 |
| Button | 新建任务、返回、停止、工具折叠、复制、进入任务、最新消息；使用 ghost 样式和组件交互状态 |
| ListItem | 任务卡片，保留状态说明与选择回调 |
| TextView（Kit base） | 替代手写 pulldown 渲染；稳定消息 ID 复用解析/布局状态，支持拖选和复制、Markdown 表格/代码；链接仅允许 http/https。使用 base 接口可明确设置蓝色气泡内白色文字 |
| Theme | 跟随原生外观设置同步 Kit 主题 |
| Sidebar | 已核对 Icon 折叠模式（默认收起 48px）；本轮保留现有 56px AppKit 导航，以免同时改动 WKWebView 叠层 |
| Input / Textarea | 已核对独立 InputState/TextareaState；现有 NSTextView 保留，后续替换需单独验证拼音组合输入 |
| MessageScroller / virtual_list | 已核对可变高列表、tail follow、splice/reset；本轮未迁移列表存储，长历史虚拟化仍待实施 |
| WebView | 继续使用现有 WKWebView，未引入 experimental Wry |

同时修复：GPUI view 保持完整窗口坐标，56px 导航留白由 Rust 布局负责；原生事件通过有界 channel 立即唤醒 Shell；200ms 后台检查仅在 View、页面、主题或输入框高度变化时 notify，不再无条件重绘。Runtime 的 500ms 快照轮询仍保留。原生编辑菜单按焦点路由，GPUI 选区使用 Kit Copy/SelectAll，NSTextView 编辑快捷键独立处理。占位文字不再拦截输入框鼠标点击。

### 本轮验证

- `cargo build --locked -p pot-app` 通过，bundle ad hoc deep/strict 签名通过。
- `cargo test --locked -p pot-app -p pot-protocol -p pot-runtime`：App 6、protocol 1、Runtime 17 通过；2 项默认忽略的真实 Pi/mock 集成本轮未运行。原手写 Markdown parser 的测试随实现一起移除，因而 App 测试数比上一轮少 1。
- 实际窗口验证：历史任务列表恢复、任务卡片点击进入、消息拖选与 Cmd+C 后粘贴出“B完成”、深色主题、Sandbox 显示“已连接”。修复新建按钮固定高度与 padding 冲突导致的文字裁切。
- 本轮没有模型请求，也没有 FPS/帧时间基准。当前安装仍是 dev 构建，不能据此宣称 120 FPS 或完全原生性能。

保留的原生部分：导航、设置/Setup、输入框、tray、Sandbox。该迁移完成的是 GPUI 渲染层及组件交互层，未声称全 AppKit 界面已重写。

最终安装构建复测：中文两行粘贴、Shift+Enter、Cmd+A/Backspace 清空、占位区域直接点击输入、新任务入口通过，测试草稿已清除，外观恢复“跟随系统”。未进行真实拼音候选窗口专项测试。重新启动时必须等旧进程正常退出，再执行 `scripts/pot open`，避免 LaunchServices 把打开请求发给仍在退出的实例。
