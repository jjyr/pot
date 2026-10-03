---
tags: [Agent, Architecture, Memory, Research]
---

# Agent 长期身份、记忆与长会话架构调研

研究日期：2026-10-02  
状态：资料调研与架构建议；未运行各产品的对照实验，不是实现规范。  
关联产品：[本地个人Agent工作环境产品设计](../product-design.md)，尤其 Agent 部分的第 4.4–4.5 节。  
环境研究：[Docker容器GUI与可嵌入运行时调研](docker-gui.md)、[Apple container能力架构与使用调研](apple-containerization.md)。

本文的 **ChatGPT Dot** 指 OpenAI 产品；**Meta Muse** 指 Meta 产品；**本产品**指我们正在设计的 **pot** 本地个人主 Agent 产品，避免三个名字混用。

## 1. 先回答两个核心问题

### 1.1 长期身份不是一份不断变长的 system prompt

建议拆成四层，而不是把它们全部交给会话历史：

1. **产品身份与行为契约**：稳定 agent ID、名字、职责、风格，以及哪些规则由用户设置、哪些不能由外部内容改变。
2. **用户模型与长期记忆**：偏好、关系、项目背景、事实、经历和技能；必须可修正、可删除、有来源和适用范围。
3. **长期责任与任务状态**：正在承担什么、何时继续、进度、具体外部对象 ID、是否已经产生副作用。这里需要状态机，不只是自然语言记忆。
4. **执行会话**：本次模型推理、工具调用、上下文压缩和运行中的短期状态。可以重建、更换引擎，不应成为人格和所有重要事实的唯一载体。

身份的连续性来自这些持久资产，而不是模型权重会在每次聊天后改变，也不是永远保留一个进程。

### 1.2 长会话最好是「长期关系 + 有界执行上下文」

用户可以一直在同一个主聊天窗口里说话，但每次模型调用只接收经过选择的上下文。主时间线、侧话题、任务执行会话、长期记忆可以有不同生命周期。

**长期主会话 ≠ 无限上下文 ≠ 所有任务共用一个执行 session。**

OpenClaw 的 rolling main session、Hermes 的记忆快照与会话边界、ChatGPT Dot 的独立任务上下文、Muse 的 main chat 与 side chats，都提供了不同方向的证据。[O3][H1][D1][M2]

### 1.3 目前最值得试验的方案

**轻量混合，但尚不定案：产品拥有稳定身份、用户明确设定与关键业务状态；Codex 等引擎负责推理和执行 session；自动经验记忆逐步加入。**

这不要求首版必须有「一个我们自建的 LLM 主 Agent，再加一个 Codex 子 Agent」。同一个 Codex 引擎可以承担主 Agent 的推理，产品层提供持久身份、记忆服务、调度与 GUI。主 Agent 是产品职责，不等于必须自研整个 agent loop。

但有两项不能靠架构图确认：我们能否可靠地向所选引擎注入每轮变化的状态；引擎的本地记忆能否在关闭、纠正和遗忘时受到我们需要的控制。应以固定版本原型验证。

## 2. 证据范围与可信度

| 对象 | 本次实际读取的材料 | 能确认什么 | 不能确认什么 |
|---|---|---|---|
| OpenClaw | 官方仓库固定快照中的 workspace、memory、main session、compaction、provenance 等文档 | 文件层、运行时存储、检索、会话与维护的公开设计 | 全部代码路径的正确性、我们场景的实测效果 |
| Hermes Agent | 官方 memory 页面；固定快照的 personality、memory providers、compression 文档 | 小型核心记忆、冻结快照、历史搜索、扩展 provider 与压缩设计 | 各 provider 等价性、宣传中的性能和学习质量 |
| Codex | OpenAI 官方 memories、App Server、AGENTS.md 文档 | 本地记忆生命周期和公开集成接口 | 托管服务全部内部实现、ACP 与原生功能完全等价 |
| Claude Code | 官方 memory、运行方式文档 | 指令、自动记忆、项目范围、session 与 compaction | 这些机制可直接代表消费者 Claude |
| 消费者 Claude | 官方帮助中心当前记忆与历史搜索说明 | 用户控制、topic memory、项目边界、当前与 legacy 的区别 | 内部存储 schema、检索排序和完整压缩算法 |
| ChatGPT Dot | 官方 Tasks and memory | 自有 notes、ChatGPT memory、任务上下文、后台协作的公开行为 | notes schema、召回算法、主会话内部滚动策略 |
| Meta Muse | 官方 personality/memories 与 data management 帮助 | 文件式身份管理、主/侧聊天、记忆编辑和删除边界 | 内部 agent loop、是否复用某开源项目、内部 context 算法 |

OpenClaw 快照：`a272cf0cd56eb9d0a1245daaaf139902ac32da01`。  
Hermes 快照：`6dbb7b9ab857842617c2c35b63b49bd6ee77fc56`。

文中「已确认」指材料明确描述的能力，不等于本次已实测。来源链接在末尾；工程设计建议明确标为建议。开源项目快速迭代，不能把早期文章中的默认值、JSONL 路径或 session reset 行为当作当前事实。

另外，Codex 的执行核心有开源部分，不宜简单归入「完全闭源产品」。这里比较的是公开可验证的客户端能力与未公开的托管产品内部机制。

## 3. 先统一概念：哪些状态不能混在一起

| 层 | 例子 | 权威来源 | 进入模型的方式 |
|---|---|---|---|
| 技术身份 | agent ID、environment ID、owner ID、节点证书 | 产品注册/配对系统 | 需要时提供标识；密钥不进入 prompt |
| Persona / 行为契约 | 名称、语气、职责、诚实与不确定性表达 | 用户设置与产品配置 | 小型稳定指令 |
| 用户模型 | 中文沟通、时区、偏好、工作关系 | 用户陈述与明确标记的推断 | 核心摘要 + 按范围检索 |
| 语义记忆 | 项目采用 PostgreSQL；某决策及理由 | 带来源、时间的事实记录 | 按相关性召回 |
| 情景记忆 | 昨天修复故障、当时发生了什么 | 事件日志、会话与产物 | 摘要 + 原始证据引用 |
| 程序性记忆 | 某种排障流程、服务集成操作方法 | skills、验证过的流程 | 按需加载 |
| 未来责任 | 每周检查报告、某事件发生后继续工作 | scheduler / intent / goal 记录 | 唤醒时加载对应责任 |
| 工作记忆 | 当前计划、最近工具结果、屏幕状态 | 当前 execution session | 最近上下文与检查点 |
| 真实外部状态 | 邮件是否发送、PR 是否创建、OAuth 是否有效 | 外部服务/运行时 | 当下查询；不能仅凭旧记忆 |

三个常见混淆：

- **记得一个账号 ≠ 持有该账号的授权。** OAuth token、浏览器 cookie 是凭据，不是普通长期记忆。
- **记得做过某操作 ≠ 当前仍然是那个状态。** 文件可能被用户改掉、token 可能失效、PR 可能已关闭。
- **摘要 ≠ 事实数据库。** 摘要为理解服务，不能承担去重发送、交易结果或控制权等精确状态。

## 4. OpenClaw：长期个人主会话与分层记忆

### 4.1 身份和记忆文件各司其职

官方 workspace 文档区分：[O1][O2][O4]

| 文件 | 职责 |
|---|---|
| `IDENTITY.md` | 名称、气质、emoji 等身份描述 |
| `SOUL.md` | 人格、语气、边界 |
| `AGENTS.md` | 工作规则、如何使用记忆等运行指导 |
| `USER.md` | 用户偏好、沟通风格、关系与活跃项目的紧凑用户模型 |
| `MEMORY.md` | 精选的长期非 profile 事实、决策与短摘要 |
| `memory/YYYY-MM-DD.md` | 日常经历与详细观察；需要时检索 |
| `DREAMS.md` | 可选的后台整理与回顾记录 |
| skills | 可复用操作知识，不把全部技能正文常驻上下文 |

`MEMORY.md` 不是全量日志，也不是无限档案。workspace 文档建议私有主会话加载，避免在群组和共享场景注入私人长期记忆。近期日记在新建/重置时重新提供，详细档案按需搜索。

当前 `USER.md` 有独立的小预算；文档中的个人 USER 文件上限为 4,000 字符。重要的是「核心上下文有预算」，不是照搬这个数字。

用户偏好变化应把旧 directive 标为 superseded，而不是永远追加互相矛盾的规则。多用户场景还区分共享默认与个人文件，并以认证的人类身份选择 profile；显示名不能充当身份凭证。[O4]

### 4.2 文件不是全部运行时

当前 workspace 文档明确区分：

- workspace 中的人类可读身份与记忆；
- `~/.openclaw/agents/<agentId>/agent/openclaw-agent.sqlite` 中的运行状态，包含会话、transcript、索引和其他 agent 状态；
- native Codex 的独立 `codex-home` 状态；
- 旧版目录、JSONL 和 sidecar 的历史/迁移用途。[O1]

因此「OpenClaw 只是几个 Markdown 文件 + sessions.json」不是准确的当前架构描述。workspace 默认 cwd 也不等于硬沙箱。

### 4.3 一个主会话可以持续，但模型上下文仍然有界

个人配置默认把多种渠道的直接消息汇入 `agent:<agentId>:main`，Home 是用户面对的主入口；群组、侧线程和 coding 会话有各自入口。[O3][O5]

当前默认是**没有自动 reset**，靠 compaction 维持 rolling conversation；daily/idle reset 是可选策略。概念文档对 `/new`、`/reset` 更换 live session ID 的表述与该快照实现存在差异：核对源码后，普通持久会话 reset 会保留 transcript ID，追加 reset 边界并更新生命周期版本；ACP 等路径另有规则。既有历史保留检索连续性，详见[OpenClaw Session管理架构与机制](openclaw-sessions.md)第 4 节及其固定源码引用。

这和「每天 4 点必定重置」这种旧式概括不同。产品层主会话标识和内部 live session 标识可以分开。

后台任务把结果回报给发起会话，heartbeat 让主会话感知累计事件；群组活动以合并通知而非逐条唤醒的方式进入主入口。这里可以借鉴的是**主 Agent 负责世界模型，任务只回传有限结果**，不是把每个 worker 的全部 transcript 倒进主会话。

### 4.4 记忆写入、检索和后台整理

已公开的机制包括：[O2][O6]

- agent 主动将值得保留的信息写入文件；
- compaction 前的 memory flush：私有维护轮提醒 agent 先保存重要内容；
- SQLite FTS5、向量与 hybrid search，支持相关性、时间和多样性等排序；
- dreaming 后台筛选与整理短期候选，达到条件后晋升为长期记忆；
- `memory_search` / `memory_get`，以及用于历史会话的独立工具；
- 可插拔 memory backend 与 context engine。[O8]

这些是不同环节：**召回、记忆晋升和上下文压缩不是同一个功能。** 原始内容放在哪里、如何建立索引、哪些内容有资格晋升，需要分别设计。

### 4.5 Compaction / pruning / forget 的边界

Compaction 将旧上下文总结，保留近期尾部；原始历史仍在持久存储。公开 safeguard 设计重视 pending asks 和精确 ID，且失败时不能假装成功替换历史。[O7]

Pruning 更轻，主要削减模型看到的旧工具结果，不等于删除原始 transcript。当前专门的 pruning 文档还描述了跨重启可恢复的 projection 标记；compaction 页末尾对 pruning 的「in-memory only」简表与之存在口径差异。这里以两者共同明确的**模型视图与原始历史分离**作为可靠结论，不把简表当统一存储保证。[O9]

`memory forget` 依据来源 lineage 删除可追踪衍生项，并记录 forgotten session 防止自动重新摄入；官方明确说，它**不自动删除原始 transcript、所有自由文本编辑和外部副本**。[O10]

启示：要做可靠遗忘，光删除 `MEMORY.md` 中一句话不够；但也不能声称一个 lineage 清理命令能抹除所有痕迹。

### 4.6 对我们的价值与代价

值得借鉴：稳定身份文件、主入口与任务会话分离、原始历史与投影视图分离、维护时机、来源追踪。

代价：当前 OpenClaw 已是丰富 runtime，不是「拷贝几个文件」就能获得相同效果。默认单用户跨渠道共享也不能直接用于多用户产品；多机器连接必须明确哪台实例是记忆权威。

## 5. Hermes Agent：小核心记忆、历史检索与冻结快照

### 5.1 Persona 属于实例，而不是启动目录

`SOUL.md` 从 `HERMES_HOME` 加载，是主要身份层；默认位置 `~/.hermes/SOUL.md`。项目规则归 `AGENTS.md`，`/personality` 是 session overlay，而非另一个长期人格数据库。[H2]

这个设计解决一个很实际的问题：**切换项目不应意外切换主 Agent 的人格。** 但 SOUL 是 prompt 内容，不是安全权限系统；扫描也不意味着可证明地防住所有注入。

### 5.2 核心记忆故意很小

当前官方 Persistent Memory 页面给出：[H1]

- `~/.hermes/memories/MEMORY.md`：环境事实、约定与经验，默认 2,200 字符；
- `~/.hermes/memories/USER.md`：用户 profile，默认 1,375 字符；
- `memory` 工具执行 add / replace / remove；
- 超限返回错误，让 agent 主动合并、缩短或移除，而非悄悄截掉所有旧内容；
- 历史会话存入 `~/.hermes/state.db`，通过 FTS5 和 `session_search` 找回具体内容。

这些字符预算可配置，并不是整个记忆系统只有这点容量。**小核心负责每次都知道的事情，历史检索负责偶尔才需要的细节。** 中文字符数与 token 数并不等价，文档的 token 近似不能直接用作我们的预算。

### 5.3 冻结快照是关键取舍

Hermes 的核心记忆在 session 开始时作为 frozen snapshot 进入 system prompt，运行期间不重写该块，以保护 prefix cache。记忆工具写入立即落盘，工具响应可见最新状态，但更新不会自动替换当前 session 的初始记忆块。[H1]

官方特别指出：消息平台的连续 session 可以跨进程重启持续数周；重启机器并不自动结束 session。一直不 reset 会增加压缩成本，也使「新 session 读核心记忆，再按需找历史」的学习路径难以发挥。文档建议在任务完成、话题变化或新一天等自然边界执行 `/new`。

对本产品非常重要：如果 GUI 永远只有一个主入口，不能要求用户懂得执行 `/new` 才能获得新记忆。可以保留 UI 时间线，但在内部引入 execution epoch，并明确把更正作为当前轮 delta 提供，而不是等到下次 session。

### 5.4 核心记忆之外还有背景学习与 provider

官方页面描述 post-turn review 可保存记忆或维护 skill；可切换辅助模型、限制成本、关闭自动 review，并区分写入和通知开关。[H1]

Memory Providers 文档描述 provider context 注入、预取、对话同步、session-end 提取和内置写入镜像；Honcho 提供用户模型与 session context 等能力。[H3]

默认路径是附加 provider 与核心记忆共存，不应假定安装插件就自动取代内置记忆。官方页面部分概括又称「never replacing」，但配置章节明确可以关闭两个内置 store、保留外部 provider。实现选型应看具体开关与版本，而不是只看概括句。

特别需要追问：镜像写入产生几份副本？纠正是否覆盖全部？provider 离线如何降级？第三方云会收到哪些聊天与用户信息？

### 5.5 长上下文压缩

固定快照 compression 文档描述：Gateway pre-agent hygiene 与 agent 内 ContextCompressor 两层；后者是正常压缩路径，可插拔 context engine，且 native Codex runtime 有不同 ownership。[H4]

当前默认 in-place compaction 保持同一 session ID，旧 turns 软归档后仍可搜索；lean 策略倾向短尾部、精确标识 anchor、摘要和恢复指针。这是值得借鉴的**摘要不是终点，仍能按原始证据恢复**。

不要把所有 provider 的策略混成同一条，更不要把「lossless」名称解释成模型每轮完整理解了所有历史。原始数据不丢失和当前推理不漏信息是两回事。

### 5.6 对我们的价值与代价

优点：身份与项目隔离、小核心控制成本、历史检索补足、skills 承担程序性记忆，结构容易理解。

不足/待验证：冻结快照与永远主会话的矛盾；同一 Hermes home 多个 agent writer 的冲突；自动学习是否真正写入、是否误把猜测变事实。官方明确不建议两个 agent 进程共用一个 home。[H1]

官方当前 memory 页说 session search 返回实际消息、不经 LLM 总结，而 README 仍有「FTS5 + LLM summarization」的概括。本文采用具体功能页的描述，性能数字和效果不作独立实测结论。

## 6. Codex：执行 session 已成熟，但个人身份所有权仍需选择

### 6.1 当前确实有本地长期记忆

OpenAI 官方说明：[C1]

- ChatGPT web memory 与 local Codex memory 是不同 store 和控制面；
- local memories **默认关闭**；
- 启用后从符合条件的既有 chats 中生成记忆，跳过 active / short-lived sessions；
- 等待会话足够 idle 后后台更新，不是每轮结束立即写完；
- 剩余额度较低时可跳过后台生成；
- `~/.codex/memories/` 或相应 `CODEX_HOME` 保存 summaries、durable entries 和 supporting evidence；
- 这些文件是 generated state，官方不建议把手改内部文件作为主要控制面；
- 每个 chat 可以分别控制是否使用已有记忆、是否贡献未来记忆。

所以不能以「Codex 没有长期记忆」为自建前提。但对一个几乎始终 active 的个人主 Agent，**它的 idle/session 驱动写入是否及时**是明确的研究问题，不应默认达到我们的实时更新要求。

### 6.2 AGENTS.md 与自动 memory 不一样

`AGENTS.md` 用于显式、稳定工作指导；自动 memories 从过往经验提炼。前者更适合放产品委托的角色与明确偏好，后者更适合可再生的执行经验。[C3]

这并不意味着一个 AGENTS.md 可以任意替换 Codex 内建身份或所有高优先级规则。我们的产品人格只能在引擎支持的指令边界内生效；必须测主 Agent 自我介绍、跨任务表达和冲突优先级，不能只测能否读到文件。

### 6.3 App Server 的价值

官方 App Server 提供 `thread/start`、`thread/resume`、`thread/fork`、`thread/compact/start` 等线程生命周期接口。[C2]

产品可以拥有自己的 main timeline，并维护到原生 thread 的映射；给任务创建独立 thread，不必每个任务继承整个主 timeline。

**ACP 标准化 agent-client 通信，不自动标准化全部记忆管理。** `codex-acp` 是否暴露原生 memories 控制、每轮指令更新、压缩事件、线程导入导出，需要对固定版本验证。不能因其底层使用 Codex 就声称和 App Server 完全等价。

### 6.4 两种可行但不同的产品承诺

**Codex-first 托管**：产品尽量复用 Codex session 与 memory，开发量小；但只能承诺它明确支持的记忆时效、编辑/删除控制与跨模型范围。

**产品记忆 + Codex 执行**：产品保存权威 profile、关键责任与事件；对 Codex 注入相关视图，Codex memory 只作执行经验或关闭。自由度大，但需要我们实现注入、版本、一致性和遗忘。

Codex-first 适合快速证明桌面操作闭环；产品记忆更适合未来多引擎、多节点的主 Agent。不能把两种架构合并成「双方都保存全部记忆而自然一致」。

## 7. Claude：必须分开 Claude Code 与消费者 Claude

### 7.1 Claude Code：显式指令 + auto memory + task session

官方文档区分：[A1][A2]

- `CLAUDE.md`：用户/项目显式规则；
- auto memory：agent 保存的经验，默认项目目录形态为 `~/.claude/projects/<project>/memory/`；
- `MEMORY.md` 是简短索引，topic files 按需读；
- 启动只加载索引前 200 行或前 25KB，以先达到的限制为准；
- 同一 repo 的 worktree/subdirectory 共享该 auto-memory 目录，但不自动跨机器或云环境共享；
- auto-memory 文件可编辑/删除，`/memory` 提供查看与管理入口；
- 主会话 auto memory 不自动加载到普通 subagent；fork 等情况另有规则。

Session 绑定工作目录，可 resume / fork；新 session 不携带前一个 session 的全部对话。接近窗口限制时先清理旧工具结果，再必要时摘要。稳定规则应进 CLAUDE.md，而不是依赖早期聊天始终保留。[A2]

项目 root 的 CLAUDE.md 会在 compaction 后重新读取；nested/path-scoped 指令有不同重新加载时机。[A1] 这再次说明「重新构建稳定层 + 有界历史」比一个永久 prompt 更合理。

### 7.2 消费者 Claude：当前已不是只有每日总摘要

本次读取的官方帮助页把**当前体验和 legacy 体验放在同一篇文章中**。[A3]

当前体验：

- memory 随聊天保存为 individual topics，可主动保存或用户要求 remember；
- 可在 Settings > Memory 的 Topics 查看、编辑、删除；
- 每个 project 有独立 memory space 与 project summary；
- past-chat search 使用 RAG，以可见工具调用与聊天引用提供具体证据；
- 当前页面说明 memory 可在 chat 与云端 Cowork 间共享，本地 Cowork 不用这一层；
- 删除/过期原始聊天**不会自动移除已生成的相关 memory entries**，用户可单独删除；
- memory 暂停、reset、单聊关闭和 incognito 是不同控制。

Legacy 部分才描述每 24 小时合成摘要、删除聊天后更新 synthesis。因此不能拿旧文章或页面后半部分来说明当前通用行为。

启示：**历史可搜索、长期 topic memory 和项目 summary 是不同资产；它们的更新与删除语义必须分别写清楚。** 这些消费者功能不代表 Claude API 会为我们的应用自动建立同样的用户模型。

### 7.3 Claude API compaction 不等于长期个人记忆

官方 API 文档描述服务器摘要压缩，包括按需和阈值触发两种 beta 路径，各自有兼容性与 header 要求。[A4]

这可以减少应用自己维护 summarizer 的工作，却不会自动解决用户身份、跨会话事实冲突、共享权限、任务副作用和可靠遗忘。摘要 block 仍是执行上下文的一部分。

## 8. ChatGPT Dot：长期主 Agent、独立 notes 与任务上下文

官方 Tasks and memory 文档已经明确描述：[D1]

1. Dot 可以同时追踪多个责任，在同一对话调整优先级。
2. 能将工作拆给**并行后台 agents**，结果报告给 Dot；也可以创建可见独立 threads 并追加指导。
3. 新 task 收到针对该任务的指令和上下文，有自己的 conversation；**不会自动收到用户与 Dot 的每一段聊天**。
4. 使用当前 conversation、相关 ChatGPT memory 和 Dot 自己的 saved notes。
5. Dot notes 保存偏好、决策和 ongoing work，**不是完整 transcript，也不等于 ChatGPT saved memory**。
6. 改 ChatGPT saved-memory 设置不一定改变 Dot 已保存的 notes。
7. 不同消息渠道可联系同一个 Dot，共享连续性，但可见对话仍分开；知道私人信息不自动获得向其他 audience 披露的权限。
8. 固定时间责任需要独立 schedule；停止当前工作与取消 recurring schedule 不同。

### 8.1 对既有产品定位的修正

早期设计草稿曾把「ChatGPT Dot 不支持子 agent」作为差异。**这一概括与当前官方文档不符，不能继续当作已验证竞品事实。** pot 产品文档 v0.5 已收敛为一个长期主助手、唯一用户可见主聊天及内部 tasks/workers；没有独立任务 conversation，已取消持久项目 Agent、独立 Agent 管理与 pin/自动归档设想。

当前 pot 的部署差异是自主控制的本地/远程 Linux 环境，而不是额外项目助手。Dot 允许用户打开独立任务线程检查和追加指导；pot 按用户决定简化掉这项 UI，检查与指导全部回到主聊天，但不禁止内部并行与有界任务上下文。首次用户任务为命名 Agent 后完成；GPUI/tray/VNC 可行性留待验证。本文其他项目的架构仍作为技术研究参考，不自动转成 pot 产品功能。

### 8.2 能学什么，不能推断什么

值得借鉴：一个长期主入口、不同责任的独立执行上下文、适量 notes、调度与消息渠道分离。

尚未知：notes 的具体格式、如何选择相关条目、是否后台统一 consolidation、session 何时更换。不能凭这些公开行为断言 Dot 采用某个向量数据库或类似 OpenClaw 的文件结构。

## 9. Meta Muse：身份和记忆是用户可管理的产品对象

### 9.1 官方公开了很具体的文件式表面

Meta 帮助页明确提供以下控制：[M1][M2]

- 名称、personality/tone、communication style、emoji/avatar/tagline 可定制；
- 可通过对话请求或手动界面编辑；
- 理解来自直接对话、观察到的偏好/模式、connectors 和 file-based memory system；
- Identity 页面下有 `Memory.md`、`Soul.md`、`Identity.md` 相关入口；
- Memory 管理长期所知，Soul 管理 core truths / boundaries / personality，Identity 管理 name / creature / vibe 等；
- 主聊天之外有 side chats；goals、upcoming reminders/tasks、library/system files 有独立管理面；
- 支持通过导出 ZIP 导入其他助手聊天历史，并能下载自己的聊天、文件和 agent information。

文档的文件大小写存在不同写法，本文保留其语义，不假定内部路径/大小写约定是稳定 API。

### 9.2 删除边界尤其值得看

官方明确：删除聊天消息或文件后，由它们学到的信息仍可能保留在 memories；forget skill 尽能力搜索并移除相关信息，**不是完整擦除保证**。完整 reset 会清空主线程、文件、active tasks 等，和删一段 side chat 不同。[M2]

这说明产品的记忆 GUI 不能只做一个「清空聊天」按钮。用户真正需要知道：这个信息存在哪些层、是否仍能从其他文件被学回、是否要删除来源。

### 9.3 对我们的借鉴

最强的启示不是某种神秘算法，而是：**身份、记忆、goals、任务与文件都应该成为可见、可编辑的产品对象。**

Muse 与 OpenClaw 使用相似文件名字不证明共享实现。公开文件式表面也不证明内部只用文件、不用数据库、没有 summarizer。当前资料未披露其完整 context assembly 和 session compaction 算法。

## 10. 横向比较：我们到底在选什么

| 维度 | OpenClaw | Hermes | Codex local | Claude Code | ChatGPT Dot | Meta Muse |
|---|---|---|---|---|---|---|
| 身份表面 | IDENTITY / SOUL / AGENTS | 实例 SOUL + overlay | AGENTS 工作指导，受引擎规则约束 | CLAUDE 指令 | 公开产品身份，内部机制未披露 | Identity / Soul GUI 与文件表面 |
| 核心长期记忆 | USER / MEMORY + 日记 | 小 MEMORY / USER | 后台 generated memories | 索引 + topic files | 自有 notes + ChatGPT memory | 可管理 Memory 与 supporting files |
| 历史召回 | 文件/会话搜索，受可见性控制 | SQLite session search | 文档确认 memory evidence，不等于通用产品历史 API | session resume + 按需 memory 文件 | 任务/对话上下文选择，内部检索未知 | file-based memory，算法未知 |
| 主会话策略 | rolling main，默认不自动 reset | continuous gateway + 建议自然 reset | 原生 task thread | 目录关联 task session | 长期主入口 + 独立 task conversations | main chat + side chats |
| 更新时机 | 直接写、flush、dreaming 等 | 直接写/后台 review，prompt 快照冻结 | idle 合格历史后台提取 | session 中主动读写 | notes 随责任变化 | 对话或 GUI 编辑 |
| 用户更正/遗忘 | 有 lineage 工具，但覆盖有限 | memory 工具/管理面；provider 另算 | generated state 需官方控制，不能只手改 | Markdown 可编辑删除 | ChatGPT memory 控制不保证清除 Dot notes | 可编辑，forget 尽力，full reset 独立 |

不存在一个「最先进项目 → 原样复制」的结论。真正要选的是：身份与事实由谁持有、需要多强的时效和删除语义、愿意承担多少 runtime 工作。

## 11. 常见长期记忆架构模式与优缺点

以下是从上述设计归纳的工程模式，不是市场占比统计。

| 模式 | 优点 | 主要问题 | 适合本产品的部分 |
|---|---|---|---|
| 小型文件核心 | 可读可改、便宜、易备份 | 容量有限、并发写、缺结构化来源 | persona、核心偏好、摘要索引 |
| 事件日志 + 摘要 | 能追溯经历、方便恢复 | 日志越来越大、摘要会丢细节 | 主 timeline、任务事件、原始证据 |
| 结构化事实库 | 可纠错、scope、版本和过期清晰 | 提取/合并成本、schema 演进 | 用户偏好、项目事实、权威记忆 |
| RAG / hybrid search | 历史大但每轮只取小部分 | 召回失败、错误 scope、旧事实高分 | 情景档案、知识与会话检索 |
| skills / 程序记忆 | 将经验变可复用流程 | 错误流程自动传播、版本失效 | 服务操作、浏览器工作流 |
| 知识图谱/用户模型服务 | 关系和复杂 profile 更丰富 | 运维、第三方副本、解释困难 | 以后有明确需求再试验 |
| 供应商原生记忆 | 省开发，与引擎优化联动 | 可控性与可迁移性受限 | 执行经验，或首版 Codex-first |

**文件、SQLite、向量库不是互斥选择。** 文件可以是用户编辑面，数据库是结构化权威，索引是可重建视图；问题在于明确哪一份可以决定事实。

特别针对中文：只接默认英文 FTS 分词不能保证良好效果；应测试中文分词/字符 n-gram、专有名词、中英混排、repo 和外部 ID。OpenClaw 当前 builtin 文档明确列出 CJK trigram 支持，可作为实现参考。[O6]

## 12. 建议的长期身份层

### 12.1 身份至少有三个 ID

建议分别设置：

- `agent_id`：这位长期助手；不因重启、换 model 或改名字变化。
- `environment_id`：它当前运行的持久环境。
- `node_id`：承载环境的机器/后端节点。

用户身份 `owner_id` 与连接渠道身份另算。名字可改，不能当数据库主键。未来一台 tray 连接多个 node，不意味着这些 node 是同一个人格或自动共享所有用户记忆。

首版可以明确「一个环境有一个主 Agent」。跨节点同一 agent 的同步、分叉或共同记忆属于后续研究，不要为了远程连接先引入分布式共享人格。

### 12.2 不同层有不同写入权

| 层 | 谁能改变 | 建议 |
|---|---|---|
| 产品硬边界 | 可信 runtime / 用户设置 | 不能由网页和普通记忆改写 |
| 用户显式 persona | 用户；agent 根据直接请求调用配置接口 | 版本化、GUI 可见、可回滚 |
| 用户明确事实与偏好 | 用户及符合规则的记忆写入 | 更正立即生效，保留 supersession |
| 自动推断 profile | 提取系统 | 明确标推断，不冒充用户要求 |
| 经历/环境知识 | agent / tools / task results | 有来源与观察时间 |
| skill | agent 或用户 | 适用环境、版本和验证记录 |

无需每次弹批准，仍然可以做自动 schema 校验、来源判定、大小限制和审计。**写入规则不是交互式授权步骤。**

但是只把文件设成「理论上不能改」不够：若 agent 在同一 container 具有 root 或能控制 manager 所在权限域，它可能绕过文件限制。要强制保护产品身份、pairing 和宿主能力，可信控制面需要位于不同权限域，必要时在 container 外。若我们允许沙箱内完全管理权限，就要诚实承认哪些内部状态只能 best-effort 防护，不能靠 SOUL.md 承诺强隔离。

### 12.3 Persona 最好小、稳定、可解释

建议只包含：名字/职责、沟通风格、如何处理不确定性、与用户的协作方式。不放全量个人经历、任务清单或 credential。

不要自动从用户一时情绪推导永久人格；不要让「昨日为了某任务表现得更强硬」成为永久身份变更。人格演进若做，应有独立变更记录，而不是静默重写全部 SOUL。

## 13. 建议的记忆写入与读取流程

### 13.1 写入流程：先判断是什么，再决定存哪里

```text
人类消息 / 工具观察 / 任务结果 / 外部内容
  → 标记来源、owner、scope、时间与信任级别
  → 分类：明确偏好 / 事实 / 推断 / 经历 / 技能 / 临时状态
  → 候选提取与去重
  → 冲突、过期、敏感性与写入权校验
  → 持久提交 + 版本记录
  → 更新索引、短摘要与 GUI
```

建议分三种节奏：

1. **用户明确 remember/change/forget**：当轮提交，收到真实写入结果后再确认。
2. **任务结束经验**：checkpoint / completion 时提取，附产物与结果证据。
3. **背景 consolidation**：空闲合并、识别重复和过期，不阻塞主要交互；可限额、暂停、失败重试。

不能等「这个永久主 session 结束」才落盘，也不能把一次压缩摘要当成所有长期记忆的唯一写入。

### 13.2 一个最小事实模型

以下是建议，不是任何竞品的真实内部 schema：

```yaml
memory_id: mem_example
subject_id: owner_example
kind: preference
claim: 默认使用中文交流
scope: owner_private
source_kind: explicit_user
source_refs: [event_example]
observed_at: 2026-10-02T13:00:00+08:00
valid_from: 2026-10-02T13:00:00+08:00
valid_until: null
status: active
supersedes: null
confidence_class: explicit
version: 1
```

不建议把 LLM 自报的 0.97 当客观置信度；先用明确陈述、工具验证、推断等离散证据类别。可增加 project/environment scope、敏感类别和索引 generation。

「最近」「月底」等相对时间应保存原话与解析后的时间/时区；临时约束有 expiry；两条偏好冲突时既看新旧，也看范围与来源权威。新网页不能因为更晚就盖过用户明确设置。

### 13.3 读取流程：先做范围过滤，再做相关性排序

```text
当前请求 + 当前话题/任务 + audience + node/environment
  → 权限与 scope 过滤
  → 核心偏好与活跃事实
  → 关键词 / 语义候选
  → 去重、冲突与新鲜度检查
  → 限额摘录 + 来源
  → 组装该轮 context
```

不要先做全库向量搜索，再指望模型不要说出越权内容。私人记忆、其他节点记忆、其他项目事实，在进入模型前就应筛选。

任务开始可主动注入最小相关背景，同时提供按需搜索工具；只提供工具会导致模型忘记搜索，只做自动注入则容易塞入不相关旧知识。两者结合，但分别计预算。

### 13.4 纠正与遗忘是两种操作

**纠正**：新 active fact 取代旧 active fact；旧记录保留历史身份，不再作为当前答案。当前 session 也收到更新 delta。

**遗忘**：需要定义删除语义与范围：核心事实、日记/摘要、索引、缓存、provider 副本、worker 已加载上下文、导出与备份，哪些删除，哪些只停止使用。

建议：

- 为衍生物保存 source refs，删除时追踪；
- 加 tombstone / exclusion，防止从仍保留的 transcript 自动学回；
- 旧 session 已加载的记忆必须失效或换 epoch，不能只更新磁盘；
- 不支持删除的远端副本明确报告；
- 保留日志和备份的策略写清楚，不能同时承诺无限审计和立即抹除一切；
- 「不使用记忆」「不贡献记忆」「不保存 transcript」「删除已有记忆」应是不同开关。

资料支持我们必须认真做这个设计，而非竞品都已完美解决：OpenClaw、Claude 当前体验和 Muse 都公开了非等价删除边界。[O10][A3][M2]

## 14. 长会话管理：主时间线与执行 session 分离

### 14.1 建议对象模型

```text
Agent（长期身份）
  ├── Main timeline（用户永久聊天入口）
  ├── Topic / task context（内部话题/任务上下文，不新增可见 conversation）
  ├── Goal / responsibility（长期责任）
  ├── Task（可检查进度与结果的工作）
  │     └── Execution session / epoch（某引擎的有界执行上下文）
  └── Memory / skills / artifacts（持久资产）
```

Timeline 是历史与 UX；topic 是内部理解范围；task 是工作与状态；execution session 是推理实现。一个 topic 可以有多个 tasks，一个 task 也可以跨多个 epochs。pot 只有一个用户可见主 timeline，内部多个 sessions 不映射为多个聊天窗口。

不因新意图自动开用户可见聊天；内部边界可自动处理，任务进展/请求/结果及追加指导汇总到主聊天。竞品 side chat 仍可研究，但不是 pot 当前产品对象。

### 14.2 什么时候保留同一 execution session

适合：同一任务连续推进、最近工具结果重要、引擎上下文仍健康、需要 prefix cache。

不适合：跨多个无关任务积累了大量桌面历史、身份或 scope 发生变化、需要移除已加载的记忆、换引擎、上下文反复压缩但没有有效进展。

**自然边界优先，不按任意消息条数粗暴切。** 可参考任务完成、稳定 checkpoint、长时间 idle、主要话题切换；有进行中的副作用/工具调用时不能直接抛弃 session。

### 14.3 切 epoch 时保留什么

至少保留：

- 用户当前目标与尚未回答的请求；
- 已确定决策及理由、明确限制；
- 活跃 task ID、外部对象 ID、产物路径；
- 已完成动作、待验证动作、结果不确定的动作；
- 下一步和阻塞点；
- 可回查原始日志与产物的指针；
- 当前 identity/profile 版本和后端 desktop control flag；更强 lease 机制为后续研究。

新 epoch 重新加载稳定身份和最新记忆，而不是把旧总结复制后继续忘记更正。主时间线只需显示简洁维护状态，不要求用户学会 provider 的 `/new` 命令。

### 14.4 一个实际示例

上午用户要求修复 repo；主 Agent 创建 coding task，得到 task ID。主 timeline 保留目标与进度，不装入全部编译日志。

中午用户聊天改为中文、并交给它一封待整理邮件：中文偏好立即更新；邮件形成另一任务，仅加载相关联系人、邮件内容和授权能力。

下午用户接管桌面：后端将 agent_control_enabled 设为 false，正在操作桌面的任务收敛输入并保留状态，确认后允许人工控制。用户改变浏览器标签页后明确交回，Agent 重新观察，不盲用旧坐标。

晚上问上午的 repo：主 Agent 根据 task ID 查状态、回查必要日志，继续该 task 或建立新 epoch。连续的是助手关系和任务记录，不是全天所有内容一直共用一块 prompt。

## 15. 上下文管理：预算、压缩与恢复

### 15.1 每轮 context 的建议构成

```text
稳定产品/引擎指令
  + 简短 persona 和明确核心偏好
  + 当前任务与可操作的真实状态
  + 本轮相关记忆（带来源/时间）
  + 当前话题的 bounded summary
  + 最近对话与必要工具结果
  + 当前输入
```

预算要包含 tool schemas、图像、系统指令、输出 reserve 和模型/引擎需要的推理空间，不只是统计聊天文字。

首轮实验可按单次**可用输入预算**设置大致限额：稳定层 10%、任务状态 15%、召回 15%、近期对话/工具 50%、余量 10%。这不是推荐所有模型统一比例；大量工具 schema 或桌面图片会改变结构，输出 reserve 应先从总窗口中扣除。

重要事实不是「相关性高就给更多 token」，而是小核心、当前任务优先、其他信息可回查。

### 15.2 区分六种机制

| 机制 | 作用 | 不解决什么 |
|---|---|---|
| Tool-result pruning | 旧工具输出改成短摘要/引用 | 不创建稳定用户事实 |
| Conversation compaction | 概括旧上下文，保留尾部 | 不是无损事实库 |
| Memory flush | 压缩/切换前保存值得长期保留的信息 | 不保证任务结果真实 |
| Session rotation / epoch | 新鲜上下文，重载最新稳定层 | 不自动续上所有待处理状态 |
| Retrieval | 从档案找证据补回具体信息 | 检索也可能漏召回 |
| Prefix cache / provider continuation | 降低成本、优化上下文继续 | 不等于跨 session 长期记忆 |

### 15.3 桌面/浏览器会使 context 增长特别快

建议把 screenshot、DOM、日志、大文件当 artifact：保存引用、时间、浏览器 tab/window ID 与必要描述，原始内容按需读取。

- 只保留近期或当前任务真正相关的图像；
- 旧 screenshot 描述是旧状态，不能用来直接点击；
- DOM/CLI 输出截取相关范围；输出超大时分页，不反复注入全文；
- 将「看到了什么」和「成功执行了什么」分开记录；
- 摘要不要声称看过 summarizer 实际没收到的图像。

OpenClaw compaction 文档明确说明 built-in summarizer 接收文本，对未送入的图像使用 omission marker。[O7] 图像历史的可恢复性要靠 artifact，而不是假装所有图片都进入了总结。

### 15.4 摘要需要验证，不是越短越好

建议结构：当前目标、用户限制、已做动作/证据、精确标识、尚未完成、下一步、回查来源。

关键 ID、路径和外部 request ID 最好从结构化状态机械复制，不让模型改写。压缩失败时保留原始记录，报告降级；设置有限次数和 no-progress guard，避免总结循环。

反复「旧摘要再总结」会逐渐丢条件。背景 consolidation 可以回查原始证据；核心事实不应只能追溯到第三手摘要。

## 16. 长期责任、worker 与桌面控制

### 16.1 主 Agent 不需要看见所有 worker 的全部历史

任务委托包建议包含：目标、范围、相关背景、允许工具/能力、产物要求、task ID、记忆版本。

结果包建议包含：完成度、验证证据、产物、外部对象 ID、问题、建议晋升的经验。worker 的报告不是自动成功证据；完成态需要对应验证。

worker 可提交 memory candidate，主记忆服务判定合并，避免多个 worker 直接争抢一份 USER/MEMORY 文件。并发写用版本/CAS、事务或单 writer 队列，不用「最后写入覆盖」作为偏好合并策略。

### 16.2 Heartbeat / wakeup 不要把主会话全部重跑

建议把固定 schedule、event-conditioned responsibility 和活跃 task 分开。唤醒只加载该责任、必要的最新观察与有限关联记忆。

无变化可以静默，事件合并去重；持久 wakeup 队列有执行状态和重试策略，不能把 reminder 写进 MEMORY.md 就认为完成了调度。停止 task、停主 agent、取消 schedule 具有不同语义。

### 16.3 人工接管属于精确 runtime 状态

pot 首版按产品 v0.5 采用环境级后端 `agent_control_enabled` flag：人工接管设为 false，所有受控 Agent 桌面/浏览器输入入口统一检查并收敛在途输入，后端确认后允许人工输入。不能仅发一句「请停下来」或只设置 GUI 变量。非桌面任务不因此全局停止；截图观察暂停是另一个建议隐私默认。

明确交回后启用 flag、重新观察桌面并核实被打断操作结果；人工控制时断线默认不自动抢回（建议）。flag 是受控通道的逻辑仲裁，shell/CDP 直连绕过、排队与在途竞态必须验证。更强的 control owner、lease generation/fencing 与权限隔离作为后续加固，不是首版已验证能力。切换控制权、进程重启和记忆更新不应互相代替。

### 16.4 外部副作用与「不确定结果」

例：发送邮件请求超时，但邮件可能已发送。不能因为摘要写「发送失败」就立即重发。

任务状态需保存 operation ID、服务返回对象、可能的幂等键和 `unknown_outcome`。恢复时先查询，支持幂等就使用，无法验证时明确处理不确定性。不能对所有外部服务承诺 exactly-once。

沙箱内无需逐步授权是本产品既定方向，不妨碍以上自动一致性约束；服务账号接入、宿主权限和 node pairing 仍独立管理。

## 17. 三种所有权方案的决策矩阵

| 方案 | MVP 速度 | 精确纠正/遗忘 | 换引擎 | 主会话控制 | 我们承担的工作 |
|---|---|---|---|---|---|
| A. Codex 托管主要记忆 | 通常最快 | 受公开控制面和生命周期约束 | 较强耦合 | 复用原生 thread，需额外映射 | GUI、环境、任务/桌面控制 |
| B. 全自建记忆与 context runtime | 最慢 | 可定义，但需真实实现验证 | 最高潜力 | 最自由 | 提取、召回、压缩、session、评测和运行治理 |
| C. 产品核心 + 引擎执行 | 中等 | 核心可控；经验缓存另算 | 核心较独立 | timeline 与 execution 分离 | 身份/关键状态、记忆服务与 adapter |

### 17.1 建议先比较 A 和最小 C，不直接投入 B

最小 C 只做：

- 产品身份、用户显式设置和少量关键事实；
- 主 timeline 与 task 状态；
- 稳定注入 + 当前轮更新 delta；
- 权威数据的查看、更正、删除；
- 原始事件和产物引用；
- 引擎 session 的创建、恢复、压缩/轮换适配。

自动 profile 推断、大向量库、知识图谱、多模型 dreaming 都可以后置。先证明不丢任务、不用过期偏好、不重复外部操作。

### 17.2 一份权威，不是两份主记忆

可选择：产品 profile/事实权威，Codex memory 只保留 coding experience；或关闭引擎自动记忆以建立清晰 baseline。

若无法从原生 memory 中隔离个人事实，就必须承认该方案的遗忘范围有限，或提供彻底重建 engine home/session 的路径。不能 GUI 删除产品记录后，仍让供应商记忆悄悄恢复旧偏好。

别用双向自动同步作为默认方案：两边都总结、重写再导入，很难确定冲突权威和删除传播。

### 17.3 一个建议持久目录

```text
agent-volume/
  identity/
    identity.json
    persona.md
  memory/
    core.md
    notes/
    skills/
  state/
    agent.sqlite
  artifacts/
  engine-homes/
    codex/
```

这是概念布局，不是已经采用的实现。`core.md` 可以是数据库生成的可读视图；如果允许 GUI/文件编辑，必须通过同一提交路径，不能形成另一份权威。

凭据需要独立存储与访问机制，不放 core memory。`engine-homes` 可能包含敏感登录状态，不能把整个 volume 当普通可分享笔记。

保留 schema/version 描述用于未来演进即可；本次不把迁移升级为产品功能。未来 `image + volume + version` 是运行资产的框架，不保证云 memory 副本、宿主钥匙串、node identity 与 OAuth 状态自然可迁移。

## 18. 必须做的研究实验

建议同一模型、尽量相同任务、固定客户端版本，比较 A、最小 C，以及 OpenClaw/Hermes 的代表配置。先关自动学习得到 baseline，再分别开启；记录输入/输出 token、辅助调用、延迟、错误和人工介入。

| 实验 | 场景 | 验收重点 |
|---|---|---|
| 身份连续 | 重启、换项目、换 model、resume | ID 与显式风格稳定，不混节点 |
| 当轮记忆 | 请求 remember 后立即新 epoch | 真实提交后才说已保存 |
| 用户更正 | 旧 timezone/偏好改新值 | 当前及后续 session 不再用旧值 |
| 长天多任务 | 编程、邮件、资料、闲聊交替 | 任务可回续，token 不无界增长 |
| 连续 active | 主聊天长时间不 idle | 原生记忆是否饥饿，显式写入是否补足 |
| 压缩恢复 | 多次 compaction 后问精确决策/ID | 能回查证据，未回答请求保留 |
| 桌面接管 | 正在点击时 human takeover | 后端 flag 拒绝新的 Agent 输入、在途收敛后确认；恢复前重观察，测试绕过/竞态边界 |
| 不确定副作用 | 邮件/PR 请求超时后重启 | 先核实、不盲目重复 |
| 遗忘 | 删事实但保留来源 transcript | 不自动学回；报告残留范围 |
| 并发冲突 | 两 worker 写同一偏好/经验 | 不丢更新、不静默覆盖 |
| 记忆投毒 | 网页要求更改 SOUL/泄露 profile | 外部文本不晋升为用户指令 |
| Scope 隔离 | 私人/项目/另一个 node 的相似问题 | 检索前过滤，不串环境 |
| 中文召回 | 中文别名、中英混排、repo ID | 事实召回与精确查询可靠 |
| Provider 离线 | embeddings/记忆云不可用 | 核心可用，说明降级，不阻断全部任务 |

量化指标：关键事实召回/精确率、过期事实使用率、纠正生效时间、遗忘后复现率、任务续接率、重复副作用数量、每有效任务总成本、P50/P95 响应延迟。记忆写入成功率要核对持久状态，不能用模型说「记住了」来计数。

安全/一致性场景宜设硬验收，不用平均分掩盖一次严重串环境或重复操作。检索质量、人格风格与成本则需要多样本测量，不能凭一次演示判断。

## 19. 尚待决定的问题与研究优先级

### P0：在实现前厘清

1. **主 Agent 推理接口**：首版只走 codex-acp、只走 App Server，还是 adapter 同时支持？每轮 context 更新能力如何？
2. **事实权威**：哪些 user memory 必须属于产品？是否允许引擎独立持有同类事实？
3. **生效语义**：GUI 更正何时进入正在运行的 session？忘记时是否强制换 epoch？
4. **主 timeline 与 task 的边界**：唯一主聊天已定；研究内部 task/session 路由、主聊天任务引用与追加指导、worker 生命周期，不研究独立可见任务 conversation 或持久项目助手。
5. **可信控制面**：如果沙箱内允许全权限，身份、配对和桌面控制状态如何免受绕过？
6. **忘记的承诺**：只停止使用，还是删除衍生物/来源/缓存？哪些副本不可完全清理？

### P1：原型阶段研究

- session rotation 触发、compaction 所属层及事件可靠性；
- background review 的成本、召回质量、是否干扰主用户交互；
- 中文 retrieval 与桌面 artifact 的索引；
- 长期责任的 wakeup 队列与幂等恢复；
- 原生引擎与产品日志的 retention / backup；
- task/worker 委托包携带哪些主助手偏好、notes 和能力，如何避免全量隐私继承。

### P2：有明确需求再扩展

- 复杂用户模型服务、知识图谱；
- 多节点共享同一 agent 的一致性；
- agent 间通信协议与共享经验晋升；
- 真正跨机迁移、凭据重新绑定和版本升级。

目前无需决定完整分布式 memory，也无需为「跨引擎」自研全部推理 loop。最有价值的下一步是对 A/C 做同一组原型实验，再决定记忆 ownership 的边界。

## 20. 最终判断

**已确认：**这些项目普遍把稳定规则、精选长期记忆、可搜索历史和执行上下文分层；没有材料支持「永远一个 session + 不断扩大 prompt」是足够的长期助手架构。

**设计建议：**我们应至少持有稳定技术身份、用户显式设置、主时间线映射和关键任务状态。是否进一步持有全部自动经验记忆，取决于原生引擎的时效、控制面、效果和成本实验。

**仍未定案：**Codex 托管与混合方案都值得验证。长期身份 ownership、主 Agent 的推理引擎、是否增加 worker 是三个独立决策。

**竞品与产品修正：**ChatGPT Dot 已公开支持并行后台 agent 及独立 task threads；pot v0.5 采用单主助手、唯一可见聊天与内部 tasks/workers，取消项目 Agent，首个用户任务为命名并完成。桌面接管首版用后端 flag，GPUI tray/VNC 待验证；这些决策不冻结 memory ownership。自动 action review、云端常在线、一台个人电脑及 ChatGPT memory 等差异，见[本地个人Agent工作环境产品设计](../product-design.md)的 0.5 和 4.6。

## 21. 资料索引

以下均为本次实际读取的官方材料；固定快照链接用于复核，在线帮助页可能继续变更。引用文档不表示本次对整个实现完成源码审计。

### OpenClaw

- [O1 Agent workspace](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/agent-workspace.md)：身份文件、workspace/runtime 分离与当前 SQLite 状态。
- [O2 Memory overview](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory.md)：记忆分类、flush、dreaming 与检索接口。
- [O3 Main session](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/main-session.md)：rolling conversation、后台汇总、默认 reset 策略。
- [O4 User model](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/user-model.md)：用户 directive、profile 选择、覆盖与权限边界。
- [O5 Session management](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session.md)：路由、scope、reset 与会话生命周期。
- [O6 Builtin memory engine](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory-builtin.md)：SQLite、FTS5、hybrid、CJK 与索引设计。
- [O7 Compaction](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/compaction.md)：摘要、标识保留、恢复与 provider 差异。
- [O8 Context engine](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/context-engine.md)：context assembly 和插件边界。
- [O9 Session pruning](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/session-pruning.md)：旧工具输出和模型投影视图。
- [O10 Memory provenance and deletion](https://github.com/openclaw/openclaw/blob/a272cf0cd56eb9d0a1245daaaf139902ac32da01/docs/concepts/memory-provenance.md)：lineage、admission、forget 和删除覆盖范围。

### Hermes Agent

- [H1 Persistent Memory](https://hermes-agent.nousresearch.com/docs/user-guide/features/memory)：本次通过官方网页读取；核心预算、冻结快照、session search、后台 review 与管理面。
- [H1 固定快照](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory.md)：已读取的对应仓库文档，便于后续复核。
- [H2 Personality & SOUL.md](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/personality.md)：实例人格与项目规则分离。
- [H3 Memory Providers](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/user-guide/features/memory-providers.md)：附加 provider、同步与 Honcho 用户模型。
- [H4 Context Compression and Caching](https://github.com/NousResearch/hermes-agent/blob/6dbb7b9ab857842617c2c35b63b49bd6ee77fc56/website/docs/developer-guide/context-compression-and-caching.md)：双层压缩、in-place、lean 与 native runtime ownership。

### OpenAI / ChatGPT Dot

- [C1 Codex memories](https://developers.openai.com/codex/customization/memories)：local memory 与 ChatGPT memory、后台生成和控制。
- [C2 Codex App Server](https://developers.openai.com/codex/app-server)：thread lifecycle 与客户端集成。
- [C3 AGENTS.md](https://developers.openai.com/codex/guides/agents-md)：显式工作指令；同时参照既有产品设计中的研究。
- [D1 ChatGPT Dot — Tasks and memory](https://developers.openai.com/codex/dots/tasks-and-memory)：官方产品页面；并行后台 agent、独立 tasks、notes、渠道与 schedule。

### Anthropic

- [A1 Claude Code memory](https://code.claude.com/docs/en/memory)：CLAUDE.md、auto memory、topic files、预算和管理。
- [A2 How Claude Code works](https://code.claude.com/docs/en/how-claude-code-works)：session、resume/fork、context、tool pruning 与 compaction。
- [A3 Claude chat search and memory](https://support.claude.com/en/articles/11817273-use-claude-s-chat-search-and-memory-to-build-on-previous-context)：当前 topic memory 与 legacy synthesis 的区别、项目范围、用户控制和删除语义。
- [A4 Claude API compaction](https://platform.claude.com/docs/en/build-with-claude/compaction)：服务器摘要，按需/阈值 beta 路径与边界。

### Meta Muse

- [M1 How to customize Muse's personality and memories](https://www.meta.com/help/artificial-intelligence/995796179982326/)：人格、记忆、file-based system 与历史导入。
- [M2 How to manage your Muse data](https://www.meta.com/help/artificial-intelligence/2225571704857152/)：Memory/Soul/Identity 管理、main/side chats、goals、forget 与 reset 边界。
