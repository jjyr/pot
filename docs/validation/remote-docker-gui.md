# 远程 Docker GUI：P2 最小切片

> 旧 `poc/` 源码已移除，下方实验命令仅适用于链接中的历史提交。

> 历史验收记录：版本、额度与界面均以各节日期为准。当前能力见 [开发计划](../development-plan.md)，操作规范见 [文档索引](../README.md)。

日期：2026-10-03。结论：**有限通过**。真实远程 Linux Docker → SSH → 标准 noVNC → headed Chromium 已形成可操作闭环；尚不能称为 GPUI 内远程桌面或完整 P2 验收通过。P1 按用户决定收尾；本报告写作时 P3/P4 未测，后续结果见文末“后续实测补充”。

## 复现

机器选择、连接与部署事实见 `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`，本仓库不保存连接账号。实现入口：[remote-desktop README](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/remote-desktop/README.md)。

```sh
cd ~/Workspace/pot
export POT_REMOTE_SSH='<从 Machines 选择的 SSH destination>'
poc/remote-desktop/poc doctor
poc/remote-desktop/poc build
poc/remote-desktop/poc run
poc/remote-desktop/poc tunnel
```

浏览器打开 `http://127.0.0.1:17680/vnc.html?autoconnect=1&resize=scale`。当前实验容器与隧道保留运行，可直接试用。若已有容器，先用 `status` 检查；更新镜像或从停止状态重建，用 `remove` 后 `run`，两者不会删除 profile/workspace 卷。

亲手验证：

1. 在远程文本框输入英文，点击计数按钮，观察页面结果。
2. 展开左侧 noVNC 工具栏 → Clipboard，输入中文；点回远程文本框，用 **Ctrl+V** 粘贴（Linux 快捷键）。反向用 Ctrl+A/C，查看 Clipboard 面板。面板与系统剪贴板自动同步是不同能力。
3. 点 Save profile marker；用 `poc driver` 读取同一页面。关闭本地网页再打开，内容应保留。
4. `untunnel` 后 viewer 显示断线；重新 `tunnel` 并刷新网页，回到同一浏览器。不是自动重连承诺。
5. `remove` / `run` 后刷新，保存文本和 session marker 应保留。未点击 Save 的临时文本不承诺跨浏览器重启保留。

停止：`poc/remote-desktop/poc untunnel`，再 `poc/remote-desktop/poc stop`。`remove` 额外删除容器，保留卷、网络与镜像供复现；没有全局 prune。显式删除持久数据应另行决定。

## 固定版本与边界

- Debian base digest：`sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251`。
- Debian bookworm/security apt snapshot：`20261001T000000Z`；完整包清单在镜像 `/opt-packages.txt`。
- Chromium `154.0.8037.57-1~deb12u1`；TigerVNC `1.12.0+dfsg-8`；websockify `0.10.0+dfsg1-4+b1`。
- noVNC `1.6.0`，归档 SHA256 `5066103959ef4e9b10f37e5a148627360dd8414e4cf8a7db92bdbd022e728aaa`。
- 最终实际运行镜像 ID：`sha256:f7167e4c5fe56a6a68342f0435a0835ded8599d9c8fae91438ae37108d80efee`。构建记录 ID 后按 ID 启动；重新构建的 ID 可能不同。
- seccomp 源于 Moby profiles `2ceae35d351c156cb5a8efc0fdc4a08cf94569d8`，增加 clone/setns/unshare/chroot 供 Chromium 内层 user namespace 使用。来源及许可证随源码保存。
- 普通 UID 1000、cap-drop ALL、no-new-privileges、3 GiB RAM / 2 CPU / 256 PID / 512 MiB 私有 shm；专用 Docker bridge、独立 workspace/profile 卷。不挂宿主 home、浏览器 profile 或 Docker socket。
- host 仅 loopback 17680 发布 viewer；VNC 5900、CDP 9222 与测试页 8081 只监听容器 loopback。SSH 提供传输认证和加密；noVNC 的 “unencrypted” 指内层 WebSocket。未实现公网 WSS、多用户认证或恶意 Agent 隔离。

## 实测与证据

操作方式：本机隔离 headed Chrome，使用 Playwright CLI 真实点击 noVNC canvas、发送按键、操作 Clipboard UI；远端 CDP 只用于确定性 driver 和结果读回。不是 fixture，也不是人工输入法验收。证据目录：[evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/remote-desktop/evidence)。

| 项目 | 状态 | 事实与证据 |
|---|---|---|
| 远程桌面/同一会话 | 通过 | driver 写入中文多行，viewer 截图显示；viewer 追加 `!` 并点 Save，driver 读回一致。`driver-write.json`、`viewer-edited-saved.json`、`same-session.png` |
| 基本键鼠 | 通过 | 真实 canvas 输入英文、End/!、Ctrl+A/C/V、点击 Save 与计数按钮。`viewer-keyboard.json`、`tiger-counter.json`。四角/拖拽/断线按住键矩阵未测 |
| 中文粘贴 | 通过 | noVNC Clipboard → Ctrl+V → 远程 textarea 逐字读回 `你好，Pot！中文 English 123`。`tiger-clipboard.json` |
| 反向中文多行复制 | 通过 | 远端 Ctrl+A/C → noVNC Clipboard，精确值为 `driver → viewer 中文\nsecond line`。`tiger-reverse-value.txt`、`same-session.png`。不代表本机系统剪贴板权限流程已测 |
| 中文直接输入 | 失败 / IME 未测 | Playwright 对 canvas 直接发送 Unicode 未送达，TigerVNC 复测仍无对应文本；`tiger-unicode-direct.json`。真实 macOS 拼音 composition/候选提交到远端未测，不能以粘贴替代结论 |
| viewer scale | 通过 | 默认远端 1280×800，本地 viewer 缩放显示可操作；`restored-desktop.png` |
| 远端 resize | 有限通过 | noVNC remote resize + 本地 1000×700 后 xdpyinfo 为 1000×700，页面 screen 同步；但 Chromium innerWidth 仍 1279，未自动重新布局，fluxbox 出现 wallpaper 工具缺失提示。`remote-resize.json`、`resize-limitation.png`。默认保持 scale |
| 隧道断线/恢复 | 通过 | 关闭 SSH master，UI 显示 Disconnected；重建隧道/刷新后相同 session、文本、事件保留。`tunnel-disconnected.txt`、`tunnel-restored.json`。未测按住键时断线或弱网 |
| 容器 restart / 同卷 recreate | 通过 | 最终配置两种操作均恢复 session `0caac25c-c61f-46c8-a9c9-09286393027c`、已保存中文多行文本和 workspace 文件。`container-{restart,recreate}.json`、`workspace-{restart,recreate}.txt` |
| Chromium sandbox | 通过 | 最终 TigerVNC 镜像 chrome://sandbox 报 Namespace、PID/Network namespace、Seccomp-BPF/TSYNC 为 Yes。`tiger-sandbox.json`；未用 --no-sandbox |
| 性能/弱网 | 未测 | 仅空闲资源快照约 283.5 MiB、0.61% CPU、129 PID，不能当容量或性能结论。未测 RTT/丢包整形、端到端延迟 p50/p95、带宽矩阵 |

## 发现与修复

1. Docker 默认 seccomp 下 Chromium namespace sandbox 初始化失败。旧 Playwright profile 又触发该节点 runc/libpathrs SIGSEGV（容器进程尚未开始）。改用当前固定 Moby profile，明确放行内层 namespace 所需调用；保留原失败证据 `default-seccomp-failure.txt`、`old-profile-runtime-failure.txt`。
2. 最初 Xvfb+x11vnc 能传英文，中文 clipboard 变为 `???Pot??? English 123`（`viewer-clipboard.json`）。只替换显示服务器为 TigerVNC；相同真实 UI 路径复测中文精确通过。
3. 同卷重建时 Chromium SingletonLock 绑定旧容器 hostname。启动先对专用 profile 取得 flock 独占锁，再清理残留 Singleton 文件；避免两个本 POC 容器并用 profile。
4. 容器 restart 保留 `/tmp/.X99-lock`，新 X server 拒绝启动。入口清理容器私有 X99 残留并检查 X readiness，最终 restart/recreate 均通过。
5. 远端 resize 已知窗口适配问题保留；本轮不扩展 WM 调度逻辑。默认 scale 可用。DBus 缺失日志不影响本次测试页，但未覆盖需要系统服务的网页功能。

## 下一步

保留 GPUI/WKWebView 与 noVNC 路线，远端组件使用 TigerVNC。P2 最小闭环足以进入 **P3：将标准 viewer 接进已有 WKWebView，并做最小接管/交回 broker**。中文先明确使用 Clipboard fallback，输入法直输和 resize 适配需在集成时针对性确认。当前 CDP driver 可自由操作浏览器，不具备 P3 的控制权仲裁，更不代表任意 shell/CDP 已受隔离。Swift helper/本地运行时 P4 仍是后续独立未知项。

## 后续实测补充

用户亲手确认「可以输入」，未说明输入方法，因此不替代上述 IME 未测/自动化 Unicode 直输失败结论。P3 已完成 WKWebView 集成与合作式控制权最小切片，P4 已完成本地 helper/App 最小切片，见相应报告；上节“下一步”为 P2 当时的阶段记录。
