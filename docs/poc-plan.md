# pot 集成 POC 实施计划

状态：P0/P1 最小切片已实施，按用户决定收尾，不再扩展 GPUI/tray 验证。日期：2026-10-03。实测与剩余项见 [macOS shell 验证](validation/macos-shell.md)；P2 仅完成候选节点只读检查，P3/P4 未测。现有基线实验不代表这些阶段已通过。

## 1. 目标与范围

本轮回答三个尚无法确认的问题：

1. GPUI 与 macOS tray、窗口、输入和应用生命周期能否稳定共存？
2. 远程 Docker 中的 Linux GUI 能否在 GPUI 窗口内获得可用的查看与控制体验？
3. GPUI App 能否通过 Swift helper 管理自带运行时，并以独立 App 形式启动、停止和清理？

完成形态是一个可亲手操作的纵向原型：**点击菜单栏 → 打开 GPUI → 连接远程 Docker 桌面 → 在同一浏览器中输入/复制/调整窗口 → 断线重连 → 查看、接管和交回 → 关闭窗口后重新打开**。另有本地 backend，验证 App/helper/VM 的生命周期与打包。

长期记忆、任务队列和 session 先参考已有 OpenClaw/Hermes 研究；五引擎完整适配、真实服务 OAuth、完整聊天 UI 和模型效果评测不在本轮关键路径。可用确定性桌面 driver 代替 LLM，以准确定位输入/显示链路问题。模型能力不作为桌面 POC 成败的解释。

## 2. 当前基线不能替代什么

| 已有证据 | 可复用内容 | 本轮仍需验证 |
|---|---|---|
| GPUI 0.2.2 + Swift NSStatusItem/WKWebView | 构建配置、同窗口挂载、C ABI 实验 | 真实点击与焦点、IME、窗口反复开关、Retina/多屏、sleep/wake |
| Apple VM + Chromium + VNC → noVNC | 固定库/内核、显示链路与截图方法 | 真正远程 Docker、网络退化/重连、可用输入、剪贴板与 resize |
| cctl library 示例可启动 VM | Containerization patch、资源与网络经验 | 自有 helper 的 IPC/异常清理、App 相对路径、独立 `.app` |
| 合作式 DesktopGate 测试 | 禁新输入、等待在途、交回约束 | 实际 viewer/自动化两路输入接入后的仲裁；不等于防 shell/CDP 绕过 |
| Codex Linux 基础协议 5 项通过 | 后续 agent 集成起点 | 本轮可以先不接模型，不重测宿主 backfill |

历史代码在 [poc/baseline](../poc/baseline/)，证据边界见 [基线报告](validation/baseline-report.md)。

## 3. 工作分解与依赖

```text
P0 复现与目录规范
  ├─ P1 macOS 原生外壳 ─┐
  └─ P2 远程 Docker GUI ├─ P3 同窗口纵向原型 / 输入控制
                       └─ P4 本地 helper / App 打包
                              ↓
                         技术决策与产品骨架
```

P1、P2 的工作可独立推进，但应尽早连接，不等各自做成完整子系统。默认先 P1，再 P2，接着 P3、P4。P4 先采用用户进程 helper，只有真实权限证据要求时才升级方案。

目录安排（macos-shell 已实现，其余按后续阶段创建）：

```text
poc/macos-shell/        GPUI、Swift native bridge、窗口和事件观测
poc/remote-desktop/     Dockerfile/Compose、Linux GUI、隧道和 driver
poc/native-helper/      Swift executable、IPC、Containerization
poc/scripts/            构建/启动/停止/打包入口
.local/poc/             被忽略的构建、下载、运行状态
docs/validation/       每个阶段的报告与小型证据索引
```

大文件录像、镜像和日志保存在忽略目录，报告记录位置与 hash；纳入 Git 的 evidence 只保留必要小型截图/结构化结果，不能含浏览器账号数据。

## 4. P0：让下一位开发者能复现

**未知项**：现有代码是否依赖原机器的隐式环境，是否能从仓库入口稳定启动？

实施：

- 固定 GPUI/Cargo.lock、noVNC、Containerization commit、kernel/init、桌面镜像版本。已有版本先复用，升级必须有实验原因。
- 将 `/tmp/pot-research-20261003`、编译产物及资源地址改为统一的任务目录参数（例如 `POT_POC_ROOT`，默认仓库 `.local/poc`），Rust、Swift、Python 使用同一配置。
- 增加最小 `doctor/build/run/stop` 脚本：检查架构、SDK、必要工具与端口，不扫描/打印全部用户环境或凭据。
- 为进程、窗口、连接和测试案例分配 ID；日志时间统一，便于关联 focus、输入、RFB 和 helper 事件。
- 已有 Runtime/fixture 测试仅做一次迁入回归，不为文档整理增加无关测试。

交付：可执行说明、版本清单、独立工作目录、缺依赖的明确诊断。

通过：新 checkout 在指定条件下按命令构建并启动已有原生 fixture；源码无需手改用户名/绝对路径。停止后无本次创建的孤立 listener。缓存缺失时提示可执行的下载/构建步骤。

## 5. P1：GPUI + Tray + macOS 原生外壳

**未知项**：原生事件循环、窗口所有权和 WKWebView 能否稳定合作，而不仅是创建出窗口？

实施切片：

1. 复用 GPUI 窗口，Swift bridge 接入同一 NSApplication；明确 NSStatusItem、delegate、WKWebView 与 Rust callbacks 的持有/释放。
2. 建立“菜单栏打开/隐藏 → 关闭窗口 → 再打开”路径。显式 Quit 清理 POC 自有进程；关闭窗口只隐藏/关闭 UI，tray 仍可进入。
3. 处理激活策略、焦点和快捷键。确定 Dock 展示策略，验证 Finder/其他 App 切换后的激活；不要仅调用 showWindow 方法便标记 tray 通过。
4. 将本地输入测试页放进 WKWebView，记录按键、composition 和剪贴板事件；GPUI 输入框与 WebView 来回切换，隔离“原生输入问题”和“远程输入问题”。
5. resize 时同步 WebView bounds，区分 logical point 与物理像素；记录 backingScaleFactor、显示器变更与坐标转换。

实际操作检查：

| 场景 | 验证方式与通过条件 |
|---|---|
| Tray | 人工或真实 UI 自动化点击菜单项打开/隐藏/退出；反复 50 次，无重复窗口、空白窗口、崩溃或失效菜单 |
| Focus | 从其他 App 点击 tray、Cmd+Tab、切换 GPUI/WebView；输入进入预期控件，没有快捷键误投 |
| macOS IME | 拼音组合、候选选择、取消组合、中文/英文混输；测试页最终文本逐字比对，无重复、吞字或拆坏字符 |
| Resize/Retina | 缩放窗口及不同 scale 显示器间移动；图像无裁剪错位，点击坐标正确；没有额外显示器则标记该项未测 |
| Lifecycle | 关闭/重开、显式 Quit、sleep/wake 后 tray 和窗口状态一致，无旧 WebView 回调访问已释放对象 |
| Resources | 记录循环前后内存/线程/句柄，调查单调增长；不预先宣称性能达标 |

交付：`poc/macos-shell/`、真实操作截图/日志、`docs/validation/macos-shell.md`。

决策：若失败来自 bridge 生命周期，先修 ownership/主线程；若 GPUI 与 WebView 在基本 focus/resize/IME 上持续冲突，再做最小原生 AppKit host 对照，以确定问题归属。不要直接用更多模拟检查替代实机结果。

## 6. P2：真正远程的 Docker GUI

**未知项**：离开同机 VM 后，显示、输入和会话在真实网络上是否可用？

环境准备：

- 从 `~/Workspace/Machines` 选择实际可用的远程 Linux Docker 测试节点，确认架构与可用资源。未找到合适机器时说明缺口；不能用同机 VM 冒充远程结果。
- 创建专用、可清理的 POC 容器/网络/volume；不修改已有业务容器。机器地址、连接配置和环境诊断写回 Machines。
- 先用 headed Chromium + Xvfb + 轻量窗口管理器 + x11vnc/WebSocket bridge。固定 image digest；workspace/profile 持久卷分开；浏览器用专用普通 UID。
- 尝试保留 Chromium 自身 sandbox；若环境要求降级，记录具体原因，不能无说明沿用旧 POC 的 `--no-sandbox`。
- 远端端口仅监听 loopback，通过 SSH tunnel 连接；后续直接 WSS 另测认证、TLS、origin 和会话撤销。禁止暴露裸 Docker socket/VNC/CDP。

先用标准 noVNC 浏览器 client 验证 Docker/传输，再接入 GPUI，以便定位失败在哪一层。

检查内容：

| 场景 | 方法与通过条件 |
|---|---|
| 同一桌面 | 浏览器显示 session marker；自动化写入页面、用户立即看见；用户修改后 driver 读到相同值，确认同 DISPLAY/进程/profile |
| 键鼠 | 点击四角、拖动、滚轮、按住/释放、快捷键；事件与目标元素一致；断开时无 stuck key/button |
| 中文 | 本地 IME commit → noVNC → Linux 应用输入框 → 读回精确文本。分别验证按键、组合、Unicode commit 支持；粘贴 fallback 要明确标注，不能冒称原生 IME 通过 |
| 剪贴板 | 双向中英文/多行文本、权限触发和失败反馈；clipboard 与文件传输分开验证 |
| 分辨率 | 验证远端 framebuffer 真 resize 与仅 viewer scale 两种行为；坐标转换和网页重新布局正确 |
| 断线重连 | 杀隧道、恢复网络、重启 viewer；原浏览器/profile 保持，恢复后的控制状态从后端重取，无重放旧输入 |
| 基础持久性 | 容器 restart 与同卷 recreate 后测试文件/profile 标记存在；不测试完整长期记忆算法 |
| 传输成本 | 记录空闲、滚动、文本输入的带宽、CPU、内存、输入到画面延迟和 reconnect 时间 |

网络测量至少包含正常链路与人工增加 RTT/丢包的链路；优先对专用 POC 容器/隧道做 shaping，不能影响整台业务主机网络。记录实际 RTT、分辨率、codec、帧率、机器引用和 p50/p95，不将本地 `ping` 当作端到端显示延迟。

初次建立实测基线后再冻结产品阈值；当前硬条件是输入准确、状态可恢复、无崩溃/串会话。录屏或输入时间戳配合画面可观测标记测延迟，明确时钟误差。

交付：`poc/remote-desktop/` 可重建镜像与启动配置、清理脚本、`docs/validation/remote-docker-gui.md`。

决策：如果标准 noVNC client 就失败，先处理远端显示/编码/网络；标准 client 成功而 WKWebView 失败则进入 P3 定位 WebView。若 VNC 的延迟/中文/剪贴板确有不可接受限制，再针对失败项目对照 WebRTC/成熟远程桌面方案，不先并建两套系统。

## 7. P3：GPUI 内的远程桌面纵向原型

**未知项**：已各自可用的 macOS 外壳和远程桌面组合后，输入、连接与控制权能否保持一致？

实施：

- P1 viewer 接入 P2 隧道，显示明确的 environment/session ID 与连接状态。
- 建立 `disconnected → connecting → viewing → taking_over → human_control → returning → viewing` 状态；错误不隐式恢复 Agent 输入。
- 本轮用确定性 driver 操作同一个 headed 浏览器。所有 driver 输入经过 broker；Take over 拒绝新输入、等待在途收敛，确认后开启用户输入。
- Return control 先禁用户输入，再使 driver 重新读取画面/页面状态；断线保持控制归属，重连先取权威状态，不重放键鼠事件。
- 关闭窗口后环境保持；重新打开回到同一桌面。此处不需要完整聊天/任务系统。

人工验收脚本：

1. 真正点击菜单栏打开 GPUI，连接远程容器；看到 session marker。
2. driver 在网页中填入一段文字，用户在 viewer 中看到；点击接管，确认 driver 停止输入。
3. 中文输入、复制粘贴、滚动并改变窗口大小；读回文本和目标坐标比对。
4. 接管期间断开隧道，再恢复；控制权不被抢回，不出现断线前残留输入。
5. 交回，driver 重新观察并读取用户修改；关闭窗口，再点击 tray 打开，浏览器内容仍在。
6. 录制完整过程并保存事件轨迹；失败具体标记为 macOS、viewer、传输、远端显示或 broker 层。

交付：一个可亲手操作的可运行 demo、`docs/validation/integrated-desktop.md`。通过后才能写“GPUI + 远程 Docker GUI 基本可用”。协议 fixture 和调用方法不能替代本阶段。

本阶段的接管只承诺 broker 管理的路径；之后若加入任意 shell/CDP 工具，必须将它们放进受控权限边界并单独验证，不能扩大 POC 结论。

## 8. P4：本地 Swift helper 与 App 打包

**未知项**：从开发用 cctl 过渡到由应用管理的 helper，进程、权限、资源和打包是否成立？

实施：

1. 自有 Swift executable 链接固定 Containerization，提供 Unix socket IPC。先实现 prepare/start/status/stop，带 request ID、明确错误和 ready/exited 事件。
2. GPUI 启动 helper，等待 readiness 后请求创建 VM；启动超时可取消，重复请求不生成多个环境。
3. helper 拥有 VM/network/共享卷句柄；定义正常 stop、GUI Quit、helper crash、guest crash 的清理与恢复。只处理有本次 POC ID 的资源。
4. 用相同 GUI 工作负载验证本地 viewer；本地与 remote backend 共用界面状态，后端配置归沙箱设置。
5. 组装 `.app`：相对 bundle 定位 helper/动态库/资源，固定 entitlement 与代码签名顺序；把 `.app` 移到另一目录后独立运行，开发 shell 的 PATH/缓存不应成为隐式依赖。
6. 首轮 ad hoc 签名可验证本机结构；Developer ID/notarization 和干净机器 Gatekeeper 验收须标成独立分发门槛，不能因本机可运行便宣称可公开发布。

故障注入：启动一半停止、kill helper、guest 意外退出、重复 start/stop、缺 kernel/init、下载中断、数据目录无写权限。UI 必须反馈确定结果，失败后再次启动可恢复，不遗留不受管理的 VM/listener。

交付：`poc/native-helper/`、本地 `.app` 构建脚本、`docs/validation/native-helper-bundle.md`。

决策：若权限需要 privileged helper，以实际失败/所需 entitlement 作依据；如果库能力或 App 生命周期不足，再评估官方后台服务包装的备选，并说明它对“用户无需自行安装 Docker”目标的影响。

## 9. 证据格式与最终交付

每阶段报告至少包含：待验证问题、固定版本/机器引用、启动与清理步骤、实际操作记录、结构化结果、失败诊断、结论可覆盖范围。状态仅用 `未测 / 通过 / 失败 / 有限通过`；有限通过必须列出缺口。

| 最终结论 | 必须有的证据 |
|---|---|
| GPUI/macOS 集成可用 | 实际 tray 操作、focus/IME/resize/lifecycle 的记录，而非仅调用 Swift 方法 |
| 远程 Docker GUI 可用 | 真实远程节点、网络条件、输入/画面/重连记录，而非本地 VM 或合成 framebuffer |
| 纵向原型可用 | 同窗口从打开到接管/交回/重连/重开的连续演示 |
| 本地 runtime 可嵌入 | 自有 helper 的真实 IPC/VM 生命周期与独立 App 运行 |

完成后写技术决策：保留 GPUI/WKWebView/noVNC 还是替换具体组件，保留的限制是什么，哪些接口可进入产品骨架。不要把尚未解决的桥接问题留给完整产品开发后再发现。

## 10. 第一项可直接执行的工作

从 **P0 + P1 最小切片** 开始：新建 `poc/macos-shell`，复用现有 GPUI/Swift bridge，消除硬编码路径，取消实验程序的自动退出，加入真实 tray 打开/关闭和事件日志；先完成“点击 tray → 输入测试页 → 关闭窗口 → 再打开 → 显式退出”。同时确认 P2 可用远程 Docker 节点，随后连接实际远端桌面。

### 2026-10-03 执行进度

- P0：有限通过。macos-shell 具备 doctor/build/run/status/stop、锁定依赖、任务根目录和嵌入资源；默认目录构建/运行成功，29 项 baseline fixture 回归通过。全新机器/冷 registry 未测。
- P1：有限通过。用户确认真实 tray 关闭后重开；真实 UI 操作验证 GPUI/WebView 焦点、拼音提交/取消、导航键、单屏 2× 缩放和应用菜单 Quit。修复键路由、firstResponder、Edit 菜单和重复运行签名问题。
- P1 未测：最终构建 50 次 tray 循环、资源趋势、多屏、sleep/wake、完整输入法与快捷键矩阵；不能将最小切片等同完整阶段通过。
- P2：只读确认 Machines/devpc 的 SSH/Docker 可用，资源余量尚不满足桌面预算，环境事实写回 Machines；未改现有服务。
- P3/P4：未测；本轮开发 .app 不代表 helper/VM 或可分发 App 已完成。

### 后续范围收敛（用户决定）

GPUI/tray 最小验证足够，到此收尾。50 次循环、多屏、sleep/wake、资源趋势和完整输入矩阵保留为未测边界，不作为本轮待办或 P2 前置条件；后续集成出现具体问题时再针对性复查。

P2 初查内存不足后，按用户授权停止了 devpc 上 llama-server 对应的用户服务。复查可用内存已满足最小桌面预算，Docker 正常；操作记录、资源数值和恢复命令已写回 Machines。下一步确认镜像/卷的磁盘预算并实施远程桌面切片。
