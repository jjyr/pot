---
tags: [Agent, Session, Architecture, Research]
updated: 2026-10-02
---

# 长期 Agent 的 Session 与任务协调机制对比

> 历史研究：版本与“当前”均指原文核对日期。实施现状见 [开发计划](../development-plan.md)，不是本报告中的建议或旧状态。

## 0. 范围、依据与结论性质

本文回答：长期助手如何维持 session，怎样限制模型上下文，后台 tasks 如何与主会话协调。**只归纳已有项目的机制与公开行为，不提出 pot 设计，也不修改产品决策。**

主要依据：[Hermes Session 管理架构与机制](hermes-sessions.md)、[OpenClaw Session管理架构与机制](openclaw-sessions.md)及其固定版本来源；Dot 官方 Tasks and memory、Controls。另补读同一 Hermes 快照的 Delegation、Cron 文档，因为原 session 笔记不足以说明后台完成投递、steering 和定时任务连续性。

| 对象 | 证据范围 | 不能推导的内容 |
|---|---|---|
| Hermes | `6dbb7b9ab857842617c2c35b63b49bd6ee77fc56`；既有源码研究 + 本次补读固定版本官方文档 | 没有运行实测；不能当作任意后续版本的默认行为 |
| OpenClaw | `a272cf0cd56eb9d0a1245daaaf139902ac32da01`；既有文档/源码核对 | 没有运行实测；不能沿用旧版本 JSONL/daily-reset 描述覆盖此快照 |
| ChatGPT Dot | 已留存的 2026-10-02 官方产品文档；本次重读 Tasks and memory、Controls 正文 | 没有公开内部 session store、token 阈值、压缩算法、调度器和恢复实现 |

下文「事实」指上述证据所述行为；「归纳」指据此作出的解释，不是新增实现事实。文中的 task 是一般工作称呼，**不表示三者都有相同 task schema 或同一个一等 Task 对象**。

## 1. 最重要的区分：长期助手不是无限上下文

**依据归纳**：三者都不能用「一个模型永远带着所有历史运行」来准确描述，但可证实的深度不同。

| 层次 | 它回答什么 | 已有依据 |
|---|---|---|
| 助手身份与长期知识 | 换会话后仍认识什么？ | Hermes 的身份/记忆文件；OpenClaw workspace memory；Dot 自有 notes 和相关 ChatGPT memory |
| 会话入口与持久历史 | 新消息应接着哪段对话？ | Hermes key → ID；OpenClaw sessionKey/生命周期；Dot 跨渠道仍是同一助手，但可见对话不同 |
| 本轮模型 context | 现在实际看见什么？ | Hermes live history/摘要/快照；OpenClaw 组装/pruning/compaction；Dot 明确是 selected context |
| 一次 run/turn | 这次模型与工具循环何时结束？ | Hermes turn lease；OpenClaw run/session lane；Dot completed run 不保证结果已达成 |
| 后台工作与未来触发 | 用户不继续发消息时谁推进？ | Hermes async delegation/cron；OpenClaw spawn/notices/heartbeat；Dot background agents/schedules/events |

所以「session 长期存在」与「本次推理知道全部历史」不是同一件事；「助手长期记得」与「原后台进程仍在运行」也不是同一件事。[H1][H2][O1][O2][D1]

## 2. Hermes：稳定会话 + 有界 live context + 异步结果回流

### 2.1 长期 session 怎样延续

**事实**：Gateway 根据 profile、平台、chat/thread 等生成 `session_key`，路由到持久 `session_id`。同一入口通常继续原 session；Agent 对象可以释放/重建，Gateway 重启不因此重新开对话。[H1]

本快照核心不因跨日、空闲、进程重启自动换 session。idle/daily 时间策略由另外的 reset-policy 插件提供，不能混成核心默认行为。

显式 `/new` 或 `/reset` 才建立新 ID，并让同一个 key 指向新的 session；普通默认压缩保留 ID。`/resume` 可以重新绑定历史 session。特殊 `/stop`/suspended 路径有替换行为，不宜概括成“stop 永远保留 ID”。

```text
同一消息入口 key
  → 持久 session S1
      → 用户 turn
      → 压缩：S1 保持，live context 缩小
      → 重启：恢复 S1，重建 runtime
  → 显式 reset：改指向 S2
```

### 2.2 历史增长时，哪些东西留下

**事实**：默认 in-place compression 在同一 ID 内更新 live message list，旧消息软归档，保留历史检索/展示路径。[H2]

模型的当前工作视图大致是：保留 head + 结构化摘要/anchors/恢复指针 + 最近 tail，而不是每轮全量 transcript。摘要涵盖目标、约束、进展、决定、文件与下一步；工具调用/结果的切点需要成对。

Gateway 启动 Agent 前有 session hygiene，Agent loop 内另有 compressor；它们触发依据不同。不能把配置 `threshold: 0.50` 当作所有模型的实际统一阈值。原生 Codex app-server 拥有的真实 thread context 应通过原生 compact 处理，单改 Hermes 本地历史投影不足以缩小原生 thread。

**记忆不是摘要的别名**：`MEMORY.md`/`USER.md` 在 session 开始时冻结进 prompt；写文件并不自动替换当前 prompt 的初始记忆块。历史缺失的信息可通过 `session_search` 取回，只有取回的片段才重新占当前 context。[H1][H3]

### 2.3 委派任务怎样从主会话分离

**事实**：`delegate_task` 创建独立 child AIAgent，child 使用 fresh conversation，不自动获得父会话全文或 prior tool calls；父助手通过 `goal`/`context` 明确提供任务背景。[H4]

需要避免把文档的“子 agent 不知道父上下文”误解成完全空白：有解析出的 workspace 时，项目规则文件可进入 child system prompt；这不是复制父 transcript。

child 继承父已启用工具的范围，并额外禁用 shared memory 写入、直接用户澄清、跨平台消息、cron 等工具；叶子 worker 与 orchestrator 的继续委派权限不同。

**归纳**：Hermes 用任务 briefing 与结果摘要连接父子 context，而不是将完整子工具日志反复灌进主会话。可以查看 live transcript，但默认父 context 的主要交付是最终结果。

### 2.4 用户继续聊天时，任务如何继续

**事实**：可接收 detached results 的顶层运行中，委派自动异步，先返回 handle，父会话可以继续。一个 batch 默认等所有 children 完成，再以合并结果进入父会话；结果只在父 turns 之间投递。[H4]

`independent_completions` 为可选模式：可按完成单元分别回流，同 group 仍合并。同一批执行依然并行，group 改的是投递粒度，**不是任务依赖或执行顺序**。

orchestrator child 等待自己的 workers 并综合后返回；不支持 detached completion 的 one-shot/cron 等环境可退回同步。不能说所有 Hermes 调用方式都让父助手立即继续。

### 2.5 追加指导、完成事件与取消

**事实**：父助手可按 `subagent_id` list/steer/stop 自己 spawn tree 内的 children；用户普通 follow-up 不会自动取消后台 batch。[H4]

steer 在 child 下一次 iteration boundary 进入，不强行切断 in-flight tool。queued 只说明已接纳，不证明 child 已读；来不及消费的指导会以 `pending_steer`/`missed_steer` 保留在结果里，结束后再来的 steer 被拒绝。

完成事件先进入 profile `state.db`，再走 fresh-turn queue。Gateway 的 ACK 以 adapter 实际接纳/排队为准，但这仍不证明模型 turn 或 outbound reply 完成；API-server 模式可以只增加 timeline delivery，由客户端驱动下一轮模型调用。

重启后未投递完成事件可以重试，投递是至少一次语义；**正在执行的 child 不因此自动恢复**，owner 进程消失时可记为 unknown。`/stop`、重置或关闭 owning session 会影响该 session 的后台 children；不能套用 Dot 的 Pause 语义。

child 的后台进程也有 owner：child 结束时未显式移交的进程会被终止。返回一个 PID 不等于进程已经转给父助手；文档提供专门 handoff 动作。

### 2.6 定时任务不是一直挂在主 session 中等待

**事实**：cron job 保存 prompt、schedule、skills、投递目标等定义；每次运行是 fresh agent session，不默认继承创建它时的聊天历史或上次 run 全文。[H5]

连续性有几种明确机制：

- 自包含 prompt/skills 给出每次运行所需信息。
- `context_from` 读取指定上游 job 的已有输出并前置到 prompt；不等待同一 tick 正在执行的上游 job，不等于严格 DAG 调度。
- `continuity=true` 将本 job 上次有实质内容的输出注入下一次 fresh run；延续的是显式输出，不是复用全部原 session。
- `attach_to_session`/`mirror_delivery` 可选地让投递结果进入可续聊历史；默认关闭。thread-capable 平台优先每次新 thread，DM 平台可镜像到目标 DM。

**重要细节**：默认“消息送到了用户”不等于“回复它的聊天 session 已经知道那条结果”。continuable delivery 专门解决这层连接，不能由通知成功自动推导上下文已同步。

job 定义、输出文件与执行 ledger 分别持久化。重启中断的进行中 job 可标 unknown，不自动重跑该次执行，下一次 schedule 仍可正常触发。连续性文档明确错误输出可作为恢复背景，不能概括成“只读取成功结果”；上游输出筛选的精细差异应再核对实现。

## 3. OpenClaw：rolling main + 子 session + 事件汇流

### 3.1 长期 session 保持什么

**事实**：Gateway 权威管理 `sessionKey`、transcript、执行与路由；默认 `session.reset.mode: none`，main 是 rolling session。daily/idle/manual reset 可选。[O1]

与 Hermes 的差异：该快照普通持久 session reset 可以保留 `sessionId`，但追加 reset boundary、修改 lifecycle revision，让模型从新历史窗口开始。文档有口径不一致，既有笔记已以源码校正；ACP/特定分支可不同。

因此不能用「ID 有没有变化」独自判断“还是不是原上下文”。新窗口、transcript 身份、稳定入口分别有生命周期。

### 3.2 如何控制上下文而保留连续性

**事实**：模型上下文由当前窗口、bootstrap 指令、工具、当前输入与适用 memory 组装；不是 UI 可见所有消息的原样全量复制。[O3]

- pruning 主要缩减旧工具结果的模型投影，原始结果仍可保留。
- compaction 保存旧历史摘要并保留近期尾部；工具调用/结果要对齐，pending asks 与精确 IDs 是摘要关注点。
- memory flush 是压缩前写入 workspace 长期记忆的独立维护机会；**flush 内容和 compaction summary 不是同一资产**。
- 其他私有会话的相关 recall 可以按配置取回，但不合并它们的 transcript；私人 memory 加载有范围约束。

默认 context engine 的部分接口是 pass-through/no-op，不宜按插件 API 推定所有步骤都由独立 engine 实现。原生 Codex 仍可有自有 thread 与 compactor。[O3]

### 3.3 主 session 与任务怎样协调

**事实**：`sessions_spawn` 创建子 session，返回 `runId`/`childSessionKey`；`sessions_send` 给已有 session 发送输入；history/search/status 工具读取权限范围内的信息。[O4]

普通非 thread spawn 默认 isolated；某些 thread-bound spawn 默认可 fork 父 active context，具体受 runtime、同 agent 与容量约束。因此 OpenClaw 的子任务不总是像 Hermes 默认委派那样完全 fresh briefing。

同一 session 的 run 通过 lane 与 execution claim 串行；不同 session 在各并发预算内执行。子 agent、cron、heartbeat 等有相应预算/lane，前台不必持有子工作全部执行过程。[O5]

**忙时输入**可 steer、followup、collect、interrupt；该快照默认 steer。steer 只在支持的安全边界进入，并不保证已运行工具立即停止。

### 3.4 Home 如何知道后台进展

**事实**：main/Home 是普通 session 加 root 角色，不是所有子 transcript 的无限汇总。[O2][O4]

- Home 发起的子任务通常回报 Home；其他 requester 有自己的完成 owner，quiet/collector/thread 投递存在例外。
- 新会话可以生成 creation notice，创建本身不一定唤醒 Home。
- 群组活动可按 conversation 合并为 compact notices，而非复制完整群聊。
- heartbeat 唤醒 main，让累计事件在没有新用户消息时也能被消费。
- history/search/send 让主助手按需了解或指导其他 session；收到 notice 不自动获得读取权限。

**归纳**：主入口保留协调所需的信息，任务执行历史留在各自 session，完成回报与合并通知连接二者。不能由此推定 Home 每时每刻已看完全部任务。

### 3.5 周期任务、恢复与消息可靠性

**事实**：既有文档所述 cron 运行使用新的执行 session，而 heartbeat 可唤醒 main；这两种背景执行不应混为“都把长期主 session 从头重跑”。本文没有补核所有 cron payload/投递配置分支。[O1][O2]

active turn 重启后可沿原 session 尝试恢复，复用已记录输入与工具结果；但未执行的内存队列不会全部自动重放。普通输入落盘、被接纳执行、工具完成和回复投递是不同阶段。[O1][O5]

后台通知队列有有界内存状态，另有 durable signal log/版本增量；不能把所有 notice 都视为持久可靠任务队列。run receipt 说明被接受，不说明任务达成；取消不回滚外部副作用。

## 4. Dot：公开的是长期协作语义，而非 session 算法

### 4.1 能确认的长期连续性

**官方事实**：[D1]

1. 同一 dot 可追踪多个 responsibilities，用户在同一 conversation 改优先级、补充信息、切换工作。
2. dot 使用当前 conversation、相关 ChatGPT memory 和自己的 saved notes。
3. notes 记录偏好、决策与 ongoing work，可随新决定更新；不是完整 transcript，也不等于 ChatGPT saved memory。
4. 每次 interaction 使用 selected context，可能不同于后台任务可用的信息。
5. 跨 ChatGPT/Slack/Teams 联系同一 dot 不重置 notes，但可见 conversations 仍分开，不自动互相复制消息。

**不能确认**：Dot 是否固定一个 session ID；多久 reset；是否每天切 epoch；何时压缩；用 SQLite、向量库还是图；如何让 notes 更新进入运行中的 context。官方的“长期同一助手”不提供这些实现答案。

### 4.2 主助手与 tasks 怎样协调

**官方事实**：[D1]

- dot 可以把工作分给并行 background agents，结果回报 dot，用户可继续与 dot 聊天。
- 也可以创建独立可见 threads，并发送 follow-up instructions。
- 新 task 接收针对该工作的 instructions/context，有自己的 conversation，**不会自动收到用户与 dot 的全部对话**。
- Activity 可查看进度、文件、结果、输入请求，也可直接指导；用户离开时 dot 可以继续协调。
- 原 task 保持计算环境归属；切换 dot 当前选择的电脑不自动搬迁旧 task。
- completed run 不代表请求的结果确已实现或交付，仍需检查结果与错误。

**依据归纳**：Dot 的长期关系由主助手和 notes 保持，具体任务用自己的 conversation/context 执行，由回报与 follow-up 连接。这个解释不等于证明其内部有与 Hermes/OpenClaw 相同的数据结构。

### 4.3 不聊天时如何继续，以及停止的作用域

**官方事实**：[D1][D2]

- assigned work 可以在 conversations 之间继续，dot 可决定 pause/wake-up，或根据时间/受支持事件继续。
- 固定重复工作需要 saved schedule，含指令、时间/时区、通知条件与交付目标；连接服务本身不创建 monitoring task。
- proactive research 可读取获准信息并保存 private notes，background agents 回报 findings；这一角色不能发消息、改应用或控制浏览器/电脑。
- Pause 只停止当前 main task；停止 delegated task 与取消 schedule 是不同动作，Pause 不自动执行后二者。

**不能确认**：pause/wake-up 的事件存储、心跳频率、执行恢复、结果去重，以及是否可在所有后台阶段追加 steering。产品承诺不能外推为 exactly-once 或崩溃无损恢复。

## 5. 横向归纳：三者解决的是哪些不同问题

| 问题 | Hermes | OpenClaw | Dot |
|---|---|---|---|
| 用户长期面对谁 | 身份/记忆文件对应的实例；入口各自路由 session | agent 的 main/Home，另有渠道/子 session | 同一长期 dot；跨渠道可见对话仍不同 |
| 默认跨日换 session 吗 | 核心不自动时间 reset | 默认 none；时间 reset 可配 | 未公开 |
| 大历史如何进入模型 | live context 摘要/head/tail；历史另存 | 当前窗口 + pruning/compaction + recall | selected context；算法未公开 |
| 压缩是否等于遗忘 | 否，旧消息软归档可检索 | 否，摘要/投影与原始历史分开 | notes 不等于全文；压缩存储机制未知 |
| 子任务继承什么 | fresh conversation + 显式 goal/context；项目规则例外 | isolated 或受限 fork，取决于入口/runtime | task-specific instructions/context，不自动继承全部对话 |
| 主助手为什么不必阻塞 | 支持异步的顶层先返 handle，结果在 turns 间回流 | 子 session 与各 lane/budget 执行，回报 requester/Home | 官方明确后台并行且用户可继续聊天 |
| 用户如何改任务 | 指定 child steer；queued/delivered 有区别 | send/steer 等有范围与安全边界 | 主助手 follow-up 或 Activity/task 直接指导 |
| 后台完成如何入主上下文 | 完成事件 → 持久记录 → 投递/父 turn；API 模式例外 | requester completion、compact notices、heartbeat 消费 | findings/results 回报主助手；内部投递未公开 |
| 定时工作连续性来自什么 | job 定义 + fresh run；可注入上次输出/续聊镜像 | cron 执行 session 与 heartbeat/main 区分 | saved schedule/notes/任务 context；实现未公开 |
| 主暂停是否停全部 children | Hermes stop/reset 可取消 owning children，不是 Dot Pause | 依目标 session/run 与工具模式，不宜一句概括 | 明确不停止全部委托和 schedules |
| 重启后什么可以继续 | 持久 session/待投递完成可恢复；执行中 child 可 unknown | active turn 可恢复；未执行内存队列不全自动重放 | 内部恢复机制未公开 |

## 6. 可以得出的结论，以及不能得出的结论

### 6.1 证据支持的归纳

1. **长期的是入口、身份、历史和工作关系；模型输入始终是有选择、有边界的。** Hermes/OpenClaw 有机制证据，Dot 有 selected context 的产品证据。
2. **主会话可以连续，但任务不必使用主会话同一份上下文。** 三者都有分离任务 context 的明确依据，继承策略却不同。
3. **协调依靠“派发 + 回报 + 指导 + 再唤醒”，不是让主模型无限持续占用。** Hermes/OpenClaw 展示具体事件/路由机制，Dot 公开相应行为。
4. **原始历史、工作摘要、长期记忆、未来 schedule 是不同资产。** 保存一个目标到 memory 不自动意味着已经创建可执行 schedule；收到通知不自动意味着后续会话已知道全文。
5. **主助手可用性与后台工作生命周期分离，但并非完全无关。** Hermes owning session 的 stop/reset 能取消 descendants；Dot Pause 反而明确不停止全部委托。
6. **可靠性需逐阶段理解。** accepted/queued、开始执行、完成、投递、用户目标达成不能互换；崩溃后的 unknown 与至少一次投递也不等于外部副作用 exactly-once。

### 6.2 暂不能下的结论

- 不能说这类助手都定期创建新 session：Hermes/OpenClaw 此快照默认恰好不自动时间 reset。
- 不能说长期主聊天等于单一永久 context：Hermes/OpenClaw 有压缩/窗口机制，Dot 明确选择 context。
- 不能说 tasks 总继承全部主历史：Hermes 默认不继承，OpenClaw 有 isolated/fork 分支，Dot 明确不是全部对话。
- 不能说所有后台结果都自动进入主模型：Hermes API-server、cron continuable opt-in 和 OpenClaw notice/wakeup 都显示投递与消费有条件。
- 不能说知道 Dot 使用 notes，就知道它的记忆检索、session rotation 或后台 scheduler 实现。
- 不能把已有笔记中针对 pot 的 epoch、memory ownership、控制权加固建议反过来当作这三个项目的现成机制。

## 7. 证据索引

以下来源标签只用于定位依据，不是测试结果。Hermes/OpenClaw defaults 均受固定版本约束。

- [H1 Hermes Sessions](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/sessions.md)：延续、reset、恢复、搜索；源码修正详见[Hermes Session 管理架构与机制](hermes-sessions.md)第 4–5 节。
- [H2 Hermes Context Compression and Caching](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md)：in-place、软归档、上下文 ownership；阈值实现详见原笔记第 7 节。
- [H3 Hermes Persistent Memory](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md)：冻结核心快照与学习。
- [H4 Hermes Subagent Delegation](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/delegation.md)：fresh child context、async handle、completion grouping、durable delivery、steering、owner 与取消；本次补读。
- [H5 Hermes Scheduled Tasks](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/cron.md)：fresh run、context_from、continuity、continuable jobs 与 unknown；本次补读。文内不同段对上游“成功/完成输出”有措辞差异，未把它当成统一成功过滤保证。
- [O1 OpenClaw Session management](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md)：default reset、路由与恢复；普通 reset ID 的源码校正见[OpenClaw Session管理架构与机制](openclaw-sessions.md)第 4 节。
- [O2 OpenClaw Main session](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md)：Home、notices、background completion 与 heartbeat。
- [O3 OpenClaw Context engine](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/context-engine.md)：context assembly、engine/native ownership；pruning/compaction/flush 的独立来源见原笔记第 8–9 节。
- [O4 OpenClaw Session tools](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-tool.md)：spawn、send、继承、完成路由与访问边界。
- [O5 OpenClaw Command queue](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/queue.md)：session lane、busy input、输入持久化与队列恢复限制。
- [D1 Dot Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory)：assigned work、任务 conversation、selected context、notes、schedule、events 和 proactive research。
- [D2 Dot Controls](https://learn.chatgpt.com/docs/dots/controls)：Pause、delegated task stop 与 schedule cancel 的不同作用域。

## 8. 源码补核：外层 session 如何与 Codex/ACP 共存

### 8.1 本次确实检查的仓库与版本

按用户要求，将官方仓库浅克隆到 Workspace，只读研究，不安装依赖、不启动 Agent、不运行测试、不修改仓库：

| 仓库 | 本地目录 | 本次 HEAD |
|---|---|---|
| Hermes | `/Users/jjy/Workspace/hermes-agent` | `b7200efef01dce2097c3c5556843ff3cd2764789` |
| OpenClaw | `/Users/jjy/Workspace/openclaw` | `4ad3766a2169740ea369afd08f65a1db11ccbfb5` |

这些是 2026-10-02 克隆时的 HEAD，和前文固定研究快照不同；下文结论独立标记版本，不声称前文所有 defaults 已按新 HEAD 重新审计。

另直接读取 `codex-acp v2.1.1` 的 `CodexAcpClient.ts`、`CodexAcpServer.ts` 及 session-compaction 文档；未克隆第三个仓库，也未核验所有发布版本。补查 OpenAI App Server 官方 Threads 文档。[C1]

### 8.2 先区分两种接入，不是有 Codex 名称就有双层 thread

```text
调用模型 API：
  Hermes/OpenClaw agent loop → 模型服务
  外层负责工具循环与上下文；不能仅由 Codex OAuth 推定启动了 Codex CLI thread。

调用 Codex 原生 Agent：
  外层 runtime → Codex App Server → Codex thread/turn/tool loop
  或：外层 ACP client → codex-acp → Codex App Server
```

Hermes `conversation_loop.py:1622` 对 `api_mode == codex_app_server` 有明确分支，把整个 turn 交给原生 runtime 并返回；不是继续在同一轮上面再套一份普通 Hermes 模型/工具 loop。fallback 是另一条件路径，不等于两套 loop 同时执行。[S-H1]

**归纳**：多出外层 session 通常是为了入口路由、产品历史、任务/状态与事件管理；Codex thread 是真实执行上下文。是否使用两个推理助手要看实际调用链，不能从“有两种 session ID”推出“有两个主 LLM”。

### 8.3 Hermes 原生 Codex 路径：显式 ID 映射与 context owner

直接源码证据：

1. `agent/codex_runtime.py:502` 定义 `codex_thread_id`；`_stored_codex_thread_id` 从 Hermes session 的 `model_config` 读取，`_store_codex_thread_id` 写回。[S-H2]
2. 同文件 `run_codex_app_server_turn` 在 projected messages 已持久化且 thread 没有被退休时才发布绑定（第 714 行）。因此 Hermes session 与 Codex thread 是分别保存、明确关联的，不是两个独立索引碰巧共享同一份历史。
3. `CodexAppServerSession.ensure_started` 恢复已记录 thread 时调用 `thread/resume`，否则 `thread/start`；恢复响应 ID 不符则报错（第 279、302 行）。[S-H3]
4. 新 thread 可以把已有 Hermes 历史渲染为 history seed；恢复已有 thread 时不再注入该 seed，避免原生历史和外层历史重复灌入（第 284 行；准备 seed 见 `codex_runtime.py:609`）。
5. `run_turn` 通过已有 `threadId` 的 `turn/start` 发送当前输入（第 470 行）；Codex 事件被投影回 Hermes transcript/UI。显示历史副本不等于模型输入权威。

```text
Hermes session H
  ├── state.db：产品侧对话、投影消息与事件
  └── model_config.codex_thread_id = C
        → thread/resume C 或 thread/start C
        → turn/start C：当前输入
        → Codex 自己运行模型/工具循环
        → 事件投影回 Hermes
```

若保存的 thread 无法恢复，Hermes 清除旧绑定、显示诊断后创建新 thread，可用历史 seed 提供背景。**这不是精确恢复原 Codex 内部 context 的保证**；可见历史与原生运行状态可能不完全相同。

压缩源码给出非常明确的责任边界：

- `conversation_compression.py:4322` 的 `_compress_context_via_codex_app_server` 不摘要改写本地 transcript，而是调用 `codex_session.compact_thread()`，最终请求 `thread/compact/start`。[S-H4][S-H3]
- `native` 默认模式：外层不按自己的阈值自动发第二次 compact，由 Codex 自动处理；Hermes 记录其 compaction 事件。
- `hermes` 模式：Hermes 决定何时触发，但执行仍是 **Codex 原生 compact**，不是 Hermes 生成另一份替代摘要。
- `off`：关闭 Hermes 发起的自动 compact，不能推定关闭了 Codex 自己的压缩。
- 手动 force 可走原生 compact；调用返回不代表结束，transport 等待对应 turn/item 终态。

`_record_codex_app_server_compaction`（第 219 行）只更新边界/计数/usage，不改本地消息行。已有测试明确断言 native 模式不额外调用 `thread/compact/start`，thread/transcript 不分裂；本次只阅读该测试，没有执行。[S-H5]

### 8.4 OpenClaw 原生路径：binding + projection + Codex 原生压缩

源码中原生 Codex harness 是 `extensions/codex/src/app-server/`，不是 `codex-acp`。

- `session-binding-record.ts:34` 定义 binding owner：session 的 agent/session ID/key，或者 conversation binding ID；binding schema 保存 Codex `threadId`（第 147 行）。`bindingStoreKey`（第 358 行）生成外层 owner 的存储键。[S-O1]
- `thread-lifecycle-io.ts` 提供 resume/start 流程；使用已有 binding 恢复 Codex thread，并记录外层 session ID/key 与 thread ID。[S-O2]
- 组装后的 OpenClaw context 可以投影到 Codex；这不等于外层能直接替换 Codex 已存储的 model-visible history。
- 特别存在 `thread_bootstrap` 模式：`attempt-context.ts:68` 解析 epoch；同一 binding 的 engine/policy、epoch/fingerprint、工具 fingerprint 匹配时，决定 `project: false`，不重复 bootstrap（第 89 行）。这是可选模式，不能把它说成所有路径默认行为。[S-O3]
- epoch/engine binding 不兼容时，`thread-lifecycle-run.ts:501` 进入旧 binding 轮换流程。**这里 epoch 是源码真实支持的 projection 合约，不是本文新提的 pot 设计。**[S-O4]

原生压缩：`compact.ts:94` 默认对非 manual trigger 返回“Codex owns automatic compaction”，不重复走通用自动压缩；允许的原生请求最终通过 `thread/compact/start` 操作绑定 thread（第 526 行）。存在 `allowNonManualNativeRequest` 和 context-engine 特殊路径，故不能扩大成“任何自动路径绝不会发原生 compact”。[S-O5]

**归纳**：OpenClaw 保留外层 context assembly 与 session 管理，但原生 Codex 仍拥有它的 thread context。projection 的注入/轮换和压缩的执行归属，是专门协调的两件事。

### 8.5 OpenClaw ACP/acpx 路径：多一层协议身份，不等于多一份摘要器

这条链与上节原生 harness 不同：

```text
OpenClaw ACP runtime
  → extensions/acpx 的薄封装
  → 外部 acpx package
  → codex-acp ACP server
  → Codex App Server
```

已检查源码中的明确事实：

- `extensions/acpx/src/codex-adapter.ts:1` 指定 Codex adapter package 为 `@agentclientprotocol/codex-acp`。[S-O6]
- `extensions/acpx/src/runtime.ts:921` 的 ensure 流程处理逻辑 session target、命令、持久复用和 resume 信息，然后委托 `delegate.ensureSession`（第 1002 行）。不是此 wrapper 自己实现完整 ACP agent。[S-O7]
- 同文件第 561 行用记录的 `acpSessionId` 校验 handle 的 `backendSessionId` 与 generation owner，拒绝过期 handle 操作。外层 key、acpx record 与 ACP session 是不同定位符。
- 已读 `codex-acp v2.1.1` 的 `CodexAcpClient.newSession`（第 708 行）：调用 Codex `threadStart`，返回的 ACP `sessionId` **直接取 `response.thread.id`**（第 724 行）。不是 adapter 再创建一个独立模型 session。[S-C1]
- 其 `resumeSession`（第 599 行）把 ACP `request.sessionId` 作为 Codex `threadId` 调用 resume；`loadSession` 还涉及历史重放给客户端。UI history replay 不应被等同为重新把整段历史 prompt 给模型。

**重要命名区别**：上述 ACP `sessionId == thread.id` 是该 adapter 版本的映射。当前官方 App Server 文档中的 `thread.sessionId` 另表示 live session tree root，fork 后可能不同于 `thread.id`。不能把两种字段仅按名称当成同一含义。[C1]

codex-acp 将原生 compaction item 转为 ACP `compaction_update`，或兼容 synthetic tool-call。它不暴露可读 summary，也不抽取内部 replacement history/encrypted compaction data；接收通知不代表外层拿到了 Codex 的替代上下文全文。[S-C2]

**核对边界**：本次未继续克隆/审计外部 `openclaw/acpx` package 的全部 session store 实现，因此不宣称已端到端验证每种 ACP reset、重启、导入和恢复分支。已证实的是 OpenClaw wrapper 的 owner/handle 管理及 codex-acp 的原生 thread 映射。

### 8.6 对“双层管理”的准确回答

**可以有两层会话状态，但不应该据此理解为双方独立压缩同一个真实上下文。** 已查代码中的做法是：

| 责任 | 外层 Hermes/OpenClaw | Codex / adapter |
|---|---|---|
| 入口、产品对话/任务、路由、事件 | 保留外层 session 与存储 | 不因此取代 Gateway/产品状态 |
| 执行 session 定位 | 保存 binding、检查 owner/generation、处理恢复 | Codex 持有 thread；adapter 映射 ACP ID |
| 本轮输入 | 当前输入及适用 instructions/context projection | 追加到原生 thread，并执行 turn |
| 真正的原生 context 压缩 | 触发或观察原生 compact，避免只压本地副本 | Codex 执行 compaction |
| 旧历史展示/检索 | 可保留投影 transcript | 不等于当前模型 context 全量内容 |

因此要修正前一轮概括的适用范围：**Hermes/OpenClaw“根据 context 大小自己摘要”适用于其相应通用 loop；当接入 Codex 原生 runtime 时，会切换 context ownership，不能照搬普通压缩路径。**

以下是依据源码职责推导的风险，不是本次复现的缺陷：只压外层 DB 无法缩小原生 thread；每轮重复发送全量历史可能重复占用 context；外层 reset 未处理 backend binding 可能让旧上下文继续；仅删除投影记录不证明原生副本已删除。现有 binding/projection/native compaction 代码正是区分这些状态，但仍需要逐条分支测试才能宣称完整保证。

### 8.7 本次源码索引

以下固定到实际读取的 commit；本地仓库保持 clean。测试链接表示阅读其断言，不表示本次运行通过。

- [S-H1 Hermes conversation loop](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/conversation_loop.py#L1622)：整个 turn 交给 Codex runtime。
- [S-H2 Hermes thread binding](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/codex_runtime.py#L502)：ID 持久映射、历史 seed 和结果投影。
- [S-H3 Hermes Codex session transport](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/transports/codex_app_server_session.py#L260)：start/resume、当前输入与 native compact。
- [S-H4 Hermes compaction dispatch](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/conversation_compression.py#L4322)：native/hermes/off 与不改本地 transcript。
- [S-H5 Hermes native compaction test](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/tests/e2e/core/providers/test_native_codex_app_server.py#L186)：native 不叠加外层 compact 的测试断言。
- [S-O1 OpenClaw binding schema](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/session-binding-record.ts#L34)：owner、thread ID 和 key。
- [S-O2 OpenClaw thread lifecycle](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/thread-lifecycle-io.ts#L66)：外层 session 与原生 thread 的恢复/启动。
- [S-O3 OpenClaw bootstrap projection](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/attempt-context.ts#L68)：匹配 epoch/binding 时不重复投影。
- [S-O4 OpenClaw context binding rotation](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/thread-lifecycle-run.ts#L501)：不兼容 context-engine binding 的轮换。
- [S-O5 OpenClaw native compaction](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/compact.ts#L94)：自动压缩归属及 manual 原生请求。
- [S-O6 OpenClaw ACP adapter](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/acpx/src/codex-adapter.ts#L1)：adapter package。
- [S-O7 OpenClaw acpx runtime wrapper](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/acpx/src/runtime.ts#L921)：ensure/owner 与外部 package 委托。
- [S-C1 codex-acp v2.1.1 session mapping](https://github.com/agentclientprotocol/codex-acp/blob/v2.1.1/src/CodexAcpClient.ts#L708)：ACP session ID 取 Codex thread ID；resume 见第 599 行。
- [S-C2 codex-acp compaction contract](https://github.com/agentclientprotocol/codex-acp/blob/v2.1.1/docs/session-compaction.md)：通知投影、不输出内部 summary；它是 adapter 合约文档，不冒充本次已经审计完全部压缩事件实现。
- [C1 OpenAI App Server — Threads](https://learn.chatgpt.com/docs/app-server#threads)：start/resume、thread.id/sessionId 区分及 compact。

## 9. 继续源码核对：直接 API 与 OAuth

### 9.1 OAuth 不是 session 管理模式

API/Responses/App Server 是接口与执行路径；API key/OAuth 是认证方式。OAuth 本身不创建持久上下文、不加载 ChatGPT 聊天历史，也不能证明调用的是 Codex CLI agent。判断 session owner 必须看实际 transport 与 loop，而不是登录按钮。

### 9.2 Hermes 直接 Responses 路径

本次沿同一 `b7200efe` HEAD 检查：

- `agent/transports/codex.py:712` 构造请求：`instructions` + 由当前 `messages` 转换的 `input`，默认 `store: False`。默认组包不是只发送新消息、让 Codex CLI thread 自动补全过去内容。
- `agent/codex_responses_adapter.py:552` 把本地 chat-style messages 转成 Responses items；tool call/output、reasoning 等需要按 transport 合约保存和重放，不能只保留最后一句文本。
- `codex.py:732` 的 session ID 用于 cache scope/key 构造；**prompt cache key 不是 conversation ID**。它不替代本轮所需历史输入，也不让模型自动获得本地数据库全部记录。
- 通用 Hermes loop 保存会话、选择活跃历史、执行工具并追加工具结果，再次请求模型；不启动 `codex_app_server` 的这条路径没有原生 CLI thread 的那层状态。
- 本地压缩改变活跃 messages，下一请求因而带摘要/近期内容而非旧全文；数据库仍可保留软归档历史。

这说明直接 OAuth Responses 可以继续一个长期 Hermes session，但连续性来自 Hermes 携带的上下文与持久化，而不是 OAuth token。

### 9.3 直接 API 也可能使用服务端状态/压缩

不能把“直接 API”概括为“所有 API 永远完全无状态”：官方 Responses 支持手动 input-array chaining，也支持 `previous_response_id` 等状态连接方式。这不是 Codex CLI/App Server thread 管理的同义词。[A1]

Hermes 的可选 native Responses compaction 在 `agent/native_compaction.py:110` 检查配置、模型、路由和 checkpoint 等能力条件；启用时在请求中发送 `context_management`，返回 encrypted compaction item，后续通过本地 replay sidecar 携带它。服务端阈值安排在本地阈值之前，本地 compressor 保留 fallback；并非两个摘要器无条件同时压缩同一份历史。

默认不开启这项 opt-in native Responses compaction；不能与原生 App Server 默认 `native` 模式混淆。支持范围按当前代码能力判断，不因任意 OAuth endpoint 名称自动启用。

官方 server-side compaction 合约也说明：checkpoint 不可读；input-array 模式带 output items 续接，`previous_response_id` 模式只传新输入，后者不要手动 prune。[A2]

### 9.4 OpenClaw 当前 HEAD 的 OAuth 路径不能套 Hermes 假设

重要限定：上一节“模型 API vs 原生 Agent”是模式区分，不是说当前 OpenClaw 所有 API-key/OAuth 路径都使用它的通用 loop。

本次 HEAD 的 `extensions/codex/src/app-server/responses-oauth.ts:17` 判断 `chatgpt-token-sharing` flow，OpenClaw 持有 OAuth refresh/grant owner；`inference-routing.ts:455` 则把该 flow 的推理配置投影为 Codex 的私有 Responses provider。它仍位于 App Server 集成中，**不能因为是 OAuth/HTTP 就说这条路径没有 Codex thread**。

这里只确认这条具体 flow 的代码，不泛化为所有认证/模型/runtime。相同 OAuth 凭据，走 Hermes 直接 Responses 与走 OpenClaw 此 App Server route，会有不同 context owner。

### 9.5 补充证据

- [Hermes Responses request](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/transports/codex.py#L712)：input/messages、store 与 cache key。
- [Hermes message conversion](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/codex_responses_adapter.py#L552)：Responses items 重放与 checkpoint 条件。
- [Hermes native Responses compaction](https://github.com/NousResearch/hermes-agent/blob/b7200efef01dce2097c3c5556843ff3cd2764789/agent/native_compaction.py#L110)：配置/能力 gate 与阈值。
- [OpenClaw OAuth owner](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/responses-oauth.ts#L17)：具体 token-sharing flow。
- [OpenClaw OAuth inference route](https://github.com/openclaw/openclaw/blob/4ad3766a2169740ea369afd08f65a1db11ccbfb5/extensions/codex/src/app-server/inference-routing.ts#L455)：Codex provider 配置投影。
- [A1 OpenAI Conversation state](https://developers.openai.com/api/docs/guides/conversation-state#manually-manage-conversation-state)：input-array 手动续接；本次读取官方正文。
- [A2 OpenAI Server-side compaction](https://developers.openai.com/api/docs/guides/compaction#server-side-compaction)：服务端 checkpoint 与两种续接方式；本次读取官方正文。

本节同样只是源码与官方合约核对，未做真实模型调用，不新增 pot 设计。

相关：[Agent长期身份、记忆与长会话架构调研](identity-memory.md)、[本地个人Agent工作环境产品设计](../product-design.md)。本文作为事实比较单独保存，不为后者新增架构要求。
