# 文档索引

先按问题选一篇，不必通读。当前实现以代码为准；产品目标不等于已交付，历史验收不等于现状。

| 你要做什么 | 读什么 |
|---|---|
| 启动、构建、测试 | [开发指南](development.md) |
| 了解已做什么、还缺什么 | [开发计划](development-plan.md) |
| 理解组件、数据流和任务编排 | [架构](architecture.md) |
| 修改 API、Pi、任务状态或恢复 | [Runtime 契约](../runtime/README.md) |
| 构建 Sandbox 镜像、桌面主题或 CJK 字体 | [Sandbox 镜像](sandbox-image.md) |
| 修改聊天、任务、设置或 Sandbox UI | [UI 规范](ui-ux-design.md) |
| 修改 Token 统计、周期额度或额度恢复 | [Token 用量](token-usage.md) |
| 修改计划触发、时区或通知 | [定时计划](scheduled-tasks.md) |
| 确认长期产品边界与验收目标 | [产品设计](product-design.md) |
| 查已跑过的测试和局限 | [Agent 层](validation/agent-layer.md)、[Pi MVP](validation/pi-mvp.md)、[多任务](validation/multitask.md)、[UI](validation/ui-ux.md)、[Sandbox 镜像](validation/sandbox-image.md) |
| 排查旧集成或追溯技术选择 | [历史 POC 索引](poc-plan.md) |

## 按需研究

`research/` 是带日期的背景材料，不是当前实现规范：

- UI 框架：[GPUI Kit](research/gpui-kit-evaluation.md)。当前组件用法看 UI 规范。
- VM 与桌面：[Apple Containerization](research/apple-containerization.md)、[Docker GUI](research/docker-gui.md)。
- 引擎会话：[协调对比](research/session-coordination.md)、[Hermes](research/hermes-sessions.md)、[OpenClaw](research/openclaw-sessions.md)。
- 后续记忆：[身份与记忆](research/identity-memory.md)。
- 原始笔记来源与导入时 hash：[来源清单](source-manifest.json)，不表示当前文件 hash。

## 维护规则

每个主题只维护一份当前规范；进度只写开发计划，操作命令只写对应入口文档。验收报告记录日期、方法、结果和未测范围；后续变化追加注明，不把旧观测改成新结论。已被替代的建议留在 Git 历史或明确标为历史。机器信息放 `~/Workspace/Machines`。
