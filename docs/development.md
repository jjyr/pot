# 开发指南

## 开发与运行

需要 Apple Silicon、macOS 26+、Xcode/Swift 6.2+、Rust、cargo-zigbuild 和 Zig。

```sh
scripts/pot prepare
scripts/configure-pi
scripts/pot build
scripts/pot bundle
scripts/pot open
```

`bundle` 会在独立 VM 内预装依赖并生成完整 Sandbox OCI 镜像；只有构建阶段需要下载软件，App 启动直接加载镜像。镜像缓存、桌面字体与大小对比见 [Sandbox 镜像](sandbox-image.md)。

开发包为 `.local/d1/Pot.app`。`scripts/pot open` 使用 `.local/d1/local-runtime`（可用 `POT_LOCAL_ROOT` 覆盖）；直接双击默认使用 `~/Library/Application Support/pot`，两者数据不同。

`configure-pi` 仅从宿主 Pi 导入 DeepSeek 凭据到 guest 私有配置，不调用模型，不把密钥打入 App。模型为 `deepseek-v4-flash`；配置入口、默认额度和 K/M token 输入规则见 [UI 规范](ui-ux-design.md#配置额度)。已有配置不会自动覆盖，用量账本不重置。

## 验证

```sh
CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-runtime real_pi -- --ignored
```

第二条使用宿主已安装的 Pi 和本地模拟模型，无付费调用。真实 App/VM/模型验证记录见 [文档索引](README.md)，旧 POC fixture 不代表当前产品验收。

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
