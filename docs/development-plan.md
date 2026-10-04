# pot 产品开发计划

更新：2026-10-03（v3，用户收敛首个 MVP 为 Pi + DeepSeek）。状态：D1 Pi MVP 已完成；P0–P4 是已有技术 POC，不计作本计划完成。设计与验收以 [产品设计](product-design.md) 为准。

## 1. 范围与实施原则

首个产品闭环使用一个本地环境、一个控制客户端和 Pi + DeepSeek；AGY、Codex ACP、Claude、OpenCode 后移，五引擎目标不变。远程多节点不纳入首个闭环。

- **先纵向、后加固**：最大的未验证风险是“GPUI 聊天 → VM 内 Rust Runtime → Pi + DeepSeek 真实模型与工具”这条链路，而不是签名或全通道负测。先用最短路径让它真实跑通，再按场景逐层加固。
- **门槛跟随场景**：每项硬约束放在它第一次能被真实测试、或第一次被需要的阶段，不再作为进入开发前的整体前置门槛。
- **从 POC 提升而非重写**：`poc/macos-shell`、`poc/native-helper`、broker/viewer 的已验证代码迁入正式目录后继续演进；`poc/` 保留为回归依据，不再新增功能。
- 长期状态与会话机制参考现有 OpenClaw/Hermes 研究，不另起记忆算法研究。

## 2. 已固定的决策（2026-10-03 用户确认）

| 决策 | 采用方案 | 理由 |
|---|---|---|
| 仓库结构 | `app/`（Rust GPUI + Swift bridge）、`helper/`（Swift + Containerization）、`runtime/`（VM 内产品 Runtime）、`image/`（guest 镜像构建）、`protocol/`（Control API/事件 schema） | 与架构组件一一对应；POC 目录冻结 |
| Runtime 语言 | Rust（guest 内）+ SQLite | 与客户端共享 protocol crate；引擎通过协议或外部进程接入 |
| Host↔Runtime 通道 | D1 用 helper Unix IPC 发现 VM 地址，GUI 经 bearer token 访问 VM 私网 HTTP；Runtime 提供 SSE，GUI 消费事件并在断线后重新同步快照 | 复用 P4；本机代理尚未实现 |
| 首个引擎与认证 | Pi RPC + DeepSeek API key（从本机 Pi 定向导入） | 用户最新决定以 Pi 自带工具推进 MVP；允许必要的真实 API 验证，尽量节省；AGY/Codex 不再阻塞 MVP |

用户没有 Developer ID，先跳过发行签名和公证；本地开发包仍使用运行虚拟化所需的 ad hoc 签名。Pi 原生会话在 guest 共享数据目录持久化；AGY 的 Google 登录留待该引擎接入时验证。

## 3. 开发阶段

每阶段交付可操作版本及简短验收记录；失败项不以 fixture 替代。

| 阶段 | 实现工作 | 完成标准 |
|---|---|---|
| **D1 纵向骨架** | 迁入 app/helper；guest 镜像加入 Rust Runtime、Node/Pi；Runtime 最小对象（conversation/message、event+cursor）与 SQLite；Control API/SSE；GPUI 原生聊天视图；Pi RPC adapter（工具/流式事件/取消/原生会话恢复）；顺带修复 Chromium 干净退出（等浏览器关闭确认再停 VM） | 打开 App → 环境就绪 → 主聊天发消息 → Pi + DeepSeek 真实模型流式回复，并在沙箱内真实执行一次工具（写文件）→ Quit 后重开，对话与 thread binding 保留并可继续；profile 无 Crashed 标记 |
| **D2 Setup 与引导** | 引擎选择页（仅已验收引擎可用）、账号登录与真实校验；镜像/资源下载进度、取消、中断续传与重试；聊天内命名/跳过与引导进度持久化 | 干净数据目录：安装 → Setup → 主聊天走通；断网/取消后从原步骤恢复，不重复已完成下载；重开保留名称、引导进度和有效认证；未就绪能力不可进入执行 |
| **D3 报告任务（S1）** | task/run/binding、主/任务聊天、附件、任务卡、Activity、Outputs；追加指导（已接收/已应用）、取消收敛、产物存储、outbox 去重、重启核实；持久明确偏好 | S1 全流程通过；后台任务期间主聊天可交流；追加成本要求进入结果；停止确认后无后续工具调用；重启不丢产物、不重复回报 |
| **D4 网页接管（S2）** | 浏览器任务接入 broker；确定 Agent/controller 权限域（独立 UID、Agent 不可达 CDP/X11/VNC socket）；桌面使用权排队、人工控制、观察暂停、交回重读；断线状态与紧急停止 | S2 全流程通过；以真实 Pi 进程做 shell/CDP/X11/截图绕过负测；中文登录输入（至少 Clipboard 路径）与必要快捷键可用；断线重连保持控制归属 |
| **D5 持久计划（S3）** | 时间计划 CRUD、时区、去重、合并补跑/跳过、禁止重叠、Scheduled 界面；来源基线、变化证据、通知条件 | S3 全流程通过；真实 sleep/wake 与重启恢复；无变化静默，重要变化一次通知；关闭计划不误停当次运行 |
| **D6 覆盖与交付** | 逐个接入 AGY/Codex ACP/Claude/OpenCode；最小记忆更正/遗忘；GitHub 授权/撤销与 action review；升级/恢复；Developer ID 签名、公证、冷安装 | 每个标为可用的引擎通过 S1–S3 及停止/恢复检查；撤销后能力真实失效；批准内容不可替换；升级保留数据；发行包在干净机器通过 Gatekeeper 与冷安装 |

### 并行轨道（不阻塞 D1–D5）

- **签名与公证**：当前没有 Developer ID，暂缓；取得资格后用最小 App 打通 notarization 管道；资格未具备时标为阻塞，只阻塞 D6 发行，不阻塞开发。
- **Dot 实机核对**：功能先按产品设计实现，布局/文案/动效在有实机条件时逐项对齐。
- **远端能力**：远端 IME、resize、弱网在承诺远程产品能力前验收；不恢复泛化 tray/多屏测试，只针对新集成暴露的问题补验。

## 4. D1 要固定的最小实现契约

- **产品对象**：D1 只实现 conversation/message、execution binding、event；task/run、artifact、operation、outbox 在 D3 加入，schedule 在 D5，integration 在 D6。schema 从第一天起版本化迁移。
- **命令与恢复**：命令携带 request ID 和目标 ID；事件携带 event ID/cursor；重连先取快照再续传。状态变更与待投递事件同事务提交，取消必须等待执行端确认。
- **引擎能力**：adapter 声明认证、真实工具、指导、停止、恢复及上下文能力；不支持即时指导时排入下一轮，不支持原生恢复时使用可核实的恢复包；产品历史不伪装成原生 thread 迁移。
- **测试方式**：Runtime 协议/状态用自动化测试（可用模拟 adapter 覆盖竞态与去重）；“完成标准”中的端到端项必须用真实 App、真实 VM 与真实模型。

## 5. 验证与进度记录

每阶段在本文件末尾追加：版本/提交、入口、实际用户操作、结果与未通过项，链接必要证据；不复制设计正文。状态仅使用未开始、进行中、阻塞、已完成，已有 POC 的“有限通过”结论保留在原报告。

仅修改文档不运行 VM；不以重复历史 29 项 fixture 代替产品验收。

## 6. 进度

| 阶段 | 状态 | 记录 |
|---|---|---|
| 决策（第 2 节） | 已完成 | Rust、正式目录、Pi + DeepSeek 先行，其余引擎后移、暂缓发行签名 |
| D1 | 已完成 | Pi + DeepSeek MVP 真实工具、取消、Quit 重开语义续聊通过；见 [验收记录](validation/pi-mvp.md)。D2–D6 尚未交付 |

### D1 本轮记录（2026-10-03，未提交）

- 产品入口：`scripts/pot prepare|build|bundle|open`；开发包 `.local/d1/pot.app`。`open` 默认使用专用 `.local/d1/local-runtime`，支持 `POT_LOCAL_ROOT` 覆盖；直接双击 App 使用 `~/Library/Application Support/pot`，两者不是同一数据目录。
- 已构建并验证 Rust GPUI App、Swift helper、ARM64 musl Rust Runtime 和本机 ad hoc bundle 签名。AGY 固定官方 CLI 1.2.16 musl 包并校验 SHA512；新增登录依赖已按 guest 实装版本锁定。
- 3 项协议/存储测试通过：事件序列化、重启保留 binding/部分输出并标记中断、请求重试去重/冲突/并发拒绝/未授权拒绝。不是模型端到端验收。
- 真实本地 VM：Control API 可达，AGY 真实进程能启动。新 VM 未登录；已识别真实 `Authentication required` 输出，修复后约 0.54 秒返回失败、清空活跃 turn 并保持 `needs_auth`，没有伪造模型回复。
- 真实 helper shutdown：CDP `Browser.close` 后等待浏览器进程退出，`browser.shutdown.confirmed=true`、profile `exit_type=Normal`。未通过直接改写 profile 伪造结果。原生 App 重开同一数据后，之前的消息仍在；中断消息显示 Failed。
- 原生 UI 已打开并核对聊天记录和菜单栏「登录 AGY」：可打开 guest xterm，显示 Google OAuth 登录选项。桌面已切为人工接管，等待用户在 guest 完成登录；没有读取或复制宿主 Keychain 凭据。
- 尚未验收：真实模型流式回复、真实写文件工具、认证持久化和原生 conversation 的 Quit/reopen 续接。Codex ACP 尚未实现；GUI 暂用快照轮询，SSE 服务端已提供。AGY CLI 取消接口明确返回 501，执行中停止/子进程收敛待后续验证。
- 诊断日志和数据库仅保留在忽略目录 `.local/d1/`，不提交登录 URL、token、profile 或用户会话。

### D1 API 验证补充（2026-10-03，未提交）

- 用户授权从本机 Pi 配置读取 DeepSeek key，并要求节省用量。配置里的模型 ID 为 `deepseek-v4-flash`，官方地址 `https://api.deepseek.com`。仅导入此 provider 的凭据到开发 guest 私有文件，没有复制 Pi 全量配置或写进源码/App 包。
- Rust 增加受限 Chat Completions 流式适配器：最多 3 次请求、每次 128 输出 token、关闭 thinking 和 HTTP 重试、单次任务至多两轮。工具仅能在 guest workspace 新建小文本文件；路径越界、覆盖和已有符号链接被拒绝。这是 API 骨架验证，不是完整 Pi/AGY 引擎替代。
- 实际通过：原生聊天输入并发送 → guest Runtime → DeepSeek 请求 → 真实 `write_file` → `d1-api-check.txt` 内容 `POT_D1_OK` → 简短模型回复。此任务用了 2 次请求。
- 重启暴露共享目录上 SQLite WAL 新状态丢失。改为 DELETE journal + synchronous=EXTRA，并增加独立 fsync 请求额度记录；先以零额外 API 请求做重启测试，再使用最后一次请求得到 `API_OK`。随后原生 Quit／重开，真实回复、API messages 历史和 3 次额度均保留，Chromium profile 为 `Normal`。不再从宿主直接打开运行中的 guest SQLite。
- 发现并修复 GUI Quit 的 SIGTERM 与父进程 watchdog 同时发起 stop 的竞态：后来的 stop 等待已有关闭，避免抢先停止 VM。原生 Quit 实测 browser confirmed、guest exit 0。
- 本轮共 3 次 API 请求，已耗尽硬限额，不再调用。第三次 API 返回 322 输入、2 输出 token；前两次用量记录在修复前丢失，不宣称掌握精确总 token 或费用。此前 2 次请求已明确计入独立额度记录，未因重启重置。
- 5 项协议/状态/额度/文件边界测试通过。API 模型历史已持久化，但为省费用，没有增加第四次请求验证多轮语义续接；AGY/Codex 原生 binding、完整工具与取消仍未验收，D1 保持进行中。


### D1 流式客户端补充（2026-10-03，未提交）

- GUI 从每 250 ms 网络全量轮询改为独立 SSE 读取线程；事件重复去重、缺口触发重连，重连先取快照。SSE 服务端按发布锁保证历史补发 → 权威快照 → 实时事件顺序，补齐临时 turn/engine 状态的订阅窗口。
- 发送线程与事件流独立；传输结果不确定时复用 request ID。发送错误不会被后台更新清掉，晚到的确认不会把已完成的 turn 恢复成忙碌状态。
- 快照和事件包含磁盘预留记录计算的请求额度。GUI 显示余量，耗尽后禁用发送；服务端返回 429，且不写消息、不排队调用模型。已接受请求的重复确认仍可返回。
- 11 项本地测试通过；macOS App 和 ARM64 musl Runtime 构建、App deep/strict 签名通过。真实 VM 验证 SSE 初始快照和心跳、429 拒绝且消息/游标不变，原生界面显示 3/3 耗尽。证据：`.local/d1/stream-verification.json`、`stream-tests.log`。
- 本轮新增模型调用为 0，保留已有 3/3 请求记录。取消、多轮语义续接和 AGY/Codex 原生会话仍未验收；D1 保持进行中。


### D1 Pi MVP 验收（2026-10-03，未提交）

用户将首个切面收敛为 Pi + DeepSeek，并允许按需调用、尽量省费用。现已交付 VM 内 Pi 0.87.1 RPC、原生工具事件、原生会话恢复、定向取消、工作目录入口及有界请求网关。真实原生 App → VM → Pi → DeepSeek → bash 写文件 → DONE 通过；Quit 重开保留相同 Pi session ID，模型正确回答上一轮 codeword；Cmd+. 取消长命令后无延迟写文件与额外请求。Chromium 为 Normal 退出。当前单会话 MVP 完成，不扩展为完整产品验收。

共 13 项常规自动化测试通过；另 1 项真实 Pi + 本地模拟 provider 集成测试覆盖工具、恢复、取消子进程、额度拒绝、缺失会话拒绝静默重建。真实验收新增 4 次 API 请求：8719 输入 token（6016 缓存命中）、122 输出 token。旧 smoke 3 次记录未重置。详细入口、边界和证据见 [Pi MVP 验证](validation/pi-mvp.md)。
