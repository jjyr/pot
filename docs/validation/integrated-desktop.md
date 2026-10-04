# P3：同窗口远程桌面与控制权

> 旧 `poc/` 源码已移除，下方实验命令仅适用于链接中的历史提交。

> 历史验收记录：版本、额度与界面均以各节日期为准。当前能力见 [开发计划](../development-plan.md)，操作规范见 [文档索引](../README.md)。

日期：2026-10-03。结论：**有限通过**。GPUI/WKWebView 已显示并操作真正远程 Docker 桌面，接管、交回、隧道断线恢复已实测。没有把完整输入法、性能矩阵或最终 tray 连续演示算作通过。

## 复现与固定边界

入口见 [README](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/integrated-desktop/README.md)。先按 P2 启动专用容器和隧道，再运行 `poc/integrated-desktop/poc prepare`、`run`、`open`（构建 GUI 用 `poc/macos-shell/poc build`）。连接参数来自 Machines，沿用 P2 固定镜像、TigerVNC、noVNC 1.6.0、GPUI 0.2.2；Python 依赖固定在 requirements.txt。环境引用：`~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`。

本机 broker 监听 loopback 17681；随机 token 写入任务目录的 0600 URL 文件。不要分享该 URL。退出 GUI 不停止远程环境；停止命令见 README。未登录任何真实网页账号。

## 实测证据

| 操作 | 状态 | 证据 |
|---|---|---|
| WKWebView 显示同一远程桌面、driver 写入 | 通过 | 真实原生窗口截图在执行记录；native-human-input.json 含远端页面事件与 session |
| 原生 Take over、拒绝 Driver write | 通过 | UI 提示 Human owns input；真实协议复验 broker-acceptance.json |
| 中文 Clipboard → canvas Ctrl+A/V → 交回读回 | 通过 | 精确读回 `P3 中文接管 123`，native-human-input.json、returned.json；这是粘贴，不是 IME |
| 人工接管时断隧道、恢复、重连 | 通过 | disconnected.json 保留 human，returned.json 读到同一文本；无自动抢回 |
| 在途 driver、RFB 输入门禁、修饰键释放 | 通过 | verify_broker.py 连接真实远端 CDP/RFB，检查未认证 403、human 下 driver 409、在途先完成、QEMU 按键释放与 Fence 确认；不是 fixture |
| 关闭窗口后 tray 重开完整 P3 序列 | 有限通过 | P1 用户已确认 tray 关闭重开；P3 曾被菜单校验死锁中断。修复后原生窗口可操作，但没有重新完成整段 tray 序列 |
| 输入法、完整鼠标/缩放矩阵、弱网延迟 | 未测 | 沿用用户收敛范围；不以中文粘贴或协议测试替代 |

小型证据位于 [evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/integrated-desktop/evidence)。原生日志、完整 broker 日志保留忽略目录 `.local/poc/`，截图在本次工具执行记录，无额外声称已保存录像。

## 发现与修复

- RFB SetEncodings 每项长度为 4 字节，修正解析器；Python 3.9 的异步锁在 aiohttp startup 初始化。
- 原生自动化送进 WKWebView 的 mousedown 出现 `buttons=0`，Ctrl 快捷键带 ctrlKey 却缺单独修饰键过渡。页面修正矛盾字段，通过 noVNC 公共 sendKey 同步 Ctrl；控制 epoch 变化时重置跟踪。输入仍由真实原生点击/按键产生，没有用 DOM 写入冒充人工输入。
- Return 先禁输入，释放已按下的标准/QEMU 键和鼠标，等待真实 RFB Fence BlockBefore 回执，再读取页面，最后授权 agent。broker 自己的 Fence 回执必须消耗，透传给 noVNC 曾导致断开，现已修复并在本地同一 viewer 复验。
- AppKit 菜单自动校验重入 GPUI mutex 导致死锁，进程采样定位到 `NSMenu _enableItems → validate_menu_item → parking_lot`。自有主菜单、Edit、状态菜单禁用自动 enable，修复后原生访问恢复。只修这个集成缺陷，不扩大 P1。

## 结论与边界

保留 GPUI/WKWebView + TigerVNC/noVNC，broker 以显式 owner/epoch 管理两个合作输入通道；重启默认无 owner。P4 复用相同页面与 broker。仍有断线后旧错误文字可能残留的展示问题，权威 owner/phase 会刷新。

这不是恶意 Agent 隔离：所有者仍可从原始 P2 隧道、Docker 或任意 shell/CDP 绕过。进入产品骨架前须明确这些通道的权限边界。用户补充“可以输入”已记入 P2，但未说明输入方法，不据此升级为中文 IME 通过。
