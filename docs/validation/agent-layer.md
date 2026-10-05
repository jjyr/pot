# Agent layer 验证

日期：2026-10-04。范围：#2 的 Pi/Runtime 基础增量；不是多引擎完整验收。

## 已验证

- Runtime 56 个非忽略测试、protocol 1 个测试通过。
- 原有 6 个 `real_pi` 测试保持通过；新增第 7 个真实 Pi 测试，通过两个本地模拟
  provider 验证迁移、模型选择、handoff bootstrap、旧 native session 保留和 provider 归属。
- Runtime/protocol `cargo check --locked`、Rust 格式和 `git diff --check` 通过。
- 没有付费模型调用；没有读取宿主 Codex 登录态或复用宿主会话。

覆盖旧元数据保留、重启后的固定快照、损坏原生会话、重复/迟到/终止后事件过滤、
迁移每个持久阶段重启、候选会话失败、设置等待运行轮次、新任务继承/旧任务固定、
既有设置导入幂等性、未知协议透明转发与非零占位语义、MCP 汇总权限和重复调用冲突。

复现使用开发指南中的 `mise run test` 和真实 Pi 命令；真实 Pi 测试只连接测试启动的
本地模拟模型。生产额外 provider 仍强制 allow-listed HTTPS，不允许测试的 loopback HTTP。

## Codex 探测（未验收）

候选：`@agentclientprotocol/codex-acp` 2.1.1、`@openai/codex` 0.159.1，安装到临时目录。
单独创建空 Codex 配置目录，剔除 API-key 环境变量，仅发送 ACP `initialize` 和
`session/new`；未发送 prompt、未登录、未访问用户历史。

- initialize 成功，报告 HTTP MCP、load/resume 等能力。
- session/new 返回 `-32000 Authentication required`。
- 不把能力声明视为取消、后台资源收敛、线程隔离或安全恢复验收。
- 探测后停止整个进程组。候选依赖未加入 Sandbox 镜像。

源码参考固定到 [ca1d9717](https://github.com/agentclientprotocol/codex-acp/tree/ca1d97173ad37b471d5a4e5847725a4657d34e29)：
`CodexAcpClient.createSessionConfig` 将 MCP servers 传入线程配置；`cancel` 请求
turn interruption 并等待 Codex turn/completed。前者尚无跨 lane 的负向实测，后者
不能单凭源码证明所有后台终端与子资源已停止。因此未实现/启用 CodexDriver。

## 未测范围

未构建或启动新 Sandbox 镜像，未跑真实 Codex 模型、登录刷新、musl ARM64、MCP
多 lane 隔离、native subagent 禁用、Codex 后台命令停止及恢复。未验证完整 profile
设置 GUI；当前通用 profile 操作使用 Runtime API，原设置表单保留。driver fake
覆盖目前是事件 gate，不替代完整 fake backend 的生命周期/取消测试。尚无按 provider
预算和按 profile/provider 并发限制。详细进度仅在开发计划维护。

## 2026-10-05 更新要求增量

- Runtime 65 个离线测试、protocol 1 个测试通过；7 个真实 Pi + 本地模拟 provider 测试通过，无付费调用。
- 新测试覆盖版本诊断不改变新 hash、旧 checksum 防篡改、命令参数冲突、PTY Unicode 输入输出、专用登录去重、会话数量限制与显式关闭。
- 真实 loopback WebSocket 测试覆盖 bearer 缺失、非原生 Origin、重复连接拒绝、resize、断开保留和缓冲回放；未使用用户凭据。
- `cargo check --locked -p pot-app`（含 Swift bridge）、Runtime ARM64 Linux/musl zigbuild、格式/lint 通过。
- 独立 WKWebView smoke 加载 bundled HTML/CSP/xterm，收到 ready，并确认 88×31 字符区和 renderer 创建成功、Unicode 写入无 JS 异常。
- 终端 UI 尚未做真实 VM/截图验收；未实际登录 Codex/Claude/AGY，没有 guest localhost callback 可达性或登录持久性结论。没有声称完整进程树隔离或新 Driver 验收。
- 本次未跑完整两页安装/引导矩阵。其余缺口以开发计划为准。

## 2026-10-05 任务路由增量

- Runtime 69 个离线测试、protocol 1 个测试通过；7 个真实 Pi + 本地模拟 provider 测试通过，无付费调用。
- 新增覆盖提示白名单及策略写入鉴权/revision 冲突、固定快照与重启、能力不匹配拒绝、重复创建冲突、计划逐次解析、profile/provider/global 上限和跨 provider 排队。
- 取消请求保留槽位；未确认停止的引擎保留全部并发租约直到 Runtime 重启，有确定性回归测试。
- `mise run check` 的格式/脚本、Rust workspace 和 Swift Helper 编译通过。尚无路由设置 GUI 或 Codex 跨引擎验收。

## 2026-10-05 Codex 传输对照（宿主，无凭据/付费调用）

`scripts/tests/codex-contract.py` 使用独立临时 HOME/CODEX_HOME、显式最小环境和
仅 loopback 的 Responses/MCP 模拟服务器。没有加载用户 CLI 配置、登录态或历史。
探测结束关闭整个进程组；版本只是结果中的诊断，不作为运行时门禁。

对照 Codex 0.159.1、codex-acp 2.1.1：

- 两个并发会话各自配置同名 `pot` MCP server，但使用不同 HTTP 身份。两次工具调用和
  输出均进入正确 lane；官方 MCP approval 往返只允许测试生成的无副作用身份探针。
- 原生 App Server：两轮各两次模型请求、一次 resume 后续轮次、一次被取消请求；
  共 6 次，未出现其他模型。累计用量 50→75，最后一次请求 25；Pot 应按累计差值计量，
  不能把每次累计值相加。
- ACP：除上述调用外还创建临时线程，使用固定 `gpt-5.6-luna` 生成两个标题；这不服从
  所选主模型。轮次 `usage` 为最后一次请求的 25，而这一轮实际模拟请求合计 50。
  因此当前候选不能直接满足模型固定和完整计量要求，后续 CodexDriver 选择原生
  App Server（#2 已允许此备用路线）。没有在会话中静默切换传输。
- 续聊保持 thread/session ID。取消测试同时等到终止事件与模拟模型 HTTP 连接断开；
  **不等于 shell 子进程/后台终端全部停止**，尚需专门 guest 负测。
- 模型可见工具中没有 native spawn/send/wait agent 工具。此结论仅覆盖探测调用。
- `mise run test:app` 的 10 个测试通过。

复现（传入已安装的可执行文件；脚本不会安装/升级）：

```sh
mise exec -- python scripts/tests/codex-contract.py --codex /path/to/codex
mise exec -- python scripts/tests/codex-contract.py --codex /path/to/codex --acp /path/to/codex-acp
```

这是传输选择证据，**不是已交付 CodexDriver 或真实登录/guest 验收**。


## 2026-10-05 原生 Codex Runtime 与 ARM64 guest

已接入原生 App Server 长驻 host，按 launch spec 共享进程、按 lane 绑定独立 thread。
主聊天、worker、自动汇总与取消走统一 Driver 事件。设置可在无 pi.json 时选择 Codex；
先打开原生候选并通过官方 `thread/inject_items` 写入初始化记录，再提交新绑定。
空线程不持久化的行为已在真实 CLI 中复现，因此不能仅保存 thread/start 返回的 ID。

`scripts/tests/codex-runtime.py` 使用真实 Runtime + Codex 0.159.1 + 本地 Responses mock，
隔离 HOME/CODEX_HOME，无宿主凭据、无付费调用。已通过：

- 无 Pi 首次配置、首次发消息前重启、连续轮次及 Runtime 重启后的原生续聊。
- 设置迁移、新 epoch、重复提交不调用模型、迁移后重启及缺失原生文件拒绝静默新建。
- 三次主轮次各有独立 MCP 回执；两 worker、同一计划两次手动触发及主会话的 thread ID 互不相同。
- 用量按原生累计值差额记账，原子更新基线；缺少基线、计数器回退及不完整数据不伪造零用量。
- 模型流取消等待终态；实际启动 `sleep 60` 后，轮次收尾终止原生后台终端，并检查进程消失。
- native subagent 工具不出现在模型 schema；汇总轮次明确携带 Runtime 的角色指令且不暴露 shell / apply_patch；继承的无关 MCP server 被管理会话禁用。

发现并修复了跨轮 MCP 连接复用：仅 thread/resume 会保留前一轮的 MCP 身份。
现在在资源收尾后 thread/unsubscribe，再在下一轮恢复，确保重新初始化 turn 身份。

以上测试在无网络 Linux ARM64/musl VM 中也通过，使用镜像内官方 Codex CLI 与交叉编译 Runtime。
证据输出保留 `guest_acceptance:false`：表示**尚未验证真实账户认证、模型服务或订阅额度**，
并非没有在 guest 执行。隔离 VM 不含用户登录态。

回归：70 个 Runtime 测试、1 个 protocol 测试、10 个 App 测试及 7 个真实 Pi/mock 集成测试通过；
`mise run check` 通过。Codex CLI 已由 npm lock 固定为构建基线并装入镜像，运行时不锁版本。
当前仍不能关闭 Issue #2：Claude/AGY Driver、其他网关 codec、完整设置/引导和真实账户矩阵未完成。

## 2026-10-05 Claude 原生 CLI 契约

原版 Claude Code 2.1.289，临时 HOME/CLAUDE_CONFIG_DIR 与本地 Anthropic SSE 模拟服务；
不读取宿主真实凭据，不调用付费模型。`scripts/tests/claude-runtime.py` 验证：

- 无 Pi 配置时选择 Claude；连续对话和 Runtime 重启沿用同一原生 UUID。
- 配置迁移不调用模型，重复请求幂等；新 epoch 创建独立 UUID；原生 JSONL 丢失时 binding 变为 broken。
- 两个 worker 和两次计划执行使用独立原生会话；只读汇总不能使用 Bash/Write/Edit。
- 显式 built-in 白名单排除原生 Agent/Task/SendMessage/Cron；Pot MCP 正常调用。
- 原生后台 `sleep 60` 在轮次完成前退出；取消等待模型请求后确认进程退出。
- 23 次本地模拟模型请求；无额外模型。结果 usage 为轮次累计量；缓存读写归入输入，缓存命中不重复累加。

旧宿主 Claude 构建在探针中出现辅助模型调用、结果只含最后请求用量及后台进程存活，
因此没有作为可用基线。Runtime 通过 help 控制项和 init 生命周期能力进行兼容性校验，
不锁死版本；用户升级仍须满足契约。取消/异常不能确认资源清理时保留“stop unconfirmed”。
Codex 原生契约回归通过（23 次本地模拟请求）。真实 Claude 账户、认证过期、服务商限流和升级矩阵仍未验收。

### 官方集成条款核实

2026-10-05 阅读 [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)。
当前页面明确说明产品可预装/运行原版 CLI，但要求适用 Commercial Terms、不得修改二进制或限制其认证方式、
用户自行认证并由服务商直接计费，不得代付/转售/中介用量。页面另明确区分原版 CLI 内用户自行登录订阅
与第三方应用收集/中介 Claude.ai 凭据。Pot 使用原版二进制及官方登录，未抽取 token；
这不是 Anthropic 对 Pot 的授权或背书，也不替代发行主体接受适用条款。

Linux ARM64/musl guest 补充：原版 2.1.289 + 最新交叉编译 Runtime 在非 root 用户下通过同一
完整契约（`.local/d1/claude-guest-20261005/data/claude-runtime.json`；23 次本地请求）。
TCP reset 与正常 EOF 均作为取消连接已关闭；summary 以工具白名单和 Runtime MCP 权限控制，
避免 CLI plan 模式连只读 MCP 也拒绝。测试要求每个模拟工具结果成功，不能用最终文字掩盖工具拒绝。
此轮 guest 使用检查目录挂载最新 Runtime，最终 bundled Runtime 仍需单独验证。

## 2026-10-05 原生 API codec

新增 Responses、Anthropic Messages、Gemini 的输出上限及 usage 观察器；Pi 直接使用相应
原生 API，不进行消息格式翻译。真实已安装 Pi 对三个本地模拟服务通过完整一轮请求，验证
路径/模型约束、真实 key 注入、输出 cap、缓存命中及 Gemini 思考 token 归账。
Anthropic message_start 不提前释放输出预留；message_delta 累计值覆盖，不重复相加。
8 个 `real_pi` 测试全部通过（含原 DeepSeek、第二 provider 迁移、取消、额度恢复、压缩、worker/计划）。
新增格式尚未验证真实服务商、压缩/额度检查点矩阵，因此其 safe_checkpoint_resume 保持关闭。

Claude 最终镜像 `pot.local/sandbox:cd5858f99ca0649d32c6` 已通过打包二进制 guest 验证：
`.local/d1/claude-guest-bundled-20261005/data/claude-runtime.json`（23 次本地请求）。
构建中的 npm 终止经内核日志确认为构建容器默认 1 GiB memory cgroup 限额；
将构建容器显式设为 3 GiB（VM 仍为 4 GiB）后安装与打包通过。这不改变用户 Sandbox 资源设置。

## 2026-10-05 API-only 设置

新增共享 API 设置表单及私有 provider 保存接口。离线测试覆盖无 pi.json 的初始配置、
未授权拒绝、UUID 幂等、冲突/旧 revision 拒绝、同 endpoint 空 key 保留、0600 权限、
公开响应/profile 不含 key，以及旧 binding 的 endpoint 引用不被新配置替换。
三个新增 codec 的真实 Pi 测试已改为完全无旧 Pi 配置/请求预算的环境，仍通过。
76 个 Runtime 测试、1 个协议测试及 8 个真实 Pi 离线契约通过；GUI 编译通过，尚未做交互截图验收。

## 2026-10-05 两页引导与 API-only GUI

独立 `dev.pot.setup-check` 应用和 `setup-ui-check-20261005` 数据目录，未改动用户运行中的 Pot。
实际界面检查通过：取消全选时不能继续；全选后进入第二页，同一 Runtime PTY 正常嵌入；
未登录的 Codex 不可开始；API-only 使用 `example.invalid` 和虚构密钥保存后进入空聊天。
通过 Runtime API 确认消息数和请求数都是 0；正常退出重启后保留完成状态和模型选择，仍为 0 请求。

首次主 Agent 选择只保存元数据，原生会话延迟至第一条真实消息创建；已有会话迁移保留预检。
Codex/Claude 宿主离线契约均回归通过（各 23 次本地模拟请求）。
工具准备使用镜像预装并检测复用；缺失工具可进入 Terminal 修复或取消勾选后稍后处理。
此证据不包括真实登录、下载中断恢复及完整浅/深色布局矩阵。

## 2026-10-05 设置、路由及生命周期补充

- Profile 编辑/复制不改变主绑定及 worker fallback；显式 worker profile 不随后续连接设置覆盖。
  新增授权、未知 profile、revision 冲突及显式用户选择优先的回归测试。
- 可注入 fake session 验证并发事件隔离、重复/迟到过滤、取消后清理失败和缺失终止事件。
  缺失终态标为 stop unconfirmed，不释放并发容量。
- Provider 额度验证跨 scope 共享、provider 隔离、全局/局部共同约束、手动恢复、重启预留保留、
  事务回滚及 wait route 清理；真实 Pi 的 8 个本地模拟 provider 测试通过。
- Codex/Claude 宿主契约加入自定义 wrapper 参数验证，各 23 个本地请求全部通过。
  Runtime 管理参数冲突拒绝；wrapper 字符串不经 shell 展开。

### AGY 尚未解决的接入条件

阅读官方 [CLI headless](https://antigravity.google/docs/cli/headless/)、
[CLI reference](https://antigravity.google/docs/cli/reference/)、
[subagents](https://antigravity.google/docs/subagents/)、[MCP](https://antigravity.google/docs/mcp/)
和 [Additional Terms](https://antigravity.google/terms)（2026-10-05）。
官方 headless 文档有结构化事件和用量，subagent 配置支持 tools；CLI help 本身未提供
已验收的每 lane 禁用原生委派、独立 MCP 与取消收敛组合。尚未据此启用 Driver。
条款明确包含使用第三方产品接入服务的限制；企业路径适用不同协议。
这些资料不足以确认 Pot 的原版 CLI 托管调用符合适用授权，需发行主体或服务商确认。
仅保留现有 Terminal 可用状态，不把它当作技术/条款验收通过。

最终增量验证：82 个 Runtime 测试、1 个协议测试、10 个 App 测试及 8 个真实 Pi/mock 测试通过。
`mise run check` 和构建打包通过；镜像 `pot.local/sandbox:4247db1d941e1045cb89` 内的
Codex/Claude 原生契约均通过（各 23 次本地模拟请求，含 wrapper 参数）。
证据目录为 `.local/d1/codex-guest-final-20261005/data` 与
`.local/d1/claude-guest-final-20261005/data`。

隔离测试应用的最终 Sandbox API 验证 profile 复制幂等、原绑定不变、路由/provider 额度保存、
旧 revision 拒绝，模型请求数为 0。新任务路由/额度设置和任务“更换 Agent”界面通过编译；
实际交互检查遇到 Mac 锁屏，已请求用户解锁，未将其记为 GUI 验收通过。
之前两页引导 GUI 验证不替代这次新增页面的交互验收。测试 Sandbox 已通过 helper 正常停止。


## 2026-10-05 自动触发、认证故障和路由交互补充

Codex/Claude 的 Runtime 契约启用一分钟间隔计划，等待 Runtime 真实时钟触发，
不调用手动 run 接口；自动执行的原生会话与主聊天、两个 worker、两次手动执行均独立。
宿主两套真实 CLI 均通过；Codex 的原打包镜像也通过更新后的契约
（`.local/d1/codex-guest-clock-20261005/data/codex-runtime.json`）。

本地模拟 provider 持续返回 401 后再恢复成功响应，验证：失败消息明确可见、
引擎回到 ready、用量标为 native_incomplete、binding 保持 active 且 native ID 不变；
后续轮次恢复原会话，不读取或改动真实账户认证。Codex 宿主与 guest 均通过。
Claude 原 CLI 发送 api_retry/authentication_failed，原行为会重试数分钟；
Runtime 现在通过已验证的 SIGINT 生命周期停止，等待终态和进程退出后提示重新登录。
宿主契约通过认证失败恢复、非重试型 400 故障和诊断过滤；原生续聊、取消、后台进程
停止、worker 和计划仍通过。测试只有 loopback 请求，没有付费调用。

解锁后的隔离应用交互验证：复制 API profile、选择后台默认、允许 Agent 指定的白名单、
配置并发 2/provider 并发 4 保存成功；provider 每周 15000 Token/自动恢复保存成功。
Runtime API 核对持久值且模型请求数为 0。长权限说明被截断的问题已修复并通过编译；
修复后的布局和任务页迁移交互尚未重新检查，以免中断用户正在使用的 Sandbox 登录终端。
真实账户登录/调用、完整升级与浅深色矩阵仍未验收，AGY 接入条件仍待解决。


## 2026-10-05 独立 Terminal 与真实 Codex 账户验收

按更新后的 #2，Terminal / Sandbox 是两个侧栏按钮。移除专用登录 PTY、登录动作/命令
配置及 Helper 历史 login RPC；Runtime 拒绝旧 agent 登录终端请求，旧 catalog login
字段仅在反序列化时忽略，不执行、不返回。官方状态检测保持只读。
环境/工具准备后进入普通助手设置；无勾选亦可继续，主 Agent 不自动预选。

隔离测试应用 `dev.pot.setup-check` 实测独立导航、只读 Agent 状态、空勾选进入助手设置、
稍后设置打开 Terminal。全新 `independent-cold-ui-20261005` root 的 profiles/bindings
均为空，未登录 Codex/Claude 不可选，usage requests 为 0；Terminal 可用。
忙碌 shell 的 read 等待期间切换 Terminal/Sandbox、触发状态刷新并返回，输入得到
`POT_BUSY_NAV:KEPT_SHELL_INPUT`，没有命令注入，焦点自动恢复。
App 新回归禁止 NeedsAuth 时发送主聊/任务 guidance。最终构建后的界面门禁和长说明
复核遇到 Mac 锁屏，不能将编译/单测记为最终 GUI 验收。

真实账户测试使用用户自行登录的 guest 原版 Codex，模型 `gpt-6-luna`：
主聊调用 Pot MCP pot_list_tasks 成功；两个并发 worker 使用独立 native threads；
只读汇总执行并归账；正常重启后官方状态仍 authenticated。
镜像 `pot.local/sandbox:ff8c27149694187b0495` 下再次重启，epoch 2 和原 native
thread `01a10962-c6ac-7331-8720-54ca5e80af90` 保持不变，MCP 续聊成功。
前台 shell sleep 60 执行中取消，确认终态 cancelled、turn 清空、engine ready；
随后普通轮次在原线程完成，证明容量释放和续聊可用。

真实验收发现 summary 后省略 shell/file feature overrides 无法恢复工具；现每轮显式
设置，并声明当前普通主轮次权限。宿主/guest 契约均增加 summary 后恢复 shell 的回归。
真实取消前 shell 未启动的旧失败尝试保留为历史，不计为通过。
取消轮次不完整用量标 unknown，不记零。此次真实测试累计账本为 9 个轮次记录，
input 255894、output 1060、cache 210432、unknown 1；并非 0 付费调用声明。
订阅路线为原生上报事后归账，不能将该记录数等同 HTTP 模型请求数。

证据位于 `setup-ui-check-20261005/workspace/product/issue2-account-{main,workers,resume,cancel,after-cancel}.json`。
冷配置证据位于 `independent-cold-ui-20261005/workspace/product/issue2-cold-ui.json`。
仅测试副本切换数据 root；一次 Quit 后 CUA 状态查询自动重启该副本并落到默认目录，
发现后停止了该副本，未发送模型/聊天动作；之后固定 LSEnvironment 防止再次发生。
未停止用户日常 Pot，未读取宿主/guest 登录凭据或在线 SQLite。

最终 ff8c guest 的 Codex 契约通过 35 次 loopback 请求；Claude 的 d2bc guest 契约
通过 32 次 loopback 请求，包含真实时钟计划、401 及时停止/恢复、400 诊断过滤和取消。
这些离线请求 paid_calls 为 0。Claude 真实账户仍待用户自行登录；AGY Driver 的接入
许可及技术隔离契约未确认，仍未启用。兼容/不兼容升级、下载故障恢复、完整浅深色
和权限界面矩阵未全部验收，本次不宣布 #2 可关闭。


最终补充：重启后的 snapshot 引擎/模型名称读取持久 binding，不等到首轮再纠正旧启动默认值；
对应回归通过。工具发现读取版本化默认 profile 的 invocation，缺失 wrapper 不借用系统
默认 CLI 的安装/认证状态；带 wrapper 参数时保留 unknown，不执行未知状态命令。
Runtime 84、协议 1、App 11 个测试通过；`mise run check` 及构建通过。
Pi 的 8 个真实 CLI/mock 契约沿用本轮前已通过的证据，未将其标成重新执行。


最终交付包镜像为 `pot.local/sandbox:21841888feb8f142ea98`。
隔离副本更新后正常重启，首条消息前即显示 `Codex · gpt-6-luna`；
只读官方检测为 authenticated，epoch/native thread 保持不变，usage 仍为 9 个轮次记录，
该包核对新增模型调用为 0。Claude 仍为 required，AGY 为 unknown。
证据为 `setup-ui-check-20261005/workspace/product/issue2-final-package.json`。
最终包核对没有重跑完整原生/真实模型矩阵；前述 ff8c/d2bc 镜像契约证据保持各自版本范围。


## 2026-10-05 Claude 真实账户跳过、兼容性与 Terminal 清理

用户明确决定无 Claude 账号，先集成并跳过真实账户测试。ClaudeDriver、助手设置、
官方只读状态查询及 guest 原版 CLI 保留；不等待登录、不把离线 mock 结果写成真实
账户通过，也不自动选择未登录的 Claude。

Claude 成功的 capability probe 按 launch 配置和 executable metadata 缓存。
未变化命令不再每轮运行 --help；文件替换/重装后重新检查，缓存有界，检查输出和时间
有界，失败停止 probe 进程组。缺失或不兼容命令在模型请求前设置可操作的 needs_rebind
原因；原绑定和 native session 保留，恢复兼容命令后显式 retry 仍使用原 native ID。

宿主原版 2.1.289 CLI + loopback Anthropic 服务的扩展契约通过 36 次本地请求，
paid_calls 0：两轮不重复 probe、Runtime 重启检查一次、wrapper 文件替换模拟兼容升级、
缺失控制参数和缺失程序不调用模型、恢复后原 session 续聊；原有 401/400 处理、
独立 worker/计划、真实时钟、只读汇总、取消及后台资源收敛均通过。
命令替换模拟升级不等同于对所有厂商版本升级都兼容的承诺。

新增 terminal-runtime.py 在无网络 Linux guest 实测：shell 的前台 sleep、独立后台
进程组、忽略 HUP/TERM 的后台 shell 及其 sleep 孙进程，在关闭 PTY和 Runtime 正常
终止后均不再运行；重启 boot ID 改变、PTY 列表为空、用量请求数 0。
证据位于 terminal-tree-guest-20261005/data/terminal-runtime.json。
本检查不包括刻意 setsid 脱离终端 session 的守护进程，该范围仍由 VM/D4 处理。

补做已授权的真实 Codex 时钟验收：在既有隔离账户 root 上创建一分钟间隔计划，
只等待 Runtime 时钟，不调用手动 run。自动 run 完成并写入结构化报告，native thread
独立于主线程及先前两个 worker，主 epoch 2/native ID 保持不变；随后立即暂停计划。
该 run 的原生上报 input 44197、output 169、cache 28160、unknown 0；累计账本
为 10 个轮次记录、input 300091、output 1229、cache 238592、unknown 1。
这是真实服务调用，不能写成 paid_calls 0。证据为
setup-ui-check-20261005/workspace/product/issue2-account-clock.json（218418 镜像）。

Mac 仍锁屏，最终发送禁用态、长说明和任务迁移的 GUI 复核继续保持未测。
Claude 真实账户跳过不再作为推进阻塞；AGY 接入授权/实际隔离契约仍未确认。


最终镜像 `pot.local/sandbox:5240639ea6342c173067` 的 Claude guest 扩展契约通过
36 次 loopback 请求（77 秒含 import/unpack），paid_calls 0、account_acceptance false。
证据为 claude-upgrade-guest-20261005/data/claude-runtime.json；同镜像 Terminal
进程清理契约通过（7 秒含 import/unpack）。Runtime 84、protocol 1 测试通过，
`mise run check`、构建和打包通过。本轮未改 App，也未重复原有 11 个 App 测试。


隔离测试副本更新为 524063 镜像后正常重启。只读确认 Codex 仍 authenticated、
原 epoch/native thread 不变，Claude 为 required，测试计划均已暂停，累计仍为 10
个轮次记录；此包核对新增模型调用 0。证据为
setup-ui-check-20261005/workspace/product/issue2-claude-package.json。


### 2026-10-05 AGY 原版 CLI 驱动（有限主聊接入）

按用户要求采用与 Claude 相同的原版 CLI + AgentDriver 路线，新增 `agy_json` profile、
CLI 创建的 native conversation binding、`--conversation` 续接、最终用量、SIGINT 取消及
助手设置入口。移除旧 run_agy 直连路径。保留 CLI 自己的 guest HOME/权限配置，不读取凭据；
AGY 没有已验证的官方只读 auth status，状态保持 unknown。

宿主和成品镜像 `pot.local/sandbox:fb8d9bcb06dacd38b387` 的模拟传输契约均通过：
首次原生 ID、续聊、Runtime 重启后同 ID、命令缺失/恢复、错误/无效输出诊断过滤、
ID 变化拒绝、result 后非法输出不发布成功消息、最终用量只记一次、取消后原会话继续。
任务路由在启动 CLI 前拒绝 AGY；驱动也拒绝 worker 和只读汇总角色。
此 fixture 是 fake transport，不是原版 CLI 模型/工具验收。

同一全新无网络 guest 的原版 AGY CLI smoke 通过：帮助参数存在；独立空 HOME、
默认模型、无 TTY 的 headless 调用失败退出，无凭据读取或远端模型调用。
证据为 `.local/d1/agy-driver-auth2-guest-20261005/data/{agy-cli-smoke,agy-runtime}.json`；
检查含 import/unpack 7 秒。Runtime 88、protocol 1、App 11 项测试通过，
`mise run check`、build、bundle 通过。界面交互未复核（Mac 仍锁屏）。

未验收范围：AGY 真实账户/模型、原版 CLI 工具事件、后台进程完整收敛、厂商适用条款。
AGY 自定义 agent tools/MCP 配置尚未证明全局配置隔离，且缺少 Claude 的严格 MCP/source
开关；因此不宣称 task/MCP/subagent 禁用/只读汇总能力，后台角色保持禁用。选择 AGY
或编辑其调用命令不覆盖已有 worker default。此结果不代表 Issue #2 的 AGY 全部验收完成。
