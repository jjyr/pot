---
tags: [Architecture, Agent, Hermes, Session]
updated: 2026-10-02
---

# Hermes Session 管理架构与机制

> 历史研究：版本与“当前”均指原文核对日期。实施现状见 [开发计划](../development-plan.md)，不是本报告中的建议或旧状态。

Hermes 把 session 管理分成三层：**入口路由决定消息进入哪个会话，持久存储保留会话身份和历史，context engine 决定模型本轮看到哪些内容。** Agent 对象是执行这些状态的运行时，可以缓存、释放或重建；它的生命周期不等于会话生命周期。

本文整理的是 NousResearch **Hermes Agent**，沿用 [此前调研](identity-memory.md) 的固定源码版本 `6dbb7b9ab857842617c2c35b63b49bd6ee77fc56`，于 2026-10-02 核对官方文档与源码。以下是源码与文档分析，未启动 Hermes 做运行实测，也不代表所有后续版本。

## 1. ASCII 架构图

图中是职责视图：CLI 与 Gateway 共用持久会话和上下文机制，但 CLI 不经过 Gateway 的聊天路由。

```text
 Telegram / Discord / Slack / ...                     CLI / TUI
                 |                                       |
                 v                                       v
 +--------------------------------+        +-----------------------------+
 | A. Adapter + RoutingIdentity   |        | New / Continue / Resume      |
 | profile, platform, chat,       |        | session ID / title / cwd     |
 | thread, participant            |        +--------------+--------------+
 +---------------+----------------+                       |
                 v                                        |
 +--------------------------------+                       |
 | B. SessionStore                |                       |
 | session_key -> session_id      |                       |
 | create / recover / reset /    |                       |
 | switch; persisted route state  |                       |
 +---------------+----------------+                       |
                 v                                        |
 +--------------------------------+                       |
 | C. Gateway turn coordination   |                       |
 | per-session turn lease         |                       |
 | load -> run -> flush           |                       |
 | pre-agent session hygiene      |                       |
 +---------------+----------------+                       |
                 +--------------------+-------------------+
                                      v
                     +------------------------------------+
                     | D. Agent runtime / prompt assembly |
                     | session prompt + memory snapshot   |
                     | live messages + per-turn inputs    |
                     | model calls <-> tools              |
                     +-----------------+------------------+
                                       |
                     +-----------------v------------------+
                     | E. ContextEngine                   |
                     | built-in ContextCompressor         |
                     | summary + anchors + recent tail    |
                     | or plugin / provider-native path   |
                     +-----------------+------------------+
                                       |
                 read / append / compact / usage / events
                                       |
             +-------------------------v-------------------------+
             | F. SQLite state.db (canonical session storage)   |
             | sessions | messages | messages_fts | routing      |
             | prompt snapshots, usage, recovery/commit state    |
             | live rows + soft-archived rows                    |
             +-----------+------------------------+-------------+
                         |                        |
                         v                        v
          +---------------------------+  +---------------------------+
          | G. session_search         |  | Display / export / prune  |
          | FTS discovery / scroll /  |  | history views & retention |
          | read / browse             |  +---------------------------+
          +------------+--------------+
                       | selected evidence -> Agent tool result

 +--------------------------------------------------------------+
 | H. Cross-session knowledge                                   |
 | SOUL.md / AGENTS.md | MEMORY.md / USER.md | skills / providers |
 | snapshot/context -> D; memory tools & review -> durable writes |
 +--------------------------------------------------------------+

 Gateway compatibility mirror: sessions/sessions.json
                               (routing mirror, not full history)
```

## 2. 先分清五种状态

| 状态 | 表示什么 | 生命周期与边界 |
|---|---|---|
| `session_key` | 消息入口的确定性路由地址 | 同一个聊天入口通常不变；`/new` 后仍可保持原 key |
| `session_id` | 一次持久对话的身份 | 默认压缩、重启和恢复保持 ID；显式新会话换 ID |
| `SessionEntry` | key 当前指向的会话及路由状态 | 持久化活动时间、恢复标记、model override、prompt pin 等 |
| Agent runtime | 执行模型与工具循环的对象 | 可以缓存或重建；释放它不自动结束持久会话 |
| active context | 当前交给模型的消息投影 | 压缩后缩小；不等于数据库里存着的全部历史 |

例子：Telegram DM 的 key 是 `agent:main:telegram:dm:123`。第一次聊天创建会话 S1；重启 Gateway 后继续 S1；压缩后仍是 S1，但旧消息退出 active context；执行 `/new` 后，同一个 key 改为指向 S2。

注意：`parent_session_id` 不足以单独判定“上下文应当继承”。旧式压缩、分支和部分 reset 路径都可能记录父关系；恢复和检索还要判断关系类型与结束原因。

## 3. A：入口身份与隔离

Gateway 先解析消息来源，再由 `build_session_key()` 生成 key。[S2][S3]

| 场景 | 典型 key / 规则 | 机制与效果 |
|---|---|---|
| 默认 profile | `agent:main:...` | 保持历史 key 兼容 |
| 命名 profile | `agent:<profile>:...` | 同一聊天由不同 profile 执行时隔离；名为 `main` 的 profile 使用 `main~` 避免冲突 |
| DM | `agent:main:<platform>:dm:<chat_id>` | 默认每个 DM 一条会话；缺 chat ID 时优先用 sender ID |
| 普通群/频道 | `...:<chat_id>:<user_id>` | 默认 `group_sessions_per_user: true`，同一房间内按参与者隔离 |
| thread/topic | `...:<chat_id>:<thread_id>` | 默认共享该 thread 的历史；`thread_sessions_per_user` 可进一步隔离 |
| Slack | key 中加入 workspace/scope ID | 避免不同 workspace 的聊天标识碰撞 |
| WhatsApp | canonical identifier | 有映射时把电话号码/LID 别名归一，避免同一人被拆成两条会话 |

路由身份还区分“接收消息的 bot/profile”和“执行 turn 的 runtime profile”：一个多 profile Gateway 可以转派执行，但不能因此让其他 bot 错投递结果。

这是 **transcript 隔离**，不能直接推出长期记忆也按用户隔离。同一个 Hermes home 的 `MEMORY.md`、`USER.md` 有自己的共享范围；多用户产品必须另外设计这一层。[S8]

## 4. B：创建、延续、重置与恢复

### 查找或创建

`get_or_create_session()` 对同一个 key 使用 single-flight：并发的 lookup/create 共享一次结果，避免重复创建 ID 与数据库记录。它读取路由、检查会话是否已结束或被删除、追踪旧式压缩的最新 successor，必要时从数据库恢复，然后创建或更新路由。[S2]

`touch_activity=False` 允许内部事件更新状态而不伪装成用户活动。这会影响重启自动续跑的新鲜度判断与历史清理。

### 显式边界

```text
 no route -- first message --> S1
 S1 -- user message / gateway restart --> S1
 S1 -- default in-place compression --> S1 (smaller live context)
 S1 -- /new or /reset --> S2 (same routing key, fresh context)
 S2 -- /resume S1 --> S1 (route switches to stored conversation)
```

`reset_session()` 创建新 ID，替换 key 的目标，并把旧会话记录为 `session_reset`。新上下文不把旧聊天全文自动带进来；长期记忆和按需历史检索负责跨边界延续。reset 前有记忆/skill 保存机制，但它依赖模型、开关和写入审批状态，不能视为每次必定成功。[S1][S2][S8]

`switch_session()` 用于恢复已有会话：旧行结束，目标行重新打开，入口重新绑定。提供 `expected_session_id` 时使用 compare-and-swap；如果等待期间用户已经 `/new` 或 `/resume`，旧异步操作不能覆盖新路由。

### 当前核心不做时间重置

本快照的 Gateway **不因空闲、跨日或进程重启自动换 session**。核心忽略旧 `session_reset` 时间策略；需要 idle/daily/both 规则时，文档提供 `hermes-session-reset-policy` 插件。[S1][S4]

`suspended` 是另一回事：代码用它标记明确暂停后的替换，下一次 lookup 可新建会话。`/stop` 相关路径会使用这一机制来打断卡住的恢复循环，因此不能把 `/stop` 简单解释成“只取消一次 API 调用且总会保留当前 ID”。

### 崩溃与重启

恢复会综合路由、来源和实际活动选择持久会话；有意 reset 是恢复边界，不能越过它复活旧对话。已结束行只有满足恢复条件才重新打开，明确 reset 与删除不能一律当作普通断线处理。[S1][S5]

执行中的 turn 留下 `active_turn_token`、开始时间等标记；正常退出按 token 清除，异常退出留下证据。`resume_pending` 表示应沿原 ID 继续，与 `suspended` 的替换语义不同。

自动续跑有独立 freshness 窗口，源码默认一小时。**窗口限制的是重启后是否主动继续执行，不是历史保留时长**；用户后来发消息仍可加载原对话。

## 5. C：turn 协调与并发保护

会话状态容易在“加载历史—执行—写回”之间竞争，因此锁不只保护单次数据库操作。[S6]

| 保护 | 作用范围 | 解决的问题 |
|---|---|---|
| key single-flight | 同一 Gateway 内同一 key 的 lookup/create | 同时到达的消息不会各自创建一个新会话 |
| per-session turn lease | 同一 Gateway 进程内，按解析后的 `session_id` 串行执行 load/run/flush | 两个不同入口恢复到同一 ID 后，不会同时用同一份旧历史执行并交错写回 |
| token / generation 检查 | turn 清理、路由切换、prompt pin 更新 | 旧任务的迟到清理不能释放或覆盖新任务的状态 |
| 压缩 snapshot / commit fence / watermark | 摘要计算与提交 | 中断或过期的摘要不能直接覆盖活跃消息；摘要开始后追加的消息需保留 |
| SQLite WAL、事务与写入重试 | 持久存储 | 支持并发读，串行写，并处理暂时写入失败 |

`SessionTurnLeaseRegistry` 明确是 **进程内、单 event loop** 的协调器，不是跨所有 CLI/Gateway 进程的分布式锁。数据库的压缩协调和事务有自己的保护，也不能据此认为多个独立 agent 共用一个 home 完全安全；官方仍建议 one agent per Hermes home。[S6][S8]

## 6. D：prompt、记忆快照与运行时缓存

session 保存 system prompt snapshot。模型实际输入由稳定 prompt、当前 live history 和本轮明确注入的内容组成，而不是每轮读取所有历史。[S1][S8]

- `SOUL.md` 提供实例身份，`AGENTS.md` 提供项目规则。
- `MEMORY.md` / `USER.md` 在会话开始时作为冻结快照进入 prompt，默认预算分别为 2,200 / 1,375 字符。
- `memory` 工具写入持久文件后，当前工具响应能看到更新，但不会自动替换正在使用的初始记忆块。
- `prompt_pin` 保存最近人类 turn 的 session/channel 动态 prompt 输入，供内部 turn 复用；这份 pin 持久化，避免重启或委派回调突然改变入口语境。[S7]
- 本轮 message ID、语音状态等易变信息走当前消息，减少不断改变 prompt 前缀造成的 cache miss。

需要区分三类缓存：Agent 对象缓存用于减少初始化；冻结 prompt 用于稳定上下文；provider prompt cache 用于降低重复前缀处理成本。任一缓存失效都不等于会话历史丢失。

这也解释了 `/new` 的价值：创建一个自然边界，让新的核心记忆和规则配置进入下一条对话，减少长期携带旧上下文的成本。重启机器本身不是这个边界。

## 7. E：上下文压缩

### 两层维护

| 层 | 时机 | 触发依据 | 职责 |
|---|---|---|---|
| Gateway session hygiene | 启动 Agent 前 | 常规阈值为窗口的 85%，也有硬消息数保护 | 接住超长历史，减少 API overflow |
| Agent ContextCompressor | 模型/工具循环内 | 实际 usage + 新增消息估算；阈值可配置 | 日常上下文管理 |

Agent 的配置默认 `threshold: 0.50`，但本快照对小于 512,000 token 的窗口设 0.75 的比例下限，还有部分 Codex OAuth 85% 特例、绝对 token cap 和辅助模型可行性限制。**实际触发点取决于解析后的模型、窗口与配置，不能把 50% 当作统一运行阈值。** Gateway 85% 与 Agent 内部规则也不能混为一个。[S9][S10]

usage anchor 用 provider 最近返回的 prompt/completion token 加上随后新增消息的估算；该 anchor 可持久化并在恢复时验证。没有可靠 anchor 时，部分普通预检会先等待一次 provider usage；Gateway hygiene 保留粗估后备路径，实际 overflow 有有界恢复尝试。

### 默认 in-place compaction

```text
 Before, session S1:
   [head] [long middle: user / assistant / tools] [recent tail]

 After, same session S1:
   LIVE:     [head] [summary + anchors + recovery pointers] [tail]
   ARCHIVED: pre-compaction rows, retained for history/search
```

默认 `compression.in_place: true`：重写同一 ID 的 live message list，旧历史软归档，归档内容仍可供历史展示与 `session_search` 恢复。语义是 **缩小模型输入**，不是删除数据。[S9]

`in_place: false` 才启用旧式 rotation：创建新 ID，用 `parent_session_id` 连接压缩前后段，并重新指向最新段。部分概览文档仍把“压缩生成子 session”写成一般行为，应以本版本的模式配置与实现为准。

### 摘要和尾部如何保留连续性

内置 compressor 先处理旧工具输出，划分待摘要区域与受保护尾部，再生成目标、约束、进展、决定、文件、下一步等结构化摘要。边界对齐工具调用与结果，避免留下孤立 tool result。

默认 `lean` 策略用主模型窗口的约 2.5% 选尾部，预算按 10K–25K clamp，并受小窗口的 20% cap 等约束；这不是最终上下文总量的硬上限。连续性还由精确标识 anchor、预算内的用户原话和历史恢复指针承担。旧摘要后续会更新，而不只是不断堆叠摘要。

失败有冷却、有限重试和提交保护；依错误类型与配置，可能保留原历史并中止，或提交确定性 fallback。不能保证摘要永远完整，也不能把原始消息仍在数据库里理解成模型当前已理解所有内容。

### 可插拔与 native ownership

`ContextEngine` 抽象负责判断压缩、执行压缩与统计 token；插件通过 `context.engine` 显式启用。内置摘要路径以外还存在 provider/runtime 原生压缩：[S9]

- Codex app-server 拥有真实 thread context，Hermes 应请求它原生 compact；只改本地镜像无法缩小真实 thread。
- 某些支持的 Responses API 路径可以 opt-in 服务端 compaction，保存 opaque checkpoint 供后续 replay。

因此判断压缩机制时，必须先确定谁拥有模型上下文。

## 8. F：持久化、视图与存储清理

| 存储 | 主要内容 | 地位 |
|---|---|---|
| `$HERMES_HOME/state.db` | session metadata、messages、FTS5、usage、prompt 与相关状态 | 持久会话的权威存储 |
| `gateway_routing` 表 | key -> 当前 ID、origin、恢复等路由状态 | Gateway 路由权威 |
| `sessions/sessions.json` | 路由的兼容镜像 | 不是所有会话列表；可关闭写入 |
| `sessions/saved/*.json` | 用户保存的方便快照 | 导出，不是路由索引 |
| `memories/*.md` / skills | 策展后的长期知识与程序 | 独立于完整 session transcript |

多 profile 时，Gateway 路由索引属于 Gateway 自身 home，执行 profile 的 transcript DB 按 owning profile 解析，不能把所有消息写到一个启动时固定的 DB handle。[S11]

读取历史失败与“确实没有历史”必须区分。`load_transcript()` 在 canonical read 失败时抛出 `TranscriptReadError`，拒绝用空列表开启新推理，避免貌似正常但实际上失忆。[S12]

文档说旧 JSONL 不再作为正常存储；代码仍保留 DB 不可用时的部分降级分支。这表示正常路径以 SQLite 为准，不应把遗留文件当作恢复权威，也不应保证数据库不可用时所有功能仍完整。

### 保留与清理

本快照文档默认 `sessions.auto_prune: true`，对已结束且足够久未活动的会话执行启动时清理，默认 retention 为 90 天；活跃 Gateway 会话不会因为年龄大直接被删除。旧式压缩 lineage 一起判断保留与删除，pin 与活跃执行有保护。[S1]

`VACUUM` 用于回收删除后的磁盘空间；`optimize` 是存储维护；这些都不是模型 context 压缩。部分死亡自动化会话先被标记结束，之后才进入后续保留周期。

不同导出视图也不同：Markdown/HTML 等展示导出可含软归档历史，而用于恢复 live context 的 JSON/JSONL 导出有 live-only 规则。备份或审计前需要核对导出格式。

## 9. G/H：历史检索与跨会话学习

`session_search` 查实际存储消息，不调用 LLM 生成摘要。它支持四种形态：[S1][S13]

| 输入 | 行为 | 适用场景 |
|---|---|---|
| `query` | FTS5 搜索，按 lineage 去重，返回匹配窗口 | 找曾经讨论过的问题 |
| `session_id + around_message_id` | 围绕 anchor 前后滚动 | 补读检索结果周围的证据 |
| `session_id` | 读取会话，长会话提供有界视图 | 回看某个任务 |
| 无参数 | 浏览最近会话 | 找最近在做什么 |

默认检索以 user/assistant 为主；调试工具时可把 tool role 加进过滤条件。搜索结果和恢复片段进入工具响应，只有被取回的相关信息才重新占据当前模型 context。

长期学习则把 session 中值得长期保留的内容写入 memory/skills，post-turn review 和 session 边界都可参与。它们可能关闭、预算受限或写入待审；“回顾已运行”与“新知识已写入”应分开判断。[S8]

三种延续方式分别是：

1. **继续 session**：保留当前任务状态与活跃上下文。
2. **新 session + 核心记忆**：用很小的稳定知识开始下一项任务。
3. **按需检索历史**：把当前需要的具体证据取回来。

## 10. CLI 恢复与跨入口交接

| 操作 | 机制 |
|---|---|
| `hermes -c` | 优先按终端 breadcrumb 恢复本终端会话，无有效记录时回退最近 CLI 历史 |
| `hermes --resume <id/title>` | 定位持久会话；旧式压缩 lineage 通常追踪到最新段 |
| `hermes --resume latest --in ./dir` | 恢复指定 workspace 的最近会话，并固定目录 |
| 普通 resume | 默认恢复记录的工作目录，`--no-restore-cwd` 可关闭 |
| `/sessions` | 从 SQLite 提供会话选择器，不读 sessions.json 充当完整列表 |
| `/handoff telegram` 等 | Gateway 接管交接请求，把目标入口绑定到既有 session ID，而不是复制成无关新对话 |

handoff 要求目标平台/home channel 配好，并在当前 turn 结束后发起；平台支持时创建新 thread/topic，随后生成一次确认与总结 turn。Gateway 不运行、创建 thread 或发送失败都有对应失败/降级处理。无 thread 的多人 home channel 存在路由限制，不能假定和单人 DM 一样工作。[S1]

## 11. 与个人 Agent 产品设计相关的结论

- 用户可见的聊天时间线可以持续，但内部应区分持久会话、模型 context 和知识更新边界。
- 采用“小核心记忆 + 原始历史 + 检索”时，当前任务仍需要明确的工作状态；不能只靠长期记忆替代任务上下文。
- 压缩应保留原始证据、精确标识与恢复路径，同时保证迟到的压缩结果不覆盖新消息。
- 多入口要按解析后的 conversation ID 协调执行，不能只按入口 key 加锁。
- 若 UI 设计为永久主聊天，需要另外设计记忆刷新或 execution epoch；不能把“用户主动 `/new`”当作唯一更新路径。这里的 epoch 是设计建议，不是 Hermes 已有机制。

## 来源

以下全部链接固定到同一源码快照，可直接复核。文档中的概览、算法示例与现行实现有少量不同，本文对 defaults、路由与边界优先采用具体实现。

- [S1 Sessions](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/sessions.md)：恢复、交接、搜索、路由、连续性、存储、清理。
- [S2 gateway/session.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session.py)：`SessionEntry`、`build_session_key`、lookup/create/reset/switch。
- [S3 gateway/session_identity.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_identity.py)：profile 与 transport/runtime 身份。
- [S4 gateway/session_lifecycle.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_lifecycle.py)：显式 suspension、turn 标记、resume freshness。
- [S5 gateway/session_recovery.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_recovery.py)：恢复判定与重开。
- [S6 gateway/turn_lease.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/turn_lease.py)：进程内按 session ID 串行 turn。
- [S7 gateway/session_prompt_pin.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_prompt_pin.py)：持久 prompt pin 与 fencing。
- [S8 Persistent Memory](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md)：冻结快照、核心预算、review 与写入策略。
- [S9 Context Compression and Caching](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md)：双层压缩、in-place、lean、native ownership。
- [S10 agent/context_compressor.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/agent/context_compressor.py)：阈值解析、摘要、软归档与提交保护。
- [S11 gateway/session_persistence.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_persistence.py)：SQLite 路由权威、JSON 镜像与 profile DB 选择。
- [S12 gateway/session_transcript.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/gateway/session_transcript.py)：消息追加、重试、load/rewind。
- [S13 tools/session_search_tool.py](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/tools/session_search_tool.py)：实际消息检索与有界读取。

相关：[Agent长期身份、记忆与长会话架构调研](identity-memory.md)、[本地个人Agent工作环境产品设计](../product-design.md)。
