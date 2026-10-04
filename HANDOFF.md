# pot 开发交接

更新：2026-10-03。本文件总结已完成的集成 POC 和建议接续顺序；详细事实以各阶段验证报告为准。

## 当前结论

**Pi + DeepSeek 的 D1 MVP 已验收完成；下一步是 D2 Setup。完整产品及公开发行仍未验收。** 最新事实见 [Pi MVP 验证](docs/validation/pi-mvp.md)；下方 POC 与 AGY/API smoke 记录保留为历史。 当前保留 GPUI + Swift/AppKit + WKWebView、TigerVNC/noVNC，以及用户进程 Swift helper + Apple Containerization 路线。

最近代码提交：`4ebd7ee`（P3/P4）。前序：`08d6d3a`（P2）、`53bf18c`（范围收敛）、`3a54a9b`（P0/P1）。

## 接手先读与范围约束

1. [文档导航](docs/README.md)及当前用户提供的仓库指令。
2. [产品设计](docs/product-design.md)、[产品开发计划](docs/development-plan.md)、[架构](docs/architecture.md)。
3. 下表的实测报告和对应目录 README。

用户明确决定：GPUI/tray 最小验证足够，停止扩展泛化验收。不要再把50次循环、多屏、sleep/wake、完整输入矩阵作为本轮待办；只有新集成暴露具体问题时针对性修复。用户已亲手确认 tray 关闭后重开、以及“可以输入”；后者未说明方法，不能升级成完整远端 IME 通过。

长期身份、记忆、任务和 session 参考已有 OpenClaw/Hermes 研究，按三个产品场景实现，不另起算法研究。产品名 pot、默认助手 Pot；Engine Setup 在聊天 onboarding 之前；初期引擎目标为 Codex、Claude、Pi、OpenCode、Google Antigravity。除引擎 Setup 和沙箱设置外，交互按 Dot 对齐。

机器连接、资源和部署信息统一维护在 `~/Workspace/Machines`。不要提交 token、profile、engine home、VM 磁盘、依赖缓存或构建产物；`.local/poc/` 为忽略的任务目录。不要用历史 fixture 或内部方法调用替代真实原生/远程验收。

## 已完成的工作

| 阶段 | 实现与实际通过项 | 限制/报告 |
|---|---|---|
| P0/P1 | 固定 GPUI 依赖、消除源码硬编码路径、构建/启动/停止入口；tray 重开、GPUI/WKWebView 焦点、本地拼音提交/取消、单屏缩放、Quit | 最小切片有限通过；完整矩阵未测。[报告](docs/validation/macos-shell.md) |
| P2 | 真实远程 Docker + TigerVNC/noVNC；同一浏览器 driver/viewer、键鼠、中文双向 Clipboard、隧道恢复、同卷 restart/recreate、远程 Chromium sandbox | Unicode 直接输入自动化失败，原生远端 IME 未测；resize 适配有限，弱网性能未测。[报告](docs/validation/remote-docker-gui.md) |
| P3 | WKWebView 操作真实远程桌面；broker 管查看/接管/交回；拒绝冲突输入、等待在途 driver、释放按键并等 Fence、断线保留 owner | 合作式双通道仲裁，任意 shell/CDP/Docker 仍可绕过；最终整段 tray 演示未重跑。[报告](docs/validation/integrated-desktop.md) |
| P4 | 自有 Swift helper 直接链接 Containerization；Unix IPC、真实 VM、本地共用 viewer；移动后的 App 独立运行；重复启动、取消、guest/helper/GUI 崩溃恢复、Quit 清理 | ad hoc 签名；首次联网下载；正常 Quit 后 Chromium profile 仍为 Crashed；发行与完整故障矩阵未测。[报告](docs/validation/native-helper-bundle.md) |

实验目录：`poc/macos-shell/`、`poc/remote-desktop/`、`poc/integrated-desktop/`、`poc/native-helper/`。历史 `poc/baseline/` 保留，不作为当前实现继续堆叠。

关键修复不要回退：

- 自有主菜单、Edit、状态菜单关闭自动 enable，避免 AppKit 菜单校验重入 GPUI mutex 死锁。
- RFB SetEncodings 按4字节解析；broker 消耗自己的 Fence 回执，避免 noVNC 断开；交回释放标准/QEMU 修饰键与鼠标。
- WKWebView 原生自动化事件曾缺 buttons/独立 Ctrl 过渡，viewer 对矛盾字段做归一，epoch 变化时重置 Ctrl 跟踪。不能据此推断所有实体键盘/IME 路径已测。
- helper 对专用 runtime root 加 flock；代次保护、父进程 watchdog、退出兜底与 stale socket 恢复。VM running 不等于桌面 ready，App 等实际 HTTP 后才载入。

## 启动与亲手验证

从项目根目录运行。构建要求 Apple Silicon、macOS26+、Xcode/Swift6.2+、Rust；运行已打包 App 不需要宿主 Python、cctl 或 Docker。

### 本地 App

```sh
poc/native-helper/poc prepare
poc/native-helper/poc build
poc/native-helper/poc bundle
poc/native-helper/poc open
```

默认产物 `.local/poc/pot Local POC.app`，另有已验证的移动副本 `.local/poc/relocated/pot Local POC.app`。**重新构建只更新默认产物，不自动更新移动副本。** 完整资源必须随 App 一起移动。

操作：Start local desktop → 等桌面出现 → Take over → 输入或 Clipboard / Send clipboard / 远端 Ctrl+V → Save profile marker → Return control → Driver write → Stop。初始 owner 为 none，需显式接管或交回。中文 Clipboard 已通过，不等于中文 IME。

关闭窗口保留运行时；Quit 停止本地 helper/VM。默认数据在 `~/Library/Application Support/pot-poc`；profile 和 workspace 持久化。首次准备需下载固定 OCI/APK，暖缓存可复用。页内沿用的测试标题仍写 remote Docker；上方 `local-swift-vm` 是 backend 标识。

### 远程桌面

先按 [P2 README](poc/remote-desktop/README.md) 启动专用容器与隧道，连接目标从 Machines 获取；随后：

```sh
export POT_REMOTE_SSH='<Machines 中的 SSH destination>'
poc/integrated-desktop/poc prepare
poc/macos-shell/poc build
poc/integrated-desktop/poc run
poc/integrated-desktop/poc open
```

如果已有原生 shell 或 broker 运行，先用其 status 检查，必要时 stop 后再打开，避免使用旧 token 的窗口。GUI Quit 不停止远程容器。完整停止入口：

```sh
poc/macos-shell/poc stop
poc/integrated-desktop/poc stop
poc/remote-desktop/poc untunnel
poc/remote-desktop/poc stop
```

最后交接时本地 App 已重新打开、VM 停止；远程 P2 环境和 broker 曾保持运行。**这是上次观测，接手后先核实，不能当当前实时状态。** 用户授权停止远端 llama-server 以腾内存；恢复方式及环境状态见 Machines，不另行修改其他服务。

机器记录索引：

- `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`
- `~/Workspace/Machines/machines/local-mac-m4/pot-macos-shell-20261003.md`

## 下一阶段

2026-10-03 D1 已开工：用户确认 Rust Runtime + SQLite、正式目录、暂缓发行签名；认证进一步明确为先用 AGY CLI 的 Google 账号登录，Codex ACP 后续接入。产品入口为 `scripts/pot`，开发包 `.local/d1/pot.app`。构建、基础测试、真实 VM Runtime/AGY 启动及 Chromium `Normal` 退出已验证。当前 App 使用 `.local/d1/local-runtime`，guest 登录终端已打开并显示 Google OAuth 选项；等待用户登录后验收真实回复、工具写文件与原生会话续接。完整事实与限制见开发计划末尾，不能将 D1 标为完成。

执行顺序统一见 [产品开发计划](docs/development-plan.md)（v2）：用户已确认 Rust Runtime、正式目录、AGY CLI Google 登录先行、Codex ACP 后续、暂缓签名 → D1 纵向骨架（GPUI 聊天 → VM 内 Rust Runtime → AGY CLI 真实回复与工具）→ D2 Setup/引导 → D3 报告（S1）→ D4 网页接管（S2，含隔离负测）→ D5 计划（S3）→ D6 多引擎与交付。签名公证为并行轨道。

Chromium 干净退出并入 D1：等待真实浏览器关闭确认再停 VM，不直接修改 profile 标记；VM 存活须结合 helper、IPC 和实际 HTTP 判断。其他缺口与证据以上表和产品开发计划为准，不在此重复维护待办。

## 验证与证据入口

- 各 `poc/*/evidence/` 为可提交的小型证据；完整日志和构建资源在忽略目录，截图多在执行工具记录，未保存完整录像。
- 真实远端协议检查：`.local/poc/integrated-venv/bin/python poc/integrated-desktop/verify_broker.py`。依赖已运行的 P2 与 broker，会改专用测试页。
- 真实 VM 故障检查：`python3 poc/native-helper/verify_lifecycle.py`。先 prepare/build/bundle；使用独立 fault-runtime 的 probe.sh，不替代桌面 UI 验收。
- 最终上述两项通过，Python/shell 语法、JSON、App deep/strict 签名与 git diff 检查通过。修改相关边界后复验即可，不为文档修改重复启动 VM 或跑历史29项 fixture。
- helper 关键证据：`lifecycle.json`、`gui-crash.json`、`app-quit.json`、`final-app-quit-helper.jsonl`、`final-native-returned.json`。通过、有限通过、失败、未测继续分别记录。

## 最新 API 测试状态（2026-10-03）

用户改用本机 Pi 的 DeepSeek key，模型采用配置中的 `deepseek-v4-flash`。`scripts/configure-deepseek` 只导入该凭据到开发 guest 私有配置；Rust Runtime 在配置存在时走受限 API adapter。真实原生聊天、流式回复、guest 文件工具、修复后的 Quit/reopen 保存已通过。当前 `.local/d1/local-runtime` 已消费硬限额 3 次请求，**不要重置额度或自动追加模型测试**。共享目录 SQLite 已从 WAL 改为 DELETE/EXTRA；运行时读状态必须走 API，勿从宿主并发打开数据库。详细失败过程、修复及费用证据见开发计划末尾；仍未提交。


## 最新客户端进度（2026-10-03）

GUI 已切换 SSE，具备事件去重、缺口恢复与断线重连；发送错误保留、额度可见，耗尽时前后端共同阻止发送，服务端不再新增失败消息。11 项本地测试及真实 VM SSE/心跳/429 无写入检查通过。新版 App 已打开，历史保留，额度仍为 3/3，本轮没有调用模型。完整记录见开发计划“D1 流式客户端补充”；尚未提交。


## 最新 Pi MVP 交接（2026-10-03）

用户要求以 Pi + DeepSeek 推进到可用 MVP，已完成。`scripts/configure-pi` 定向导入本机 Pi 的 DeepSeek 凭据；`scripts/pot open` 打开当前开发包。Pi 0.87.1 在 VM 内提供 read/write/edit/bash/grep/find/ls，Rust 管持久消息、SSE、取消与有界模型网关。UI 支持 Enter 发送、Cmd+. 或按钮停止、原生菜单打开工作目录。

真实创建文件、原生 Quit 后语义续聊、长命令取消已通过；累计新增 4 次 DeepSeek 请求，详见验收记录。用户已允许任意必要调用但要求尽量节省；先前“3 次耗尽不得再调用”仅描述旧 smoke adapter，不能再当作当前用户限制。旧 ledger 保留，Pi 使用独立 ledger；默认 100 次累计上限、512 输出 token/次、6 请求/轮，显式配置调整不会清零。不要为重复证明已通过项继续花费。

13 项常规测试和 1 项真实 Pi/模拟模型集成测试通过；源码与文档未提交，用户原有修改和 AGENTS.md 删除均保留。后续范围：Setup、多会话、附件与结构化产物、网页隔离/接管、计划、多引擎和发行。机器运行状态统一记在 Machines。


## Pot 名称与菜单更新（2026-10-03）

应用显示名、窗口名和开发包已统一为 Pot（`.local/d1/Pot.app`）。tray 仅 Open / Hide；顶部 Pot 应用菜单保留 Sandbox（Cmd+D）、工作目录和 Quit。新增原创 App 图标及单色 tray 模板，矢量源 `scripts/make-icons.swift`。构建/签名通过，内嵌 noVNC 实际显示当前 Linux VM 桌面；跨设备远程访问尚未产品化。本轮没有主动模型调用。


## Sandbox 关闭浏览器修复（2026-10-03）

Chromium 不再决定 VM 生命周期；关闭后桌面、noVNC、Runtime 保持运行。接管后在桌面空白处右键 → Chromium 可重开。新增 image/open-browser.sh，已加入 prepare/bundle/helper 资源复制。浏览器关闭、重开、两种退出路径真实回归通过，见 docs/validation/pi-mvp.md。本轮不调用模型，仍未提交。


## UI / UX 改版（2026-10-03）

按 docs/ui-ux-design.md 实现窄图标侧栏、无中央头像的气泡聊天、原生多行输入、独立 noVNC 页面及资源详情、Agent/Sandbox 设置、锅盖状态图标与 tray 未读。新增 app/swift/SettingsPanel.swift、UnreadTracker.swift 以及 Markdown/安全产物入口。验证范围见 docs/validation/ui-ux.md。Agent 目前真实支持 Pi + DeepSeek，首次未配置时复用同一表单；不把未来引擎显示为可用。

helper 现在同时设置 VMResources 和容器限额，修复原先配置 3 GiB 而 VM 实际只有默认 1 GiB 的问题。Sandbox 配置保存到运行目录 sandbox.json；不要将凭据写入仓库。新增加一次短模型验收（2 输出 token），Pi ledger 当前 6/100。历史与用户修改保留，未提交。


## 2026-10-04 多任务切面

已实现任务入口、独立 Pi session/工作目录、最多两个后台任务、独立主会话槽、FIFO 持久指导、定向取消、中断恢复和主聊天回报。详情与真实验收边界见 `docs/validation/multitask.md`。本轮 4 次 DeepSeek 请求、70 输出 token，ledger 10/100；无需再做收费测试。保留工作区原有修改，未提交。自然语言自动委派、桌面 broker 强制互斥未实现，不得将 shared 调度标签称为安全隔离。

## 2026-10-04 Tray 点击与 GPUI Kit 研究

移除 status item 的 Open/Hide 菜单，左键点击调用 showWindow；状态与未读保留。已编译、打包并更新 `.local/d1/Pot.app`。`docs/research/gpui-kit-evaluation.md` 记录 gpui 0.2.2 仍为同名包最新稳定版、gpui-kit 0.7.0 使用 gpui-pre =0.3.7，以及更新/混合窗口边界的诊断。当前只研究建议，未迁移依赖，也未实测 FPS；不要将业务 200ms tick 说成 GPUI 渲染上限 5 FPS。

## 2026-10-04 GPUI Kit 迁移

- App 已从 gpui 0.2.2 切到 gpui-kit 0.7.0 / gpui-pre 0.3.7，使用 Kit Root、Button、ListItem、可选择 TextView 与主题。
- 保留 AppKit rail/settings/NSTextView 和 WKWebView；GPUI view 使用完整窗口尺寸，Rust 为 rail 留白，避免窗口坐标错位。
- Swift 事件即时唤醒 Rust；周期检查只有视图状态变化时重绘。Runtime 仍 500ms 轮询，外层消息列表尚未虚拟化。
- 原生编辑键/菜单按焦点路由；placeholder 不拦截输入点击。tray 无菜单，左键直接打开保留。
- 研究与验收：`docs/research/gpui-kit-evaluation.md`。免费 Rust 回归 24 项通过，无新模型请求。构建安装到 `.local/d1/Pot.app`，仍 dev 构建；未测 FPS。

## 2026-10-04 UI 细节优化

顶部 Pot 标记已与聊天入口合并；RailButton 提供 hover/选中颜色。复制使用 Kit Clipboard + message group hover，任务页居中限宽、标题右侧新任务、卡片状态精简。设置原生卡片表单统一列宽和垂直对齐；Pi 固定只读，DeepSeek 凭据明确标注归属，用量限制默认折叠。App 编译/打包签名及实际复制、表单展开、任务点击验证通过；未调用模型、未保存凭据。说明见 docs/ui-ux-design.md / docs/validation/ui-ux.md。

聊天切页优化：Client 同会话 select 不清空快照；切回主会话复用 main_snapshot，任务缓存最多 16 条且 boot_id 变化清除。select 使用有界刷新通知跳过 500ms 轮询等待，receive 完成即时唤醒 UI。App 8 项测试通过；已打包安装，无模型调用。首次环境连接仍需等待。

### 2026-10-04 三页风格统一

任务、设置、Sandbox 外层已统一 gpui-kit。共用布局在 `app/src/ui.rs`；设置为 Kit 表单，`SettingsPanel.swift` 现仅是配置服务。Sandbox 工具栏为 Kit，noVNC/Web 信息面板保留网页实现，应用内隐藏重复标题栏，并跟随外观。新版已 build/test（8/8）、bundle、重开；无模型调用。规范重写为简明的 `docs/ui-ux-design.md`，范围与实测限制见 `docs/validation/ui-ux.md` 最后一节。未提交。
