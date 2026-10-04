# 开发计划与当前范围

更新：2026-10-04。按“纵向骨架优先”推进；已交付能力与完整场景验收分开记录，不再维护逐轮开发流水账。

## 当前能力

| 能力 | 状态与证据 |
|---|---|
| Pi + DeepSeek 主聊天 | 已实现真实流式回复、工具、取消、Quit 重开续聊；[Pi MVP](validation/pi-mvp.md) |
| GPUI / Sandbox / 设置 | Kit 统一界面、原生输入、noVNC 桌面、资源配置、tray；[UI 规范](ui-ux-design.md)、[验收](validation/ui-ux.md) |
| 多任务与主 Agent 路由 | 独立 session、并发、持久指导、取消、结果汇总、按状态搜索/归档；[多任务验收](validation/multitask.md) |
| Token 用量与本地额度 | 整体趋势、任务累计与工具统计；自动压缩及实际用量；日/周额度、等待及检查点续跑；[契约](token-usage.md)、[验收](validation/ui-ux.md) |
| 定时计划 | 主 Agent 创建；日历/间隔、时区、去重、补跑/跳过、独立执行和变化通知；[契约与验证](scheduled-tasks.md) |

最新界面只通过主 Agent 创建任务和计划；任务页面用于查看与管理。当前没有完整产品或公开发行验收结论。

## 剩余工作

D1–D6 保留为范围编号，后续能力已提前实现，不要求重新顺序开发。

| 阶段 | 现状 | 仍需交付或验证 |
|---|---|---|
| D1 纵向骨架 | 已完成 Pi MVP | 仅按新问题维护；AGY/Codex 不阻塞当前使用 |
| D2 Setup / 引导 | 配置表单和基本环境准备已有 | 冷安装、下载续传/故障恢复、认证校验、聊天内命名与进度持久化 |
| D3 报告场景 S1 | 任务执行与自动编排已有 | 两份附件→追加部署成本→报告/来源→重启追问的完整验收；结构化产物、明确偏好 |
| D4 网页接管 S2 | 桌面查看与合作式接管已有 | 浏览器 Agent 工具、桌面资源排队、独立 controller 权限域、真实 Pi shell/CDP/X11 绕过负测 |
| D5 持久计划 S3 | 调度及本地模拟端到端已实现 | 真实来源变化与证据、实际 sleep/wake 错过触发和重复唤醒完整场景验收 |
| D6 多引擎与发行 | 未完成 | 先按 #2 验证 Codex ACP 的认证/工具/取消/恢复；Claude 订阅及 AGY 不在当前范围；记忆更正/遗忘、服务授权与 review、升级恢复、签名公证与冷安装 |

完整 S1–S3 通过标准见 [产品设计](product-design.md#产品验收场景)。当前无 Developer ID，发行签名公证暂缓，只阻塞发行；开发包保留必要的 ad hoc 签名。

## 实施约束

Rust Runtime + SQLite、共享 `protocol` crate、GPUI/Swift/helper 路线固定；旧 `poc/` 已移除，历史实验从 Git 查阅。先用 Pi 推进真实产品切面，不重写引擎已有工具循环，不另开记忆算法 POC。远程多节点不纳入当前 MVP。

优先确定性测试与真实 Pi + 本地模拟 provider，必要时做节省用量的真实模型验收。历史 tray/多屏/完整 IME 矩阵不重新作为开工门槛；新问题做针对性回归。签名、远端 IME/弱网等只在对应产品承诺前验收。


## Issue #2：Agent layer（2026-10-04）

此分支是基础增量，**尚不能关闭 #2**。

| 阶段 | 当前结果 | 仍需完成 |
| --- | --- | --- |
| 1 数据模型 | Profile/ResolvedSpec/Binding、epoch 1 导入、不可变快照和损坏检测 | 产品化 profile 管理界面 |
| 2 Pi 驱动 | 主聊天/worker 走 DriverHost/SessionHandle，事件隔离与产品投影抽离，原真实 Pi 测试通过 | 完整 host start/shutdown/auth、permission、可注入 fake-driver 取消验收；Codex 生命周期 |
| 3 网关 | 通用请求核心、OpenAI Chat/unknown、provider 路由和用量归属 | Responses/Messages/Gemini codec、provider 维度的预算策略与 UI |
| 4 工具桥 | 共享 schema/dispatcher，Pi 薄适配器，带 lane/turn 身份的 HTTP MCP facade | Codex 的 MCP 重连/多 lane 实测；权限 UI |
| 5 迁移 | 事务化 handoff、新 epoch、重启恢复、API 设置及既有 pi.json 设置触发、任务默认固定 | 完整 profile/role 设置 UI、rebind 选择 UI；原地切换与可选 narrative 未开启 |
| 6 Codex 主聊天 | ACP 2.1.1 + Codex 0.159.1 仅无凭据握手探测 | CodexDriver、guest 登录与二进制固定、真实工具/取消/后台资源停止/恢复验收 |
| 7 Codex workers/计划 | 未实现 | 独立 native threads、角色隔离、无重复计量、并发实测 |
| 8 任务路由 | 新任务继承所选 worker 默认 profile，旧任务固定 | 显式/计划/tool hint 的优先级、能力要求校验、profile/provider 并发上限 |

验证证据及局限见 [Agent layer 验证](validation/agent-layer.md)。不启用未经验收的
Codex profile；AGY、Claude 订阅和自制 agent loop 未新增支持。
