---
tags: [Agent, Architecture, POC, pot]
---
# pot 产品架构研究与 POC 验证

> 旧 `poc/` 源码已移除，下方实验命令仅适用于链接中的历史提交。

> 历史验收记录：版本、额度与界面均以各节日期为准。当前能力见 [开发计划](../development-plan.md)，操作规范见 [文档索引](../README.md)。

日期：2026-10-03。对应 [本地个人Agent工作环境产品设计](../product-design.md) v0.11。代码与证据：[POC 复现说明](baseline-reproduction.md)。

本次完成官方文档研究、原生界面/虚拟机实验、五种引擎的真实基础程序化接入及确定性 Runtime 测试。**这些结果证明基础技术路线可继续实施，不代表完整 pot 产品、五引擎执行或 Dot 实机一致性已经验收。** 未调用真实模型，未读取或修改用户账号凭据，也没有连接真实服务账号；复验增加本地固定模型响应 fixture，用实际 Pi 执行工具、保存/恢复 session、取消和 steering。

> 此文记录截至 2026-10-03 的历史实验。文中“剩余验收”包含未来产品验收，不代表当前开发优先级；当前 POC 顺序以 [POC 实施计划](../poc-plan.md) 为准。证据位于 [poc/baseline/evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline/evidence)，文中未展开的 evidence 文件名均相对此目录。

## 1. 结论与架构决定

| 问题 | 本次结论 | 实施决定 |
|---|---|---|
| GPUI 能否结合菜单栏和桌面 viewer？ | GPUI 0.2.2 + Swift NSStatusItem + 同窗口 WKWebView 已运行；真实 noVNC 可连接和发送键事件 | 采用 Rust GPUI 客户端 + 小型 Swift native bridge；viewer 先用 WKWebView/noVNC |
| 能否不要求用户安装 Docker？ | 直接编译 Apple Containerization 0.47.0 示例，真实 ARM64 VM 启动、共享目录跨 recreate、网络通过 | 本地走 Swift helper/library；PoC 的 cctl 是库示例验证载体，不是最终产品 helper |
| agy 是什么、是否可嵌入？ | 用户确认 Google Antigravity；官方 Python SDK 0.1.20 实际启动、cancel、退出通过 | 优先 SDK adapter；CLI stream-json 不能照搬 Claude control 协议 |
| 五引擎是否共用协议？ | Pi RPC、OpenCode HTTP、Claude SDK 控制握手、AGY SDK 均不同；Codex 在全新 Linux VM 中完成 App Server 基础协议验证；宿主 backfill 不再阻塞此路线 | 产品协议稳定，内部逐引擎 adapter 与 capability 协商；不以单一 ACP 覆盖全部差异 |
| 长期状态放在哪里？ | SQLite Runtime 崩溃恢复/事件/撤销/代次测试通过；未做模型记忆质量评测 | 首版采用最小 C：产品持有明确偏好、任务、计划和授权事实；provider 持有原生上下文 |
| 一个 flag 能否保证接管？ | 合作式 broker 竞态测试通过，但 shell 绕过未被其阻止；真实不同 UID 的受保护文件不可读 | controller/desktop 与 Agent 分开权限域，flag 在受控入口实施；不能让 Agent root、访问显示 socket 或 CDP |
| 登录是否能统一？ | Claude 与 Codex 的第三方认证约束不同 | Setup 分引擎展示合法可用认证；模型认证与 GitHub/Gmail 服务授权分开 |

## 2. 证据等级与已完成 POC

“真实验证”使用实际原生程序/虚拟机/引擎；“fixture 验证”使用本地受控模拟服务；“源码/文档依据”不等于运行通过。所有限制随证据保留。

### 2.1 原生界面和显示链路

真实构建 GPUI 0.2.2；同一进程通过 Swift bridge 找到 GPUI NSWindow，创建 NSStatusItem，并把 WKWebView 放入该窗口。真实 noVNC v1.6.0 对 RFB 3.8 fixture 完成握手、显示画面、发送 a 键按下/释放。WKWebView 截图已保存并检查；隐藏窗口后调用 tray action 可重新打开同一窗口。

证据：`poc/baseline/evidence/native-ui.json`、`rfb-events.json`、`native-viewer.png`。这里的彩色 framebuffer 是 fixture，不能称为 Linux 桌面。tray 验证调用 action 方法，尚未通过真实鼠标点击菜单验收。

进一步完成真实链路：Apple library VM → Xvfb → headed Chromium → x11vnc → loopback WebSocket proxy → 同一 GPUI 窗口 WKWebView/noVNC。已检查 `native-vm-viewer.png`，显示真实浏览器与中文页面；键事件到达 VNC server，Ctrl+L 使浏览器地址栏被选中。证据为 `native-vm-ui.json`、`rfb-vm-events.json` 和 `linux-vnc.txt`。Chromium POC 使用 `--no-sandbox`，guest 中 dbus/GPU 有诊断警告，不能将此配置直接用作 hardened 产品镜像。

GPUI 默认构建曾缺少离线 Metal Toolchain；开启 `runtime_shaders` 后构建成功。此选择属于开发/打包路线，不据此宣称 GPU 性能或发行兼容已通过。未验收窗口缩放、中文 IME、剪贴板、多显示器和 Accessibility。

### 2.2 Apple Containerization

固定 library 0.47.0（commit `bc994b88df46207fad7775b0eabc51947e315881`），修改 upstream cctl 示例仅使用临时状态目录、固定 init image，并用显式环境开关启用网络。示例经 virtualization entitlement ad hoc 签名运行；没有安装全局 Apple container 服务，没有启动 Docker/OrbStack。

真实通过：

1. Alpine 3.22 ARM64 VM 启动，guest `uname -m` 为 aarch64。
2. VM 写 `/data/proof.txt` 后退出；新 VM 挂载同一宿主目录读到同一内容，输出 `PERSISTENCE_PASS`。
3. controller 目录 0700、文件 0600；新建普通 `potagent` UID 读取失败，输出 `SEPARATE_UID_BOUNDARY_PASS`。
4. VmnetNetwork 获得 eth0 地址，指定 DNS 后通过 HTTPS 下载 Alpine 软件索引，输出 `NETWORK_PASS`。
5. 安装 Xvfb/Chromium/x11vnc/中文字体，普通 desktop UID 运行 headed 浏览器；原生 viewer 显示真实桌面并发送键事件。结构化证据见 `vm-results.json`。

kernel 来自 Kata 3.32.0 官方 ARM64 release。下载 archive SHA256 为 `8736c054d9223974735394f822000823baef509e1c33405ec798240fa9b6e4b5`；实际 kernel SHA256 为 `fb2cfb79eb1ae19447a85d75682d7fa5cfec97e24beb2609a492b806e8072c8d`；init 为 `ghcr.io/apple/containerization/vminit:0.47.0`。

共享目录持久性不代表整个 rootfs 持久。cctl 删除临时 guest 状态后，apk 安装不会自动带到新 VM。产品应交付固定 GUI 基础镜像，将工作区、浏览器 profile、provider 状态和可重建安装 manifest 分别持久化。

不同 UID 文件权限只是权限域的初步实证，尚未证明所有 X11/CDP/shell 绕过都被阻止。VNC 演示的无密码配置只能用于临时受控 POC，产品必须通过受认证 broker 转发，不开放裸 VNC。

### 2.3 引擎接入：真实握手与未覆盖能力

| 引擎/固定版本 | 实际执行 | 尚未执行 | 推荐 adapter |
|---|---|---|---|
| Codex 0.159.3 | 全新 Linux VM：initialize、重复初始化拒绝、ephemeral thread start/read、无活跃 turn 的 interrupt 拒绝；5/5 通过 | 带真实模型 turn/resume/steer/interrupt/compact、工具沙箱 | 直接 App Server，先解决干净部署状态；ACP 留作外部兼容 |
| Claude Agent SDK 0.3.288 | SDK native CLI 的 stream-json `initialize` 控制握手 | API-key 模型 turn、权限回调、执行中取消、resume | 官方 SDK；PoC 原始协议用于诊断 |
| Pi 0.87.1 | RPC 管理能力；新增本地模型 fixture 下实际 write 工具、文件持久化、进程重启恢复、执行中 abort/steer | 真实模型/provider 下的能力与质量 | RPC；当前包为 `@earendil-works/pi-coding-agent` |
| OpenCode 1.18.34 | 实际 HTTP health、创建/读取/abort/删除 session，OpenAPI 162 个 path | prompt/SSE、实际工具审批、执行中取消与恢复 | Server HTTP + SSE / SDK |
| Google Antigravity SDK 0.1.20 | Agent 启动、connection.cancel、正常关闭；明确 deny-all policies | 有效凭据下的 chat、history/resume、工具审批和实际任务 | Python SDK sidecar；固定 SDK 与 runtime 版本 |

证据：`engine-probes.json`、`agy-sdk.json`。初轮 abort/cancel 只在空闲状态通过；复验补充了 Pi 在本地模型 fixture 的执行中停止，其他引擎的执行中停止仍未验收。初轮 Pi 无 turn 时 session 文件未物化；复验使用固定模型响应驱动真实 Pi 工具循环，文件已物化，并通过真实 Pi 进程重启恢复。此结论只覆盖本地 fixture 场景，仍未验证真实 provider。AGY 初始化创建状态文件，但 conversation_id 仍 null，不能标记会话恢复已通过。

此前宿主 Codex 阻塞的诊断：即使 `sqlite_home` 指向临时目录，初始化仍等待已有 `~/.codex` state backfill；30 秒后超时并退出。sandbox 内及批准后的独立启动均遇到同一问题。未删除锁、重置数据库或改动正在使用的用户状态。`codex-backfill.txt` 留有脱敏诊断；这属于本机验证阻塞，不足以证明 App Server 产品不可用。

AGY 官方 CLI 文档的 stream-json 用 `event` 字段，明确不支持 Claude control_request/control_response。官方 Linux ARM64 manifest 已查到 CLI 1.2.15，但本次 SDK 和其他引擎握手运行在 Mac；**Codex 基础协议现已在 Linux VM 验证，其余引擎在产品 Linux 镜像内运行仍须独立验收**。

### 2.4 产品 Runtime：22 个测试

`runtime.py` 和 `test_runtime.py` 使用真实 SQLite、子进程崩溃与线程协调，原有 22/22 通过；另增加 4 项反向测试，并修复其发现的 3 个缺陷。原测试覆盖：

- 事务提交前/后进程退出，重启读取与事件投递去重。
- outbox 持久化、迟到 worker 结果被 run epoch fencing 拒绝。
- 外部操作 unknown-outcome 不被直接标成功或盲目重试。
- 明确偏好更正/遗忘 tombstone，旧 binding 失效与新 epoch。
- 计划错过执行的 coalesce/skip，重启继续保存责任。
- account/scope、凭据撤销与动作规则检查。
- 接管先关闭输入入口并等待在途操作，人工控制跨重启保留。

这是最小确定性实现的验证，不是完整生产 Runtime，不含分布式一致性、LLM 判断 reviewer、自动语义记忆或 provider 真实 worker 回流。

选择最小 C 的理由：授权、计划和任务事实必须有产品权威；不能由模型摘要决定是否已发送或有权限。语义记忆策略 A/C 的质量、成本与长期误记忆尚未比较，不宣称 C 的模型效果已胜出。

### 2.5 OAuth fixture：3 个测试

真实本地 loopback HTTP callback 验证 state、PKCE、单次使用/过期、账号 scope 与撤销，3/3 通过。使用本地 fixture token issuer；**没有注册 GitHub OAuth App，也没有真正交换 GitHub token**。这验证接入机制，不验证服务平台审批、真实账户授权或撤销传播。

## 3. 官方研究对产品的修正

### 3.1 Dot：行为基线已研究，像素/动效仍需实机

官方 Getting started、Tasks and memory、Controls、Computers and apps、Messaging 支持以下基线：主聊天与任务 threads、Activity/Outputs、Scheduled、追加指导、连接服务、动作 review、Custom Rules、查看/Take over/Return control。打开桌面不等于接管；暂停主任务、停止委托、关闭计划是不同作用域。

Custom Rules 是 Dot 尝试遵守的用户指令，不应描述为操作系统硬隔离或范围授权。产品若需要可执行的限制，须另在工具入口和账号 scope 实施。

用户确定 pot 的流程为先 Setup agent，再聊天询问命名，默认 Pot。Dot 文档支持个性化，但没有证明固定“先问名字”的完整顺序；这段是用户需求，不能冒称 Dot 已核实行为。Dot 已登录实机访问问题仍待用户回复；没有擅自读取用户登录界面。

### 3.2 Setup 认证需要区分分发方式

[Claude Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview) 写明：未经批准，第三方不能提供 claude.ai login 或订阅 rate limits，应使用 Quickstart 的 API-key authentication。因此 claude 默认 API key；不能把已装 Claude CLI 的订阅登录当作产品自动继承的合法方案。

[Codex App Server Auth endpoints](https://developers.openai.com/codex/app-server) 写明：已有 local/open-source app-server authentication 可以继续，推荐迁移 Sign in with ChatGPT；commercial/hosted services 从未被允许使用该认证，Sign in with ChatGPT 用于支持这些情形。API key 与 Sign in with ChatGPT 方案应按实际分发条件核对；商业形态不能直接依赖 App Server ChatGPT OAuth。技术上能握手不等于有分发授权。

Pi/OpenCode 随模型 provider 配置，AGY SDK 有 api_key；各认证方案都需要真正低风险模型 turn 验证。五项 engine 可进入 Setup 支持目标，但未通过所需能力的 adapter 不应标记“就绪”。

## 4. 剩余验收与阻塞

| 优先级 | 工作 | 本次为何没有完成/所需条件 | 通过标准 |
|---|---|---|---|
| P0 | Dot 实机一致性 | 需用户有 Dot 并授权检查已登录界面，问题已发出 | 截图/流程对照，主/任务/Activity/Outputs/Scheduled/review/接管与开场覆盖 |
| P0 | 五引擎实际模型与 Linux adapter | 本次未使用 POC 专用模型凭据；Codex 启动已通过全新 guest 绕开宿主状态验证 | Linux 镜像各自执行任务、resume、执行中 stop、steer/follow-up、权限回调实测 |
| P0 | hardened desktop 仲裁 | UID 文件权限与合作式 broker 不是全部保护 | agent UID 无显示/CDP/VNC/socket 直连能力；shell/worker 绕过负测，在途输入收敛 |
| P0 | 可分发安装包 | 本次只 ad hoc 签名运行 | Developer ID/notarization、helper 更新、必要 entitlement、冷机器安装/重启/卸载 |
| P1 | 工具/profile 升级保存 | 已证共享工作区 recreate，未完成升级矩阵 | GUI 镜像升级、manifest 重建、profile 可用、失败回滚与 schema 恢复 |
| P1 | GitHub 服务闭环 | 本地 OAuth fixture 不含真实 App registration/token | 专用测试账号、最小 scope、真实回调/撤销、未知写结果核实 |
| P1 | 中文输入与性能 | 字体显示不等于 IME；未定性能基线 | 中文输入/剪贴板/resize，冷启动、下载体积、内存、桌面延迟、接管/取消残留测量 |
| P1 | A/C 语义记忆比较 | 无模型 turn 与长期评价数据 | 固定案例、成本/准确率/更正/遗忘反学回对照；再决定自动语义记忆 |
| P1 | 后台生命周期 | 计划逻辑已测，尚未接 macOS 生命周期 | sleep/wake/退出/预算/通知；关机不虚报执行 |
| P2 | Remote Docker/多节点 | 首版不要求此闭环 | 配对/撤销/环境绑定；不声称本地 library 即 Docker API |

这些项不是又一轮泛化研究题：基础协议与技术可行性已经有证据，剩余主要是凭据、真实 UI、硬隔离、打包及真实任务的产品验收。不能用更多 fixture 把这些标为通过。

## 5. 独立复验与实际修复（2026-10-03）

用户要求再次验证后，重新运行 Runtime/OAuth 测试、四引擎握手与 AGY 生命周期，并重启新的 Apple VM 核实此前文件、UID 文件保护和网络。均通过；Codex initialize 仍失败，原因仍为用户目录 state backfill 超时。真实本地模型服务 8080 当前不可达（connection refused），未借用其他账号或额度。

新增 `test_adversarial.py` 最初 4 项中 3 项失败，证据 `adversarial-before.txt`；已修复后 4/4 通过，证据 `adversarial-after.txt`：

1. `return_control` 在旧 Agent 输入仍在途时原本会重新启用输入。现在拒绝提前交回，直到在途归零；避免将“交接中”显示成可正常交回。
2. `review` 对未知规则原本默认放行（包括拼写错误）。现在使用显式允许列表，未知值拒绝。
3. `review` 对未知动作原本也默认放行。现在未知动作拒绝，扩展动作需显式纳入能力/规则定义。

原 22 项 + 新增 4 项 + OAuth 3 项，合计 **29 项通过**。这次发现说明测试数量本身不能保证边界正确。原“idempotent consumer”测试仅用内存 set 去重，证明 event ID 可去重，不是完整持久消费者实现；首版仍须实现并验收消费者确认与副作用同事务/可恢复的路径。

进一步写了 `pi_turn_fixture.py`：临时 loopback OpenAI SSE fixture 发出确定的 write tool call，实际 Pi 写出 proof.txt、生成 session JSONL，终止 Pi 进程后用同一 session 重启，5 条消息一致恢复；另在未结束的 SSE 中发 abort，返回 idle；在执行中发 steer，下一次 fixture 请求实际包含追加指导。没有调用真实模型，也不将固定回复当作理解/记忆质量证据。见 `pi-turn-fixture.json`。

结构化复验汇总 `reverification.json`，VM 输出 `vm-reverification.txt`。尚需真实模型凭据与 Dot 实机访问的验证已询问用户，等待其指定，未将等待结果视为许可。

## 6. Codex 全新 Linux VM 验证（2026-10-03）

按用户指定改用容器中的新实例，不再尝试修改宿主 backfill。使用 Apple Containerization 新建 Alpine 3.22 ARM64 VM，只共享 POC 目录；guest 的 `/root/.codex` 是新环境默认 home，没有挂载宿主 `~/.codex`，也没有改写 HOME/CODEX_HOME。

官方 `rust-v0.159.3/codex-aarch64-unknown-linux-musl.tar.gz` SHA256 与 GitHub release digest 一致：`cd5f307b3fcd6080773e684b86c3114a67d4f1c61dc447be09876b552eb4bea7`。实际运行 `codex-cli 0.159.3`，App Server 返回 Alpine Linux 3.22.6/aarch64。

五项真实检查通过：initialize；重复 initialize 返回 Already initialized；ephemeral thread/start；thread/read 返回同一 thread 且 idle；对不存在 active turn 的 turn/interrupt 返回 no active turn to interrupt。最后一项是错误路径检查，不能称为执行中取消通过。

源码 `poc/baseline/guest/codex_app_server_probe.py`，证据 `codex-linux-app-server.json`，固定 release URL/hash 为 `codex-linux-release.json`。VM 退出并完成清理。没有发送 turn/start、访问真实模型或挂载认证数据。

这一结果证明干净 guest 的基础 App Server 接入可用，宿主状态不是架构阻塞。它不能证明宿主“正在运行 App”就是 backfill 超时的原因，也不能证明无需登录便可执行模型任务。stderr 提示未在 PATH 找到 bubblewrap，并声明使用 bundled fallback；完整工具沙箱仍应固定 bubblewrap 及权限配置并独立测试。

## 7. 来源与固定版本

在线文档读取日期为 2026-10-03；sources manifest 记录 URL 和已获取文件哈希。上游网页会变动，版本化证据以锁文件、commit、release 和本次快照哈希为准。

- [Dot Getting started](https://learn.chatgpt.com/docs/dots/getting-started)、[Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory)、[Controls](https://learn.chatgpt.com/docs/dots/controls)、[Computers and apps](https://learn.chatgpt.com/docs/dots/computers-and-apps)、[Messaging](https://learn.chatgpt.com/docs/dots/channels)。
- [Codex App Server](https://developers.openai.com/codex/app-server)、[固定源码](https://github.com/openai/codex/tree/rust-v0.159.3)、[Sign in with ChatGPT](https://developers.openai.com/siwc)。
- [Claude SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)、[permissions](https://code.claude.com/docs/en/agent-sdk/permissions)。
- [OpenCode server](https://opencode.ai/docs/server/)、[SDK](https://opencode.ai/docs/sdk/)。Pi 协议使用实际安装 0.87.1 包的 docs/rpc.md；不依赖旧仓库名称推断。
- [AGY SDK overview](https://antigravity.google/docs/sdk/overview/)、[policies](https://antigravity.google/docs/sdk/policies/)、[lifecycle](https://antigravity.google/docs/sdk/lifecycle/)、[CLI headless](https://antigravity.google/docs/cli/headless/)。
- [Containerization 0.47.0](https://github.com/apple/containerization/tree/0.47.0)、[container 1.5.0](https://github.com/apple/container/releases/tag/1.5.0)、[Kata 3.32.0](https://github.com/kata-containers/kata-containers/releases/tag/3.32.0)。
- [GPUI 0.2.2](https://crates.io/crates/gpui/0.2.2)、[noVNC v1.6.0](https://github.com/novnc/noVNC/tree/v1.6.0)。

版权/发行审核：本次实际 gpui 0.2.2 crate 声明 Apache-2.0；noVNC MPL-2.0、Apple library Apache-2.0 等依赖及转发资源应列入 NOTICE/SBOM，并按实际交付文件逐项核对。技术 PoC 没有解决产品许可证与发行授权问题。
