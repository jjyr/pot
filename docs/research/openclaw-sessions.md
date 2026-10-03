---
tags: [OpenClaw, Agent, Session, Architecture]
---

# OpenClaw Session 管理架构与机制

整理日期：2026-10-02  
分类：技术 / Agent 运行时  
关联：[Agent长期身份、记忆与长会话架构调研](identity-memory.md)

版本范围：沿用既有调研的官方仓库快照 `a272cf0cd56eb9d0a1245daaaf139902ac32da01`。本文核对该快照的官方文档，并针对 reset 行为核对源码；未运行 OpenClaw 实测，不声称覆盖后续版本。文中架构图是机制示意，不是源码模块依赖图。

## 1. 核心模型：稳定的会话地址，有界的模型上下文

OpenClaw 的 session 管理由 **Gateway 统一持有状态**：先根据消息来源选择 agent 与 session，再排队运行，读取历史、组装上下文、调用模型和工具，保存结果并向原入口回复。[S1][S2]

要把六个概念分开：

| 概念 | 职责 | 生命周期与边界 |
|---|---|---|
| `agentId` | 选择 workspace、身份、配置和独立存储 | 一个 agent 可以有很多 session |
| `sessionKey` | 会话的路由地址，也是并发控制的基本分桶依据 | 如 `agent:main:main`；决定谁共享上下文 |
| `sessionId` | SQLite transcript 的身份 | 普通持久会话 reset 可以保留它，不能把它等同于上下文代际 |
| `lifecycleRevision` / reset 边界 | 标识当前生命周期和新的历史窗口 | 防止旧运行继续写入重置后的会话 |
| run / turn | 一次执行及其中的模型、工具活动 | 一次 run 不等于一份永久 session |
| model context | 本次推理实际收到的消息与指令 | 从历史中组装，受 token 预算约束 |

因此：**同一个聊天入口可以长期存在，但模型不需要每次读取全部历史。** 会话连续性依靠持久历史、上下文摘要和按需记忆召回共同提供。

## 2. ASCII 架构图

```text
  [Telegram / WhatsApp / Slack / other channels]
  [Control UI / Mobile / TUI / CLI / RPC clients]
                         |
                         v
  +-------------------- Gateway ----------------------------+
  | A. Authentication + ingress + deterministic routing     |
  |    bindings -> agentId -> dm/group/thread scope          |
  |    -> sessionKey + source/reply route                    |
  |                         |                               |
  |                         v                               |
  | B. Session lifecycle + admission checks                 |
  |    sessionId + lifecycleRevision + reset window          |
  |    reuse / reset / fork / archive / delete               |
  |                         |                               |
  |                         v                               |
  | C. Queue + execution ownership                          |
  |    per-session serial lane -> execution claim           |
  |    -> main / cron / subagent concurrency budget          |
  |                         |                               |
  |                         v                               |
  | D. Runtime + context assembly                           |
  |    history + bootstrap + recall + tools + current input  |
  |    context engine: ingest / assemble / compact / after   |
  |        |                    |                           |
  |        v                    v                           |
  | E. Bounded model view   F. Memory maintenance            |
  |    pruning / summary       private memory flush          |
  |        |                    |                           |
  |        v                    v                           |
  | G. Model/harness <-> Tools   [Workspace memory files]    |
  |    built-in / Codex / CLI     MEMORY.md, memory/*.md     |
  |        |                                                |
  |        v                                                |
  | H. Persist results + settle run + reply to source        |
  |                                                         |
  | I. Session tools -> child sessions / other sessions      |
  |    completion & coalesced notices -> requester / Home    |
  +--------------+-------------------------+----------------+
                 |                         |
                 v                         v
  +------------------------------+  +-----------------------+
  | Per-agent SQLite             |  | Archives / retention   |
  | openclaw-agent.sqlite        |  | sessions/ artifacts    |
  | - session rows / metadata    |  | age / entry / disk cap |
  | - transcript event tree      |  | archive, restore, GC   |
  | - reset / compaction markers |  +-----------------------+
  | - durable admitted input     |
  +------------------------------+

  Native harnesses may also own separate native thread/history state.
  Incognito uses process-memory storage rather than durable disk storage.
```

图中 D–G 有 runtime 差异：默认 context engine 的部分方法是 pass-through / no-op；原生 Codex 仍拥有自己的 native thread 和 compactor。不可据此认为所有后端都执行完全相同的压缩和记忆流程。[S6][S7]

## 3. A：消息路由与会话隔离

### 3.1 先选 agent，再选 session

普通消息通过配置的 `bindings` 匹配 agent：更具体的 peer/thread 规则优先于 guild、team、account、channel 级规则；选定 agent 后使用其 workspace 和 session store。路由是宿主的确定性逻辑，回复通常回到消息来源渠道。[S3]

**共享上下文与共享回复目的地是两回事。** 手机和 Slack 的 DM 可以进入相同 main session，但 Slack 的回复仍走 Slack。模型不负责自行猜测应该发到哪个渠道。

### 3.2 DM scope

| `session.dmScope` | 上下文共享方式 |
|---|---|
| `main`，默认 | 所有 DM 汇入该 agent 的 main session |
| `per-peer` | 按 sender 分开，可跨渠道归一 |
| `per-channel-peer` | 按 channel + sender 分开 |
| `per-account-channel-peer` | 按 account + channel + sender 分开 |

`session.identityLinks` 可以把同一个人的多个渠道身份映射到 canonical peer ID。它影响适用 scope 下的会话地址选择，不是授权凭据。[S1]

main 的 canonical key 是 `agent:<agentId>:main`；该快照忽略自定义 `session.mainKey`。Slack Agent/Assistant View DM 有额外 root thread 分桶，即使 `dmScope: main` 也不是全部折叠成一条历史。[S1][S2]

### 3.3 群组、线程与自动化入口

默认 `session.groupScope: per-group`，群组和房间各有历史；可用 `main` 或 binding 覆盖让指定群共享主会话。

```text
main:            agent:<agentId>:main
group:           agent:<agentId>:<channel>:group:<id>
room/channel:    agent:<agentId>:<channel>:channel:<id>
thread:          <base-key>:thread:<threadId>
Telegram topic:  <group-key>:topic:<topicId>
```

cron 每次运行使用新的执行会话；webhook 按 hook 隔离。具体 key 还受入口/runtime 影响，不能只靠一个字符串前缀推断权限。[S1][S3]

`session.scope: global` 也不会把不同 agent 的存储与执行责任合成一个 agent；操作仍保留选定 agent 的归属。

## 4. B：生命周期、reset、fork 与过期

### 4.1 默认持续，定时 reset 可选

当前快照默认 `session.reset.mode: none`，依靠 compaction 限制上下文增长。用户可以选择 daily、idle，或手动 `/new`、`/reset`。[S1]

| 策略/时间戳 | 判断机制 |
|---|---|
| daily | 根据本轮生命周期起点 `sessionStartedAt` 和 Gateway 主机本地的 `atHour` 判断；未显式设置时该小时为 4 |
| idle | 根据 `lastInteractionAt`，即真实用户/渠道交互，判断空闲时长 |
| 同时设置 daily/idle | 先过期者触发 |
| `updatedAt` | 元数据修改时间，用于列表、维护；不作为 daily/idle 的权威时间 |

heartbeat、cron 和 exec 通知不能靠更新元数据延长 idle 生命周期。daily/idle 的 rollover 在后续消息处理时判断，不能理解为到点必定立即启动模型。[S1][S5]

### 4.2 Reset 的真实边界：必须以实现为准

**文档存在口径不一致。** `session.md`、`main-session.md` 仍有 reset 更换 live session ID 的描述；schema 页明确 Gateway reset 保留 ID，但其 daily/idle 条目仍称更换 ID。[S1][S2][S5]

核对该快照源码后的可靠结论是：[C1][C2]

- 普通持久会话存在时，显式和隐式 reset 路径复用 `entry.sessionId`，追加 reset 边界，建立新的模型历史窗口。
- Gateway reset service 同时生成新的 `lifecycleRevision`，刷新生命周期起点，清理运行时状态和计数；模型等用户选择可按规则保留。
- 旧历史保留在存储里，但不会自动重新进入新窗口；历史搜索/锚定读取可以访问保留的旧内容。
- ACP 路径、特定重建/新建分支可以更换 ID；不能推广成“所有 reset 永远保留 ID”。

```text
sessionKey K -> sessionId S
               [older events ...]
               [reset boundary R1] -> current window starts here
               [new user / assistant / tools ...]

address K and transcript S can stay stable;
the reset window and lifecycle revision change.
```

这也是实现取消和防旧写入时不能只检查 `sessionId` 的原因：同一个 ID 背后可能已是另一轮生命周期。

### 4.3 Fork：子会话继承某个历史前缀

fork 新建子 session，父 session 保持不变。自动 thread/subagent fork 从 active branch 继承；过大时可退化为 isolated context。Operator 显式 fork 则按目标模型的输入容量检查，超限拒绝，不悄悄退化。[S5]

父会话运行中普通 operator fork 会被拒绝；`forkFrom: last-completed` 可以只复制最后完成的 assistant 消息之前的历史，排除尚未完成的尾部。消息级 fork 则从指定 user 消息之前分叉，并把该消息放回编辑器。

**fork 是新建会话，reset 是更换当前会话窗口，compaction 是缩减当前窗口的模型表示。**

## 5. C：并发队列、执行权与输入持久化

### 5.1 同 session 串行，不同 session 受预算并行

CLI、内嵌 runtime 和 Codex 都经过 session lane，再取得执行 claim。同一个 session 的两个 run 不能同时写入；不同 session 可以在全局/后台预算范围内并行。[S4]

普通入口使用 `agents.defaults.maxConcurrent`；普通子 agent 的并发预算归直接发起者/controller session，受 `agents.defaults.subagents.maxConcurrent` 控制。cron、heartbeat、Swarm 有相应的独立 lane，减少后台活动阻塞前台回复的情况。

运行和维护还需要检查会话身份、生命周期及当前 owner。reset、delete、取消后，旧运行不能继续对新状态提交写入。**取消不回滚已经完成的工具副作用或已经提交的 compaction。**[S6][C2]

### 5.2 忙时新消息怎么办

| Queue mode | 机制 |
|---|---|
| `steer`，当前默认 | 在 runtime 可接受的位置注入现有 run；不可 steer 时等待现有 run 结束 |
| `followup` | 每条消息进入后续 turn |
| `collect` | 静默窗口后合并为一次 followup；不同渠道/thread 仍保留投递路由 |
| `interrupt` | 中止该 session 的 run，再执行最新消息 |

默认 debounce 为 500ms、cap 为 20、溢出策略为 `summarize`。Steer 不意味着强行终止正在运行的工具：已启动的工具/batch 有各自的安全边界，Codex 在模型边界应用 steering。[S4]

输入合并与 delegation 保留原发送者的权限上限；不能因为共享 session 或后来来了权限更高的人就借用其权限。

### 5.3 已保存的输入不等于可自动重放的执行

普通 `chat.send` 对已有 session 的输入，会在 Gateway ACK 前写入 per-agent SQLite。collect 的合并 turn 和源输入 consumed 标记在同一事务内提交。[S4]

但是 **内存队列不在重启后自动重放**。尚未进入 transcript 执行的 queued input 会显示为 interrupted，需要显式重发；channel durable ingress 的重试另有 admission/dedupe 机制。

因此应分别检查：输入是否落盘、是否已被 turn 接纳、工具是否已完成、回复是否已投递。一个 accepted 状态不足以证明这四件事都完成。

## 6. SQLite：元数据与 transcript 两层存储

默认权威位置在 Gateway 所在主机：[S8]

```text
~/.openclaw/agents/<agentId>/agent/openclaw-agent.sqlite
    session rows:  sessionKey -> SessionEntry
    transcript:    messages / tool calls / results / boundaries / summaries

~/.openclaw/agents/<agentId>/sessions/
    legacy / archive artifacts
    sessions.json = legacy migration input
```

SessionEntry 记录 session ID、生命周期时间、模型/思考设置、归档/置顶状态、token 计数、compaction/flush 状态等。Transcript 是带 `id` / `parentId` 的追加式事件树，而非一串可以随意覆盖的消息。[S5][S8]

| 事件 | 用途 |
|---|---|
| `message` | user、assistant、toolResult |
| `custom_message` | 进入模型上下文的扩展消息 |
| `custom` | 扩展状态，不进入模型上下文 |
| `compaction` | 摘要及保留尾部的定位信息 |
| `reset` | 新历史窗口及可选保留起点 |
| `branch_summary` | 分支导航摘要 |

模型读历史和 UI 读历史可以使用不同视图，读取也会校验 admission、生命周期和当前访问权。通常使用有界尾部读取和专用 worker，避免为了最新一页加载全部消息。[S5][S8]

旧文章常见的 `sessions.json + JSONL` 不是这个快照的活跃运行时方案。旧状态需要停 Gateway、备份并通过 Doctor 显式迁移；启动不偷偷导入旧文件，也不应通过编辑 `sessions.json` 改活跃状态。

## 7. D–G：上下文组装与模型执行

### 7.1 Context engine 的四个生命周期点

| 方法 | 职责 |
|---|---|
| `ingest` | 新消息进入 session 时，允许 engine 存储/索引 |
| `assemble` | 返回预算内有序消息，可加 `systemPromptAddition` |
| `compact` | 上下文压力或手动请求时压缩 |
| `afterTurn` | 完成后的状态保存、索引和维护 |

可选 `maintain` 做 transcript 维护，`prepareSubagentSpawn` / `onSubagentEnded` 处理子 agent 边界。[S7]

默认 engine 名为 `legacy`：ingest/afterTurn 是 no-op，assemble 是 pass-through，由现有 runtime 的 sanitize → validate → limit 流程完成处理，compact 委托内置 summarizer。不要把插件 API 示意误读成默认 engine 自带一个独立历史数据库。

### 7.2 模型上下文来自多种资产

本次上下文包含适用的身份/规则文件、当前窗口历史、摘要、工具定义、当前输入及按策略召回的记忆。Bootstrap 文件有大小预算，skills 按需要加载。[S7][S10]

`MEMORY.md` 应只在私有主会话加载；群组/共享会话不能因为同属一个 agent 就默认注入私人记忆。main-session 页对“每个 fresh session 加载”的宽泛表述，应结合 workspace 页的隐私限定理解。[S2][S10]

对于 bundled Codex harness，OpenClaw 把组装结果投影到 developer instructions/current prompt，但 Codex 仍管理 native thread history 和 native compactor。切换模型/后端、跨客户端打开同一个 session，不等于复制所有后端的内部状态。[S7]

## 8. E：Pruning 与 Compaction 各解决什么

### 8.1 Pruning：缩减旧工具输出

Pruning 主要处理旧 `toolResult`，保留普通对话；客户端可以保留头尾 soft-trim，压力仍高时以 placeholder hard-clear。近期 turn 和 bootstrap 读取有保护规则。[S9]

客户端 projection 可通过隐藏的 `openclaw.cache-ttl` transcript marker 在重启后恢复；原始 toolResult 不被改写。直接 Anthropic API-key 路径可交给服务端 tool-result clearing，其他 eligible route 采用客户端策略，因此 TTL/阈值行为不能一概而论。

关闭新 pruning 不意味着旧 projection 立即消失；已有裁剪视图可继续重放，直到相关结果被 compaction 移出当前历史，或 session reset。旧图片 replay 清理是另一套模型视图处理，不等于删除原始图像。

### 8.2 Compaction：旧历史摘要 + 最近尾部

内置 compaction 概念上生成旧历史摘要，保存 compaction 事件，再保留近期尾部用于下一次调用。切点保证工具调用与结果成对，不把半个工具交互切开。[S6]

触发包括接近模型预算、provider overflow 后恢复、手动 `/compact`，以及配置的 active transcript byte guard。预算应包含 system prompt、工具 schemas、pending input 和输出预留；换一个更大 summarizer 不会增加前台模型的窗口。

新配置默认 safeguard 模式，关注 pending asks、精确 ID 和最终摘要质量。校验失败时保留原历史并报告失败。内置 summarizer 接收文本，图片会标记 omitted，不能假定截图细节已经进入摘要。

provider checkpoint、原生 Codex compaction、自定义 engine 的 successor identity 都可能有不同路径。内置 SQLite compactor保留当前 transcript 身份；原始历史保留不代表永远免于存储维护。[S6]

## 9. F：Memory flush、长期记忆与跨会话召回

**Flush 是压缩前的记忆整理机会；它与 compaction 摘要是两份资产。**

内嵌 OpenClaw runtime 在接近阈值时可以开一个 private detached view，让 agent 把重要事实写到 workspace 的 `memory/YYYY-MM-DD.md`。内部维护 prompt/reply 不进入原 transcript；写出的文件可以持久保留。`NO_REPLY` 用于抑制面向用户的维护回复。[S11]

flush 每个 compaction cycle 跟踪执行，默认启用；只读 workspace、CLI backend、heartbeat 等有跳过条件。可指定独立模型。flush 失败不等于 reset，也不能被当成重要事实已经保存的证明。

必要 preflight 在推理前执行；可选 post-turn 维护等待前台回复和 owner 结算后运行。新输入可以取消可选维护并等待其结算，防止维护与新 turn 同时写入。[S6][S11]

`memory.search.rememberAcrossConversations` 提供同 agent 其他私有会话的相关召回，**不合并 transcript**。个人 main 配置默认可启用；配置 DM isolation 后默认关闭，除非明确 opt in。默认 per-group 的群组不作为私有 recall 源，也不接收私有 transcript recall。[S1][S2]

会话路由、工具可见性、workspace memory 加载和跨会话 recall 是四个独立控制面，只改一个不能推导其他三个都隔离。

## 10. I：Session 工具、子任务与 Home 汇流

### 10.1 会话编排工具

| 工具 | 机制 |
|---|---|
| `sessions_list` / `session_status` | 看可见会话与当前状态；列表不是全部原始历史 |
| `sessions_history` | 读取指定会话；分页、工具结果开关和输出上限影响可见内容 |
| `sessions_search` | 在可见 transcript 中搜索，并返回用于历史定位的标识 |
| `sessions` | 修改设置、reset、delete、归档/恢复、分组等 |
| `sessions_send` | 向同 Gateway 的另一个 session 输入，按模式等待或通知 |
| `sessions_spawn` | 新建独立子 session，并返回 `runId` / `childSessionKey` receipt |

`sessions_send` 选择的是本地模型上下文地址，不是外部渠道发送对象；要向渠道发送，需要显式 channel/target 的 message 操作。[S12]

普通非 thread spawn 默认 isolated；原生 thread-bound spawn 的配置默认可为 fork。`context: fork` 有同 agent/native runtime 等限制。`visible: true` 可创建持久 dashboard session；并非所有 spawn 都是短命的隐藏 worker。

完成事件通常回报发起 session，但 collector、直接 thread 投递、quiet run 有不同 completion owner。Receipt 表示接受启动，不代表任务已经完成。

### 10.2 Home 如何获知其他活动

main/Home 是普通 session 加上 root 角色：[S2]

- 新建会话可加入 creation notice，在下次 turn/heartbeat 消费；创建本身不额外唤醒。
- 默认 per-group 下，群组活动以每个 conversation 合并的 compact notice 汇入 main。
- Home 发起的子任务回报 Home；其他发起者拥有各自回报路径。
- heartbeat 唤醒 main，使已累计事件在无人发消息时也能被处理。

这不是把全部群聊和 worker transcript 实时复制到 Home。通知队列是有界内存状态，notice/watch 也不自动授予读取权限。

状态变化另有 durable signal log；`stateVersion` 和 `changesSince` 可用于读取增量变化，超过保留历史时返回 `historyGap`。[S12]

## 11. 权限与 Incognito

Session tools 的 `tools.sessions.visibility` 有四档：[S12]

| 档位 | 范围 |
|---|---|
| `self` | 当前 session |
| `tree` | 当前和 spawned subtree；canonical main 有同 agent 全会话例外 |
| `agent` | 同 agent 的 sessions |
| `all`，当前默认 | Gateway 全部会话，跨 agent 还受 `tools.agentToAgent` 控制 |

实际访问还受工具 profile、allow/deny、sandbox clamp、用户身份/ACL 限制。默认 unsandboxed `all` 不是多用户隐私隔离方案；group watch、session owner/display name 本身都不能代替访问控制。

Incognito 是 Control UI 创建时选择的临时 session：session entry、transcript、compaction state 在进程内存中保存，创建后 24h 或 Gateway 重启就到期，活动不延长寿命。到期停止工作并清除，不归档；不运行自动 memory flush。[S1]

它从 cross-session tools/search 隐藏；但正常工具仍能写文件，显式保存的信息可以留在 session store 之外。原生 Codex 使用 ephemeral thread，模型提供方仍会处理请求。这是一种会话存储生命周期，不是限制所有外部副作用的 sandbox。

## 12. 重启恢复、归档与存储维护

### 12.1 重启恢复的是已开始的 turn

Gateway 重启中断 active turn 时，会尝试沿原 session 恢复，复用原 user input 和已记录的工具调用/结果。已完成回复或后续 user message 会关闭旧 turn 的 replay 资格。[S1]

连续三次未能启动 backend turn 会耗尽恢复预算；真正启动后才刷新预算。只是 accepted/queued/preparing 不算恢复成功。恢复耗尽时历史仍保留，用户可以选择新 session 继续。

这与第 5 节未执行的内存队列不同，也不构成所有外部工具操作都 exactly-once 的保证。未记录/未结算的外部副作用仍需依据具体工具确认。

### 12.2 维护分别控制年龄、条目数与物理空间

该快照的常见默认值：[S1][S13]

| 参数 | 默认 | 作用 |
|---|---|---|
| `session.maintenance.mode` | `enforce` | 执行维护；`warn` 只报告 |
| `pruneAfter` | `30d` | 普通 eligible durable 会话到期归档；可丢弃自动化行有删除策略 |
| `archiveDashboardAfter` | `7d` | dashboard 闲置自动归档 |
| `maxEntries` | `5000` | 未归档 session rows 的目标上限 |
| `maxDiskBytes` | `10gb` | 数据库、WAL 和 session artifacts 的物理预算 |
| `preserveRecent` | `false` | 可选近期交互保护窗口 |

超 entry cap 时，普通 eligible 会话优先归档，synthetic runtime 会话可删除；active/admitted work、pin、模型锁定和 durable external pointers 等有保护。因此 cap 是有保护条件的目标，不保证列表永远不超过上限。

磁盘维护先处理更便宜的 artifacts、未引用历史，并可先生成验证过的压缩 archive 再移除数据库历史。因 cap 归档的会话在进一步磁盘压力下可能成为清理对象；手动、age-retention、recovery 等受保护 archive 有不同规则。WAL 的实际占用也计入预算。

**归档、reset、compaction、pruning、delete 与长期记忆删除不能互换。** 归档影响活跃列表/生命周期；reset 改窗口；compaction 改模型表示；pruning 改工具结果投影；delete/GC 处理存储；记忆及其衍生来源需要单独管理。

## 13. 多客户端同步与 coding harness attachment

Control UI、mobile、TUI、Gateway-backed CLI 读取同一 Gateway 的 session 状态，不导出/复制 transcript。远程 Gateway 的 SQLite 在远程主机，查看本地目录不能判断远程会话状态。[S14]

`sessions.resolve` 负责 key、ID、label、short link 的解析，受可见性过滤；CLI/TUI 用 resolved canonical key 继续会话。

`openclaw attach <target>` 给 coding harness 发临时、session-scoped MCP grant；通常 harness 退出时撤销。共享链接只标识会话，终端仍须独立认证和满足 ACL。`tui --local`、`openclaw chat` 等 local embedded 模式不是同一 Gateway attachment 路径。

## 14. 一条消息的完整走向

以个人 agent 用户从 Telegram 询问，再从 Web 跟进为例：

1. Telegram ingress 认证来源，bindings 选 agent；默认 DM scope 路由到 `agent:main:main`，保留 Telegram reply route。
2. 检查会话是否存在、是否需 reset，取得当前 ID/生命周期/窗口。
3. 进入 session lane 和相应预算；已有 run 时按 steer/followup/collect/interrupt 处理。
4. 从 SQLite 读取模型窗口，加载适用 bootstrap、记忆和工具，必要时先做维护/compaction。
5. 模型运行工具 loop，写入消息与工具结果；回复走 Telegram，完成 run 结算。
6. 可选后台维护在 foreground owner 结束后运行；新消息到来时先取消并结算冲突维护。
7. Web 客户端选择同一个 main key，读取共享状态并跟进；它不会为此复制 Telegram transcript。
8. 后续历史增长时 pruning/compaction 缩减模型视图；用户 reset 则建立新窗口，旧历史仍可按权限检索。

最值得借鉴的结构是：**Gateway 掌握状态权威，稳定地址与生命周期分开，原始历史与模型视图分开，前台执行与后台维护有明确写入所有权。**

## 15. 证据与来源

以下全部固定到同一快照，便于回查；链接中的默认值不应直接套用到其他版本。

- [S1 Session management](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md)：scope、reset、recovery、incognito、maintenance。
- [S2 Main session](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md)：Home、group notices、background completion。
- [S3 Channel routing](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/channels/channel-routing.md)：agent 选择、session key、回复路由。
- [S4 Command queue](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/queue.md)：lane、队列模式、durable input 与重启限制。
- [S5 Session schema](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/schema.md)：ID、metadata、fork、reset/transcript events。
- [S6 Compaction](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/compaction.md)：摘要、预算、provider/runtime 差异、取消。
- [S7 Context engine](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/context-engine.md)：生命周期 API、legacy engine、Codex 投影。
- [S8 Session state on disk](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/store.md)：SQLite 两层存储、migration、history worker。
- [S9 Session pruning](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-pruning.md)：projection marker、server/client clearing、图片 replay。
- [S10 Agent workspace](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/agent-workspace.md)：bootstrap 和私人 MEMORY.md 的加载边界。
- [S11 Silent turns and memory flush](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/housekeeping.md)：private view、NO_REPLY、flush 条件。
- [S12 Session tools](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-tool.md)：可见性、编排、spawn、stateVersion。
- [S13 Store maintenance](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/reference/session-management-compaction/maintenance.md)：retention、cap、disk、保护规则。
- [S14 Session synchronization and attachment](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-attachment.md)：多客户端与 harness grant。
- [C1 `initSessionState` 源码](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/src/auto-reply/reply/session.ts#L805)：普通/ACP reset 的身份分支；第 997 行起创建 reset boundary。
- [C2 Gateway reset service 源码](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/src/gateway/session-reset-service.ts#L1040)：reset boundary；第 1101 行复用 ID，第 1122 行更新 lifecycleRevision。

后续验证重点：不同 backend 的 reset/compaction 实测、restart 中断与未结算工具副作用、同 agent 多用户下四个隔离控制面的组合效果。
