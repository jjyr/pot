# Backend crate 拆分（Issue #5）

设计确认：2026-10-05；先提交设计文档，再实施拆分。本次保持产品 API、数据格式、会话与额度语义，只调整模块边界；远程后端属于架构扩展能力，不提供远程连接设置或部署产品功能。

## 目标结构

```text
App / GPUI / Swift ---- HTTP/SSE ----> Server process
                                         |
                               config / routes / hosting
                                         |
                                         v
                                      Runtime
                            tasks / schedules / projection
                              /          |          \
                             v           v           v
                          Storage      Driver      Gateway
                          SQLite       CLI         HTTP / codecs
                             \           /
                              v         v
                                  Core
                         models / execution / tool schema
```

编译期依赖（省略三方库）：

```text
pot-app -------> pot-protocol <------- pot-server
                                           |
                                           v
                                       pot-runtime
                                         |---> pot-protocol
                                         |---> pot-core
                                         |---> pot-storage ---> pot-core + pot-protocol
                                         |---> pot-driver ----> pot-core
                                         +---> pot-gateway
```

Gateway 的生产依赖不包含 Storage；codec 与账本联动的测试使用 dev-dependency。


Rust workspace 包含 `app`、`protocol`、`core`、`storage`、`driver`、`gateway`、`runtime`、`server`。Helper 继续管理本地 VM，Server 不依赖 App、Swift bridge、Helper 或 GPUI。

- `pot-core`：不可变 Profile/ResolvedSpec/Binding、执行请求、Driver 事件与宿主/会话契约；不包含数据库、HTTP server 或进程实现。
- `pot-storage`：产品 SQLite、绑定持久化、请求/用量账本、事务和兼容迁移。保持现有主/任务数据库及 usage.sqlite 布局与 DELETE + synchronous=EXTRA。
- `pot-driver`：Pi RPC、Codex App Server、Claude/AGY CLI、进程收敛、原生会话身份检查与标准化事件。通过固定 ExecutionContext 和窄 ExecutionFeedback 接口汇报 native identity/usage；不持有 Runtime Service 或数据库连接。
- `pot-gateway`：模型协议 codec、provider HTTP transport。Runtime 在请求前完成持久预留并校验 lane/turn，再调用 transport；实际用量回到持久账本。Driver 的 native usage 路径独立于网关。
- `pot-runtime`：可复用后端库；产品命令、任务/计划、执行协调、binding 迁移、消息投影、恢复与结果回报。HTTP 适配函数沿用现有契约，路由和服务启动集中到 Server；本次不同时重写全部 handler 的参数与错误类型。
- `pot-server`：独立后端二进制与服务模块；配置、TCP 监听、HTTP/SSE/MCP/PTY 路由、endpoint 发布和生命周期。通过 Runtime 的公开后端入口组装，不读取 App 配置或依赖前端启动。
- `pot-protocol`：稳定前后端 DTO/API 常量；App 只依赖此协议，不依赖 Runtime/Core。
- `pot-app`：UI、原生桥、协议客户端与展示缓存。

Driver 命名描述执行适配职责；历史持久字段 `engine` 与枚举序列化值继续保留，不能因 crate 命名改变绑定 checksum。

## 运行与依赖边界

Server 可指定 `POT_DATA_DIR`、`POT_WORKSPACE`、`POT_RUNTIME_BIND` 后单独启动。后端文件路径始终属于运行 Server 的机器；模型与 CLI 请求在后端执行。模型网关/MCP 的 loopback 地址是后端内部通道，不是 App 地址。

本地 App 的 Helper/VM 自动启动和发现流程保持兼容。不同机器部署还需要端点发现、TLS、身份配置和产物传输等产品工作，本次不承诺这些功能已交付。桌面 broker 仍为当前合作式仲裁，crate 拆分不产生权限隔离。

Driver 的 ExecutionContext 包含本轮路径、角色、授权端点与窄反馈能力。Runtime 的反馈实现负责验证 native binding 与持久计量；Driver 不接触任务 Hub 或配置管理。lane token 沿用既有权限：主 lane 仍可调用主 Control API，worker token 仍受 API 限制；本次不声称增加 token 或 guest 权限隔离。取消仍要确认原生执行停止，不能以终态文本代替进程清理。

## 实施与验收

1. 文档先落地；建立 Issue/独立 branch/worktree。
2. 抽 Core 和 Storage，保留序列化和 SQLite 行为。
3. 抽 Driver，移除 DriverHost/SessionHandle 对 Service 的依赖；保留 epoch/turn/seq 过滤。
4. 抽 Gateway codec/HTTP transport，持久预留仍在联网之前。
5. Runtime 改为库，Server 独立组装和启动；更新构建、镜像、mise 和离线契约入口。
6. 编译后端和 App，运行领域/存储/Driver/Runtime 回归及独立 Server API 启动/重启检查。

数据表结构化、全局持久事件、App SSE 常态同步和 Desktop Controller 独立权限域不属于本次结构重构，继续由后续方案推进。

本次实现与验收结果见 [Backend crates 验证](validation/backend-crates.md)。
