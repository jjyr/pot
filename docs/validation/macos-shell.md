# macOS shell：P0 / P1 最小切片

后续用户决定：最小验证足够，停止继续 GPUI/tray 验证。下文未测项保留为证据边界，不再要求补齐后才能进入 P2。devpc 的内存不足已通过用户授权停止 llama-server 解决；更新记录见本文末尾。

日期：2026-10-03。结论：**有限通过**。可操作原型已实现；真实 tray 打开/关闭后重开有人工确认，真实键盘、拼音、焦点、缩放和显式退出有实测证据。没有把完整 P1 的 50 次循环、多屏、睡眠唤醒标为完成。P2 只读检查节点，P4 helper/VM 未实施。

## 复现

在 `~/Workspace/pot` 的正常 macOS Terminal 中运行：

```sh
./poc/macos-shell/poc doctor
./poc/macos-shell/poc build
./poc/macos-shell/poc run
./poc/macos-shell/poc status
./poc/macos-shell/poc stop
```

默认产物、开发 `.app`、PID 状态和日志在 `.local/poc`。可设置 `POT_POC_ROOT=/任意/可写目录`，所有命令使用相同值；路径可以包含空格。不要改 HOME/CODEX_HOME。`run` 生成 run ID，等待 `web.ready`；重复启动拒绝已有匹配进程，锁阻止入口并发竞争；`stop` 校验 PID 对应的完整可执行路径后发送 SIGTERM。关闭窗口仅隐藏，保留同一窗口/WebView；显式退出释放 WebView message handler、观察者、monitor 和 status item。此切片不启动 helper/VM/HTTP server。

缺 Rust/Cargo/Xcode 工具时 `doctor` 给出诊断。首次构建需要 Cargo registry 网络或缓存。受限 agent sandbox 曾无法连接 LaunchServices，`ps` 也被禁止；使用获准的正常桌面执行后成功，不需要改变系统安全设置。

亲手验收：

1. 点击右上角 **pot POC → Open pot**，分别点击左 GPUI 和右 WebView 输入框。
2. 选择系统拼音输入法，逐键输入 `nihao`，空格选首项「你好」；输入英文/数字，再输入 `ni` 后 Escape，确认已提交文本不变。左右来回切换。
3. 右侧输入 `你好，Pot！中文 English 123`，点击「核对标准文本」。粘贴与输入法必须分开记录；粘贴成功不证明拼音正确。
4. 测试 Cmd-A/C/V、方向键和退格。右侧 `abc123 → 左移 → 退格` 应为 `abc13`。
5. 点绿色窗口按钮的缩放动作，再还原；确认右侧重新排版、点击位置和输入正确。
6. 红色关闭 / Cmd-W 后从 tray 重开，文字仍在。点 **Quit pot POC** 或 Cmd-Q；`status` 应为 `null`。再 `run` 可以重启。

POC 保留 Dock，便于 Cmd-Tab；当前不是 tray-only 产品策略。测试输入会记录到本地 JSONL，请只用测试文本。

## 固定版本和构建边界

| 项目 | 本轮值/处理 |
|---|---|
| GPUI | `=0.2.2`，`runtime_shaders`，完整 Cargo.lock |
| Unicode 分词 | `=1.13.3`，输入框适配自 GPUI Apache-2.0 示例，NOTICE/许可证随源码 |
| Rust / Cargo | 实测 1.98.1；依赖锁固定，不自动安装/升级工具链 |
| Swift / SDK | Swift 6.4，Swift language mode 5，macOS SDK 27 |
| 宿主 | arm64 macOS 27.0.1，单屏，backing scale 2；机器事实仍以 Machines 为准 |
| Swift bridge | build.rs 编译静态库；只链接系统 frameworks/Swift runtime，无临时 dylib rpath |
| 输入页 | `include_str!` 嵌入，无 HTTP 端口；WKWebsiteDataStore.nonPersistent |
| 开发 App | 原子替换 executable、ad hoc 签名；无 helper、VM、kernel/init、发行签名 |
| noVNC / Containerization / 镜像 | 此切片不使用、不下载；历史 pins 仍在 baseline-reproduction.md，P2/P4 实施时独立固定 |

先复用了历史 Cargo 缓存做快速构建，随后在默认 `.local/poc/cargo-target` 从空 target 编译依赖成功（约 61 秒，registry cache 复用）。默认路径启动成功；还将首次构建的二进制移到默认目录运行，`otool -L` 确认没有历史 dylib 依赖。**未做干净机器/全新 registry 下载验收**，所以 P0 全阶段仍有限通过。上游 `block 0.1.6`、`proc-macro-error2 2.0.1` 有 future-incompatibility 警告，当前编译通过。

## 实测记录

证据：[`results.json`](../../poc/macos-shell/evidence/results.json)、[`native-events.jsonl`](../../poc/macos-shell/evidence/native-events.jsonl)、[`ime-focus.png`](../../poc/macos-shell/evidence/ime-focus.png)、[`resources.json`](../../poc/macos-shell/evidence/resources.json)。JSONL 为实际输入/窗口事件，不是 fixture；原始日志保存在忽略目录，hash 见 results.json。时间统一 UTC，本文日期按 Asia/Shanghai。

| 场景 | 状态 | 方法、观测及边界 |
|---|---|---|
| 构建/启动/停止 | 通过 | locked build、默认根目录、移动 binary、readiness、SIGTERM stop 后 status=null，再 run 成功 |
| 真实 tray 点击/关闭后重开 | 有限通过 | 用户亲手确认“已点击，关闭后可以重新打开”；06:00:21Z open、06:00:37Z hide、06:00:40Z open，同一 window_number=461。工具没有暴露 status menu；没有内部调用伪装点击。50 次循环未测；该人工记录在后续焦点修复前 |
| GPUI ↔ WKWebView 焦点 | 通过 | 原生点击左框输入 GPUI-123、右框 Web-456；截图与各自事件日志核对，未串入另一框。最终构建也复测英文输入切换 |
| WKWebView 导航键 | 通过 | 真实键序列 abc123/Left/Backspace → abc13；Escape 后无新增字符，修复前失败保留 |
| 拼音最小切片 | 通过 | 两侧均逐个 native key 输入 nihao，再空格提交你好；Web 最终“你好 English 123”，GPUI 最终“你好 GPUI 123”；追加 ni/Escape 取消后保持原文。Web 有 compositionstart/update/end，GPUI 有 marked/commit 日志；不是粘贴替代 |
| 完整 IME 矩阵 | 未测 | 非首候选选择、候选翻页、更多中文标点、其他输入法、长文本/复杂选区还需补测。后续重启时系统处于英文源，nihao 直接落字，不将该轮标成 IME 通过 |
| Unicode 剪贴板 | 有限通过 | Web Cmd-A + 原生粘贴中英标准句，页面逐字比对成功；完整双向 GPUI/Web clipboard 矩阵未测 |
| Resize / Retina | 有限通过 | 真正执行窗口 zoom，1000×650 → 1470×891 points；Web 660×650 → 1130×891，scale=2。还原后文字保留，缩放后点击输入 ! 成功。跨屏 scale 未测 |
| 红色关闭 | 通过 | UI 自动化点击 close，window.hide、visible=false，进程存活；不是销毁后复制状态 |
| 显式退出 | 通过 | 真实点击应用菜单 Quit pot POC，06:06:50Z quit.request + bridge.cleaned，随后独立进程查询 status=null。tray Quit 与 Cmd-Q 本轮未单独实测 |
| 脚本 stop 清理 | 通过 | 最新版本 SIGTERM 进入同一清理路径，run 11f2a0b5 有 bridge.cleaned，重复启动成功 |
| 多屏 / sleep-wake / Cmd-Tab 矩阵 | 未测 | 只有一个屏幕；未主动让用户电脑睡眠；有 key/resign-key 事件，不替代完整切换矩阵 |
| 资源稳定性 | 有限通过 | 单次样本 RSS 117936 KiB、8 threads、12 numeric FDs、0 TCP listener；不含 WebKit 子进程合计。没有 50 次前后比较，不能宣称无泄漏 |
| 基线迁入回归 | 通过 | Runtime 22 + adversarial 4 + OAuth fixture 3，共 29 项；仅 fixture 回归，不作为原生交互证据 |

截图取自实际拼音/焦点测试。当时页面下方保留了之前标准句比较的“逐字相同”，这不是当前文本断言；后续已修正为输入/清空时清除旧结果。每项判定以对应时间/事件和逐字值为准。

## 发现与修复

1. **WebView 非打印键被 GPUI 抢先处理**：沿用旧嵌套方式，Left 插入 U+001C，Escape 插入 U+001B。GPUI 的 `performKeyEquivalent` 在 WebKit responder 之前进入输入法处理。改为同一 GPUI-owned NSWindow 内两个兄弟视图，由 HostView 根据 firstResponder 路由 key equivalents；相同真实按键复测通过。没有修改上游 GPUI crate。
2. **contentView 不是实际 GPUIView**：窗口 contentView 是容器；仅 makeFirstResponder(contentView) 会让左框吞键。对固定 GPUI 0.2.2 查找内部 GPUIView，鼠标点击明确恢复原生 firstResponder 和 GPUI FocusHandle。这个类名依赖需要在升级时重新验证。
3. **WKWebView Cmd-A/V 缺少 Edit responder menu**：添加标准 Cut/Copy/Paste/Select All 菜单后，Unicode 粘贴逐字成功。
4. **GPUI 示例 IME 选区偏移**：修正 marked-text replacement 的 UTF-16 相对偏移转换，不再把旧 replacement end 加到新 selection end。文本框仅为 POC，非完整编辑器。
5. **重复运行签名缓存**：直接覆盖开发 App 的 Mach-O 出现无 stdout 的立即退出，codesign verify 也报告 bundle resources 不匹配。改为临时文件原子替换 + 整个开发 bundle ad hoc 签名；run → stop → run 验证通过。
6. **误导性旧比较结果**：输入后清空之前的 compare 文案。

所有 AppKit/WebKit 修改在主线程；借用的 C 字符串在跨异步边界前复制，桥接没有反向 Rust 裸指针 callback。保留 GPUI 的 NSWindow delegate/主循环。隐藏期间对象有意保留，退出时移除 message handler 以断开 WKUserContentController 的强引用环。

## P2 节点检查

已读取 canonical inventory，选择开发用途的 `machines/devpc/machine.yaml.local` 做只读 SSH 检查；Docker daemon 可访问，但当前空闲资源不足，**候选存在，不等于桌面部署已就绪**。环境条件已写入：

`~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`

连接地址/账号和机器运行细节不复制到本仓库。没有 pull 镜像、创建容器/网络/volume、修改端口或停止现有服务。P2 前需要复核至少约 3–4 GiB 可用内存及磁盘预算，或选择其他专用节点；随后才固定桌面 image digest、loopback viewer 及 SSH tunnel。没有以同机 VM 冒充远程 Docker 验证。

## 下一步决策

保留 GPUI + Swift + WKWebView 进入 P2/P3：基本输入可行，已定位并修复真实桥接缺陷。P1 完整通过仍需 final-build tray 50 次循环与资源趋势、完整输入法/快捷键矩阵、多屏和睡眠唤醒。独立 App 的 helper IPC/VM 生命周期、签名分发、Gatekeeper 和冷机器安装属于 P4，本轮开发 `.app` 不能替代这些验收。

## 后续状态更新

按用户决定，GPUI/tray 最小切片到此收尾。上文完整 P1 补测清单不再作为本轮待办。

用户明确授权停止 devpc 的 llama-server 后，已停止对应用户服务并验证无残留 llama-server 进程；Docker 仍可用，内存已满足最小桌面预算。未禁用服务开机配置，未改模型配置或其他服务，尚未创建桌面容器。操作和恢复命令记录在 `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`。P2 下一步关注镜像/持久卷磁盘预算与真实远程桌面集成。
