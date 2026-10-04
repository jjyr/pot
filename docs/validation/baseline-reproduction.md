---
{ tags: [ POC, Containerization, 虚拟化 ] }
---
# pot 研究 POC

> 旧 `poc/` 源码已移除，下方实验命令仅适用于链接中的历史提交。

> 历史验收记录：版本、额度与界面均以各节日期为准。当前能力见 [开发计划](../development-plan.md)，操作规范见 [文档索引](../README.md)。

[poc/baseline](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline) 保存可检查的实现和证据，对应 [pot产品架构研究与POC验证](baseline-report.md)。基础 POC，不是完整产品。测试日期 2026-10-03。

> 本文从笔记迁入，保留原实验条件。以下代码命令在仓库的 `poc/baseline` 目录运行；部分脚本仍依赖历史 `/tmp/pot-research-20261003` 缓存。新 POC 的第一步会消除这些固定路径。

## 内容与范围

| 文件 | 验证什么 |
|---|---|
| runtime.py / test_runtime.py | 真实 SQLite、子进程崩溃、outbox、binding epoch、计划、授权、偏好、更正/遗忘和合作式接管；22 测试 |
| test_adversarial.py | 提前交回、未知动作/规则拒绝、恢复 binding 失效；4 测试，修复前失败证据保留 |
| pi_turn_fixture.py | 实际 Pi 工具循环、session 持久化/重启、执行中 abort/steer；模型端是本地固定响应 fixture |
| local_oauth_poc.py | 本地 HTTP OAuth fixture，state/PKCE/单次/过期/撤销；3 测试，非 GitHub |
| probe_engines.py | 实际 Codex/Pi/Claude 进程和 OpenCode HTTP 协议探针，无模型 turn |
| probe_agy.py | Google Antigravity SDK 的生命周期，无模型 turn，使用无效 fixture key |
| native-gpui / native-swift | GPUI 0.2.2 窗口、Swift C ABI、NSStatusItem、同窗口 WKWebView/noVNC |
| rfb_fixture.py | 本地合成 framebuffer，用于独立显示/键传输测试 |
| rfb_vm_proxy.py | loopback WebSocket 到临时 Linux VM VNC；没有产品认证，只用于 POC |
| guest/desktop-poc.sh | guest Xvfb/Chromium/x11vnc/中文字体验证，五分钟后自动退出 |
| containerization.patch | Apple library 0.47.0 cctl 示例的临时状态、init pin、网络开关修改 |
| virtualization.entitlements | POC ad hoc virtualization entitlement，非发行签名 |
| evidence/ | 测试、引擎、VM、截图、来源 manifest；成功和失败均保留 |

## 最小测试：不依赖下载、不调用模型

在本目录运行 Python 3.9+：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 test_runtime.py
PYTHONDONTWRITEBYTECODE=1 python3 local_oauth_poc.py
PYTHONDONTWRITEBYTECODE=1 python3 test_adversarial.py
```

预期 22 + 3 + 4 tests OK。测试用临时数据库及 loopback fixture，不动用户账号状态。Runtime runner 会更新 evidence/runtime-tests.json；不要把此结果说成语义记忆或硬隔离通过。

## 原生与 VM 复现前提

本次临时资源根目录固定为 `/tmp/pot-research-20261003`。下载/build 缓存未复制进笔记；临时目录清理后需要按以下版本重新准备。现有源码含固定路径，迁移根目录需要一并更新 Python ROOT、Swift 输出/URL、build.rs 和 Apple patch；不能直接在任意目录保证运行。

- Apple Containerization tag 0.47.0，commit `bc994b88df46207fad7775b0eabc51947e315881`，clone 至 `$POT_POC_ROOT/containerization`，应用本目录 patch。
- Kata 3.32.0 `kata-static-3.32.0-arm64.tar.zst`，从官方 release 下载并核对研究报告中的 SHA256，解压至 `$POT_POC_ROOT/kernel`。
- init image `ghcr.io/apple/containerization/vminit:0.47.0`；Alpine `docker.io/library/alpine:3.22`。实际 index digest 见 evidence/vm-results.json。严谨重跑使用 digest pin。
- noVNC tag v1.6.0，clone 至 `$POT_POC_ROOT/noVNC`。
- Swift/Xcode macOS SDK、Rust/Cargo；GPUI 由 Cargo.lock 固定，开启 runtime_shaders。
- 原生 AppKit 与 Virtualization 运行需要系统允许的执行上下文。本轮受限环境启动需要批准后的执行；不要求关闭 SIP。

```sh
export POT_POC_ROOT=/tmp/pot-research-20261003
mkdir -p "$POT_POC_ROOT/vm-data" "$POT_POC_ROOT/swift-cache" "$POT_POC_ROOT/swift-config" "$POT_POC_ROOT/swift-security"
# 先在 containerization checkout 内 git apply <本目录绝对路径>/containerization.patch
GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=http.version GIT_CONFIG_VALUE_0=HTTP/1.1 \
SWIFTPM_MODULECACHE_OVERRIDE="$POT_POC_ROOT/swift-modules" \
CLANG_MODULE_CACHE_PATH="$POT_POC_ROOT/clang-modules" \
swift build --package-path "$POT_POC_ROOT/containerization" --disable-sandbox \
 --scratch-path "$POT_POC_ROOT/swift-build" --cache-path "$POT_POC_ROOT/swift-cache" \
 --config-path "$POT_POC_ROOT/swift-config" --security-path "$POT_POC_ROOT/swift-security" --product cctl -j 4
codesign --force --sign - --entitlements virtualization.entitlements "$POT_POC_ROOT/swift-build/debug/cctl"
```

注意：有效 cctl 路径是 `swift-build/debug/cctl`；本轮新 Xcode 构建布局不应猜 arm64 子目录。cctl command passthrough **不能加 `--`**，否则 guest 会尝试执行名为 `--` 的程序。

```sh
"$POT_POC_ROOT/swift-build/debug/cctl" run \
 --kernel "$POT_POC_ROOT/kernel/opt/kata/share/kata-containers/vmlinux-6.18.35-197-debug" \
 --image docker.io/library/alpine:3.22 --id pot-poc-write --memory 1024 \
 --mount "$POT_POC_ROOT/vm-data:/data" /bin/sh -c 'printf "persisted-by-pot-poc\n" > /data/proof.txt'
"$POT_POC_ROOT/swift-build/debug/cctl" run \
 --kernel "$POT_POC_ROOT/kernel/opt/kata/share/kata-containers/vmlinux-6.18.35-197-debug" \
 --image docker.io/library/alpine:3.22 --id pot-poc-read --memory 1024 \
 --mount "$POT_POC_ROOT/vm-data:/data" /bin/sh -c 'test "$(cat /data/proof.txt)" = persisted-by-pot-poc && echo PERSISTENCE_PASS'
```

网络另用 `POT_POC_NETWORK=1` 和 `--ns 1.1.1.1`，guest wget Alpine HTTPS APKINDEX。默认 POC 不启用网络。

真实桌面：复制 `guest/desktop-poc.sh` 至 `$POT_POC_ROOT/vm-data/desktop-poc.sh`，以网络开启的 cctl run、3072 MiB memory、上述共享 mount 执行 `/bin/sh /data/desktop-poc.sh`。读取 vm-data/desktop/network.txt 中 eth0 地址，启动：

```sh
POT_VNC_HOST=<guest-ip> PYTHONDONTWRITEBYTECODE=1 python3 rfb_vm_proxy.py
```

另一个终端构建/启动 UI：

```sh
swiftc -swift-version 5 -module-cache-path "$POT_POC_ROOT/swift-modules" \
 -emit-library native-swift/Bridge.swift -o "$POT_POC_ROOT/libpot_native.dylib"
CARGO_HOME="$POT_POC_ROOT/cargo-home" CARGO_TARGET_DIR="$POT_POC_ROOT/cargo-target" RUSTC_WRAPPER= \
 cargo build --locked --manifest-path native-gpui/Cargo.toml -j 4
"$POT_POC_ROOT/cargo-target/debug/pot-native-poc"
```

原生 UI 自动检查、截图并退出（最多25秒），结果写临时根目录。fixture 与真实 VM proxy 占用同一个 loopback17410，必须先停止前者。UI 自带键测试，真实 VM proxy 追加 Ctrl+L，验证地址栏响应。VM 演示五分钟后退出，proxy 需 Ctrl+C 停止。POC 浏览器用了 --no-sandbox，VNC 无密码；不得作为产品隔离/认证配置。

## 引擎握手复现

脚本假定本轮准备的隔离目录与固定二进制；依赖缺失时会失败，不能把退出解释为支持能力。

- Pi 0.87.1 在 PATH；`PI_CODING_AGENT_DIR` 由脚本设置，关闭 extensions/skills/非必要流量。
- Claude SDK0.3.288 及匹配 darwin-arm64 native package，路径为临时根目录 `claude-sdk/node_modules/@anthropic-ai/claude-agent-sdk-darwin-arm64/claude`；配置隔离，无 API/OAuth key。
- OpenCode1.18.34 放在 `opencode-bin/opencode`；用临时 XDG_CONFIG_HOME/XDG_DATA_HOME/XDG_CACHE_HOME、OPENCODE_CONFIG_DIR 启动 `serve --hostname 127.0.0.1 --port 17409`，不使用用户配置。
- Codex0.159.3 在 PATH。本轮被已有 state backfill 阻塞，诊断见 codex-backfill.txt。不要为跑测试删用户数据库或锁。`POT_SKIP_CODEX=1` 会保留旧失败结果，只跑其余探针。
- AGY 用 task-local venv，安装 `google-antigravity==0.1.20`；脚本使用 deny-all policies，显式无效 API key，不调用 chat。若本机代理把 SDK loopback WebSocket 错送 SOCKS，使用只影响此子进程的 proxy unset/NO_PROXY；不要改全局代理。

```sh
POT_SKIP_CODEX=1 PYTHONDONTWRITEBYTECODE=1 python3 probe_engines.py
env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy \
 NO_PROXY='*' PYTHONDONTWRITEBYTECODE=1 "$POT_POC_ROOT/agy-venv/bin/python" probe_agy.py
```

这些是 Mac 原生协议/SDK验证。Linux ARM64 镜像内的真实 turn、执行中 cancel、恢复、权限回调仍需要专用有效凭据和完整 adapter。

## 证据与许可

fixture 与真实 VM 分开保存 native-viewer.png / native-vm-viewer.png。sources-manifest.json 包含抓取 URL 与实际快照 hash；源内容保留在临时目录，报告包含可访问来源链接。VM 结构化记录包含 OCI index digest，未声称 818 MiB 安装结果就是最终产品镜像体积。

gpui0.2.2 crate 声明 Apache-2.0；Apple library Apache-2.0；noVNC MPL-2.0；引擎及其他依赖按实际交付文件核对。此目录保存自写 POC 与小型 upstream patch，保留 Apple 原文件许可证；不包含上游完整源码、二进制、真实凭据或依赖缓存。最终产品须单独完成 NOTICE/SBOM、provider 分发认证与代码签名。

## Pi 工具循环与恢复复验

安装 Pi 0.87.1 后运行 `PYTHONDONTWRITEBYTECODE=1 python3 pi_turn_fixture.py`。此脚本不依赖上述固定临时下载目录；自身创建临时配置、工作区、session 与 loopback SSE 服务，退出时清理。实际 Pi 工具写文件、重启恢复、取消/steer；模型是 fixture，没有真实推理。证据 pi-turn-fixture.json，不能将其称为真实 provider 端到端验收。

## Codex Linux VM 基础协议验证

源码 `guest/codex_app_server_probe.py`，真实结果 `evidence/codex-linux-app-server.json`（5/5）。下载 `evidence/codex-linux-release.json` 的官方 ARM64 musl asset、验证 SHA256，解压到临时根目录 `vm-data/codex-poc/`；复制探针为同目录 probe.py。

使用上面 cctl 网络/mount/kernel 参数、2048 MiB memory、全新 VM ID，执行 `/bin/sh -c 'set -e; apk add python3; PYTHONDONTWRITEBYTECODE=1 python3 /data/codex-poc/probe.py'`。必须使用 PTY；默认 guest home 保持全新，不挂载宿主 ~/.codex、不设 HOME/CODEX_HOME、不复制凭据。脚本结果写共享目录 app-server-results.json。

通过项包括初始化、重复初始化错误、ephemeral thread start/read 和无活跃 turn 的 interrupt 错误路径。没有模型 turn，不能算执行中取消、持久 resume 或工具隔离通过。guest 的 PATH bubblewrap 警告保留在 evidence；产品完整工具验证须固定依赖。
