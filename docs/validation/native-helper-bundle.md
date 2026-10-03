# P4：Swift helper、本地 VM 与独立 App

日期：2026-10-03。结论：**有限通过**。自有 Swift helper 直接链接 Containerization，已从移动后的 App 实际启动、操作、停止本地桌面；不需要用户安装 Docker，也不依赖宿主 Python 或 cctl。当前是本机 ad hoc 签名原型，不能据此宣称可公开分发。

## 复现、版本与资源

完整入口见 [README](../../poc/native-helper/README.md)：

```sh
poc/native-helper/poc prepare
poc/native-helper/poc build
poc/native-helper/poc bundle
poc/native-helper/poc open
```

默认产物 `.local/poc/pot Local POC.app`。移动整个 App 后正常打开，点击 **Start local desktop**，等待 viewer 出现。首次拉镜像并安装依赖，实测约数分钟；暖 APK 缓存约几十秒，这只是观测，不是性能承诺。Stop 停止 VM；窗口关闭保留环境；Quit 清理 helper/VM。数据默认 `~/Library/Application Support/pot-poc`，workspace/profile 持久化。机器运行记录统一在 `~/Workspace/Machines/machines/local-mac-m4/pot-macos-shell-20261003.md`。

固定项：Containerization 0.47.0 commit `bc994b88df46207fad7775b0eabc51947e315881`，传递依赖 Package.resolved；Kata 3.32.0 kernel 与 noVNC 1.6.0 下载 SHA256 在入口校验；Alpine/init OCI 固定 digest 在 Swift 源码；294 项 APK 精确版本在 packages.lock。Chromium 142.0.7444.59、TigerVNC 1.15、aiohttp 3.11.15。资源在 App 内相对定位，首次 OCI/APK 仍需网络，不是离线包。

构建期间复用过旧实验依赖缓存以节省下载；源码和默认入口没有该硬编码依赖。全新 Mac 冷构建、上游旧 APK 长期可取得性未测。

## 实测证据

| 场景 | 状态 | 证据/范围 |
|---|---|---|
| 自有 helper IPC + 真实 VM | 通过 | initial-helper.jsonl；mode0600 Unix socket、request ID、异步 start/status/stop，不调用 cctl |
| 移动 App 独立运行 | 通过 | App 移至 `.local/poc/relocated/pot Local POC.app`，LaunchServices 打开，真实原生 Start 显示 VM 桌面；签名 deep/strict 验证通过，helper 无开发路径动态库 |
| 共用 viewer、driver、接管/交回 | 通过 | native-local-returned.json 精确读回 `P4 本地桌面 中文 123`；最终构建 final-native-returned.json 为 `P4 最终恢复 中文 456`；均由原生 Clipboard 和 canvas 按键输入/保存 |
| 重启后 profile 持久化 | 通过 | 原生 viewer 再次显示相同 session 和保存文本 |
| 重复 start、启动中 stop | 通过 | lifecycle.json，真实 minimal probe VM；同 generation，不是 GUI 工作负载验收的替代 |
| guest exit42、helper SIGKILL 与重启 | 通过 | lifecycle.json；明确 failed、陈旧 socket 恢复、重新启动成功 |
| 缺 generated initfs、缺 kernel | 通过 | 前者由缓存的固定 OCI 重建，后者明确错误；不代表 registry 中断恢复已测 |
| 无效数据根目录 | 通过 | invalid-root.json：目录位置是普通文件，干净退出1；真实权限拒绝矩阵未测 |
| GUI SIGKILL、父进程 watchdog | 通过 | gui-crash.json：活动桌面下 GUI 崩溃，helper 退出、socket 删除，约2.54秒完成清理，旧桌面无 HTTP 响应 |
| 原生 Quit 清理 | 通过 | app-quit.json； 同时核查 helper/socket 和实际 HTTP；不用 TCP connect 单独判断 VM 是否存活 |
| 分发/完整故障矩阵 | 未测 | Developer ID、notarization、干净机器 Gatekeeper、下载中断、只读数据目录、sleep/wake、长期资源趋势 |

证据目录：[evidence](../../poc/native-helper/evidence/)。`verify_lifecycle.py` 使用独立 fault-runtime 和真实 Alpine VM 的 probe.sh；native viewer 路径单独实测。截图在工具执行记录，未保存完整录像。

## 修复与限制

helper 对 runtime root 加 flock，清理仅限固定 POC 容器；启动代次防止旧任务覆盖状态，取消等待清理完成。父进程死亡和 SIGTERM 触发停止，15秒最后退出兜底；SIGINT 转发清理。VM 启动不等于 viewer ready，App 轮询真实 HTTP 后才加载页面。

曾强制停止 Chromium，重启出现 Restore pages 提示；改为主进程 exec Chromium，先 TERM 并给予2秒再停止 VM。最终正常 Cmd+Q 后 guest exitCode 为0（final-app-quit-helper.jsonl）、helper/VM 已清理，但 profile.exit_type 仍为 Crashed（app-quit.json），因此 **Chromium 干净退出未通过**，TERM 宽限并未解决全部退出问题；后续须接入浏览器关闭确认，不能宣称已修好。仅能确认已保存测试标记恢复。

退出初验仅 socket.create_connection 成功，误将 guest listener 记为存活；随后 HTTP 请求0字节超时，旧 helper 已退出。最终证据保留这一初验现象，并以应用层请求判定桌面是否仍可服务；不把 TCP 建连成功归因于已证实的特定网络实现。

原生菜单校验死锁和 viewer Ctrl/Fence 缺陷由 P3/P4 实测发现并修复，见 P3 报告。桌面沿用 P2 测试页，页内标题仍写 remote Docker；**backend 的权威标识是上方 local-swift-vm**。

本地 Chromium 未传 --no-sandbox，普通用户运行；最终 chrome://sandbox 各项状态未验收，不能沿用 P2 结果。VNC/CDP/websockify 仅 guest loopback，共用 broker 在私有 vmnet 上需随机 token；这不是恶意 guest/shell 的完整隔离。

## 技术决策

保留用户进程 Swift helper + 固定 Containerization，暂无证据要求 privileged helper。GPUI/Swift 通过小型 IPC 管生命周期，共用 viewer/control 协议可以进入产品骨架。下一阶段优先补分发供应链、首次下载/失败反馈、全输入通道隔离和原生 IME；长期身份/记忆/任务仍参考已有研究，不在本轮展开。
