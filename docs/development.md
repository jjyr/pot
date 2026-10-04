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

设置 → Agent 填写账户可用的模型名称并选择 Codex；在 Sandbox Terminal 运行
`codex login --device-auth`。登录态只由 guest CLI 管理，保存在持久 engine-home。

无凭据集成测试（显式指定安装好的官方 Codex CLI，不调用付费模型）：

```sh
mise exec -- cargo build -p pot-runtime
mise exec -- python scripts/tests/codex-runtime.py --runtime .local/poc/cargo-target/debug/pot-runtime --codex /absolute/path/to/codex
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
| `runtime/` | Rust Control API、SQLite、Pi、任务与调度 |
| `protocol/` | 共享消息与控制类型 |
| `image/` | guest 桌面、浏览器、broker/viewer 资源 |
| `scripts/` | 构建、打包、配置导入与图标生成 |

`poc/` 已移除，实验代码和证据可从 Git 历史查阅。`.local/poc/` 只是沿用的本机构建/下载缓存路径，不依赖旧实验源码。

机器连接与部署记录统一在 `~/Workspace/Machines`；不要提交凭据、用户会话或构建产物。在线检查走 Runtime API，不从宿主打开运行中 guest 的 SQLite。

### Claude 离线契约

Sandbox Terminal 使用 `claude auth login` 完成官方登录；在 Agent 设置填写模型并选择 Claude。
无需真实账户的开发验证使用原版 CLI + 本地模拟 Anthropic 服务：

```sh
mise exec -- python scripts/tests/claude-runtime.py --runtime .local/poc/cargo-target/debug/pot-runtime --claude /absolute/path/to/claude
.local/d1/helper-build/release/PotImageCheck .local/d1/claude-guest-check .local/d1/resources scripts/tests claude-guest.sh
```

guest 检查遵循上面的 Helper 签名及 VM 执行前提。测试使用独立临时 HOME，不复用宿主凭据。
