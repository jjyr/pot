# 开发指南

## 开发与运行

需要 Apple Silicon、macOS 26+、Xcode/Swift 6.2+ 和 [mise](https://mise.jdx.dev/)。Rust、Python、Zig、cargo-zigbuild 与检查工具的版本由 `mise.toml` 固定；Swift 使用当前选中的 Xcode。

```sh
mise trust
mise install
mise run setup        # Rust 组件、Linux target、本仓库 hk hooks、环境检查
mise run prepare
mise exec -- scripts/configure-pi # 可选：仅 Pi + DeepSeek 路线
mise run build
mise run bundle
mise run open
```

`bundle` 会在独立 VM 内预装依赖并生成完整 Sandbox OCI 镜像；只有构建阶段需要下载软件，App 启动直接加载镜像。镜像缓存、桌面字体与大小对比见 [Sandbox 镜像](sandbox-image.md)。

开发包为 `.local/d1/Pot.app`。`scripts/pot open` 使用 `.local/d1/local-runtime`（可用 `POT_LOCAL_ROOT` 覆盖）；直接双击默认使用 `~/Library/Application Support/pot`，两者数据不同。

`configure-pi` 仅从宿主 Pi 导入 DeepSeek 凭据到 guest 私有配置，不调用模型，不把密钥打入 App。模型为 `deepseek-v4-flash`；配置入口、默认额度和 K/M token 输入规则见 [UI 规范](ui-ux-design.md#配置额度)。已有配置不会自动覆盖，用量账本不重置。

## 独立 Server（开发入口）

后端可以独立构建和启动，不需要运行 App/Helper。路径属于运行 Server 的机器；使用单独数据目录，不打开正在运行的 App/VM 数据。

```sh
mise exec -- cargo build --locked -p pot-server
POT_DATA_DIR=/tmp/pot-server/product POT_WORKSPACE=/tmp/pot-server/workspace POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

`POT_PI_BIN` 可指定 Pi executable。Server 将私有 bearer/boot/port 写入数据目录中的 `runtime.json`；HTTP/SSE/MCP/PTY 仍使用现有 Control API。未配置 Agent 时主聊天拒绝发送，Terminal 可用。关闭时停止后台协调器、会话、PTY、Codex host 和 SSE。

App 继续使用本地 Helper/VM 发现方式，没有远程端点设置。跨机器部署仍需 TLS、身份与产物传输等工作；这里只保留后端独立运行的架构边界。

## 验证

```sh
mise run fmt           # 显式修复格式
mise run check         # 格式、脚本、Rust workspace 和 Swift Helper 编译
mise run test          # Runtime / protocol，无真实模型调用
mise run test:app
POT_TEST_PI_BIN="$(command -v pi)" mise exec -- cargo test --locked -p pot-runtime real_pi -- --ignored
```

最后一条使用宿主已安装的 Pi 和本地模拟模型，无付费调用。真实 App/VM/模型验证记录见 [文档索引](README.md)，旧 POC fixture 不代表当前产品验收。

### Codex 验证

用户需要登录时，自行在独立 Terminal 运行官方 CLI 登录命令（Codex 支持
`codex login --device-auth`）；Pot 仅在设置 → Agent 检测状态。
在设置 → 助手填写账户可用的模型名称并明确选择 Codex。
登录态只由 guest CLI 管理，保存在持久 engine-home。

无凭据集成测试（显式指定安装好的官方 Codex CLI，不调用付费模型）：

```sh
mise exec -- cargo build -p pot-server
mise exec -- python scripts/tests/codex-runtime.py --runtime .local/poc/cargo-target/debug/pot-server --codex /absolute/path/to/codex
```

构建最新镜像后，可在独立无网络 VM 中运行同一契约：

```sh
codesign --force --sign - --entitlements helper/virtualization.entitlements .local/d1/helper-build/release/PotImageCheck
.local/d1/helper-build/release/PotImageCheck .local/d1/codex-guest-check .local/d1/resources scripts/tests codex-guest.sh
```

每次冷验证使用新的结果目录。该测试验证真实 CLI 与本地模拟模型的行为，不代替真实登录和模型验收。

### Git hooks

`mise run setup` 安装本仓库的 hk pre-commit / pre-push，不修改全局 hooks。每个新 clone 都需要运行一次。Git 调用 hook 时通过 mise 加载工具，无需先激活 shell（mise 本身需在 PATH）。

- pre-commit：按改动文件检查格式和脚本；Rust 格式检查覆盖 workspace，规则或检查入口变更触发全量轻量检查。检查 staged diff 的空白错误。
- pre-push：相关 Rust/Swift 改动触发 `check:rust` 和核心测试；Helper 改动另触发 `check:swift`。
- hooks 只检查、不自动修复或暂存，源码检查读取当前工作树。部分暂存时，应保证提交内容也已格式化。镜像构建、VM 和付费模型测试不进入 hooks。新机器的编译检查可能耗时，后续复用缓存。

手动验证入口：`mise exec -- hk run pre-commit --all`。完整本地检查用 `mise run check`；需要修复时执行 `mise run fmt`。基础规则集中在 `ruff.toml`、`.swiftformat` 和 `scripts/dev-check`，hook 只负责筛选和调用。

## 代码入口

| 目录 | 职责 |
|---|---|
| `app/` | GPUI 界面、Swift 原生桥与配置 |
| `helper/` | Swift Containerization、VM 生命周期 |
| `server/` | 独立启动、HTTP/SSE/MCP/PTY 路由、监听与关闭 |
| `runtime/` | 后端库、产品命令、任务/计划、执行协调与恢复 |
| `core/` | 不可变 Agent 模型、执行契约、共享工具 schema |
| `storage/` | SQLite、绑定/用量与文件请求账本、事务 |
| `driver/` | Pi/Codex/Claude/AGY 原生执行适配 |
| `gateway/` | provider HTTP transport 与模型协议 codec |
| `protocol/` | 共享消息与控制类型 |
| `image/` | guest 桌面、浏览器、broker/viewer 资源 |
| `scripts/` | 构建、打包、配置导入与图标生成 |

`poc/` 已移除，实验代码和证据可从 Git 历史查阅。`.local/poc/` 只是沿用的本机构建/下载缓存路径，不依赖旧实验源码。

机器连接与部署记录统一在 `~/Workspace/Machines`；不要提交凭据、用户会话或构建产物。在线检查走 Runtime API，不从宿主打开运行中 guest 的 SQLite。

### Claude 离线契约

Pot 只通过官方接口检测 Claude 状态；需要认证的用户自行在普通 Terminal 处理。
在助手设置填写模型并明确选择 Claude。当前项目验收按用户决定跳过 Claude 真实账户测试（无账号）。
无需真实账户的开发验证使用原版 CLI + 本地模拟 Anthropic 服务：

```sh
mise exec -- python scripts/tests/claude-runtime.py --runtime .local/poc/cargo-target/debug/pot-server --claude /absolute/path/to/claude
.local/d1/helper-build/release/PotImageCheck .local/d1/claude-guest-check .local/d1/resources scripts/tests claude-guest.sh
```

guest 检查遵循上面的 Helper 签名及 VM 执行前提。测试使用独立临时 HOME，不复用宿主凭据。


### Terminal 进程清理（Linux guest）

```sh
.local/d1/helper-build/release/PotImageCheck .local/d1/terminal-guest-check .local/d1/resources scripts/tests terminal-guest.sh
```

使用新的隔离结果目录和上面的 Helper 签名。验证关闭 PTY、Runtime 正常退出及重启；
包含前台 sleep、独立后台进程组和忽略 HUP/TERM 的后台 shell/孙进程，不调用模型。
刻意 setsid 脱离终端会话的进程不属于此契约，仍由 VM 生命周期终止。


### AGY 驱动回归

AGY 采用原版 CLI 接入，按用户决定开放主聊、后台任务、计划和汇总；不提供 Pot 编排工具，角色依靠提示词约束。
用户自行在 Terminal 登录，状态保持 unknown。无需账号的模拟传输回归：

```sh
mise exec -- cargo build --locked -p pot-server
mise exec -- python scripts/tests/agy-runtime.py --runtime .local/poc/cargo-target/debug/pot-server
```

使用上面的 Helper 签名与全新无网络 guest 目录，验证打包 Runtime 的同一回归，
以及原版 CLI help 和空 HOME的 headless 失败退出：

```sh
.local/d1/helper-build/release/PotImageCheck .local/d1/agy-guest-check .local/d1/resources scripts/tests agy-guest.sh
```

模拟传输不能证明原版 CLI 模型/工具行为；原版离线失败 smoke 也不算真实账户或角色隔离通过。
