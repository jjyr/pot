# UI / UX 改版验证

日期：2026-10-03  
设计依据：[UI / UX 设计](../ui-ux-design.md)。状态：当前 Pi MVP 界面切面通过下列验证；不代表后续多引擎、完整冷安装 Setup 或网页隔离已交付。

## 已实现

- 原生 56px 图标导航：聊天、Sandbox、设置；hover 名称；聊天中央不放头像。
- GPUI 蓝灰气泡、深浅色、基础 CommonMark 正文、复制、工具折叠、成功 write/edit 的工作文件入口。产物仅在工作目录内解析，Finder 显示文件，不执行生成文件。
- 原生 NSTextView 多行输入与中文粘贴、Enter 发送、Shift+Enter 换行、停止。页面切换保留草稿；上翻历史时保留位置，提供回到最新消息入口。
- Sandbox 全页 noVNC，精简工具栏、接管/交回、重连、剪贴板、重开浏览器、打开工作目录、管理/停止/重启环境。移除 viewer 调试表单，浏览器使用 Pot 欢迎页。
- 环境信息：真实 OCI 镜像引用和压缩层大小、运行时长、CPU 平均使用率、内存、系统盘使用与容量、工作文件逻辑占用。工作文件统计每 30 秒更新。
- 设置：系统/浅色/深色；Pi + DeepSeek 密钥与额度；Sandbox CPU/内存/系统盘。配置验证后原子保存、正常重启；任务执行或 Runtime 状态未知时不应用配置。密钥留空保留，文件模式 0600。
- 首次无 Pi 配置时使用同一 Agent 表单进入 Setup。当前唯一可选执行引擎为已验证的 Pi，模型固定 DeepSeek Flash。
- 原创 U 形锅身与悬浮锅盖，App/Dock/侧栏/tray 使用同一造型。工作时锅盖轻微起伏，减少动态效果时静态抬盖；需要认证时倾盖。tray 默认无文字，有未读显示数字，菜单仅 Open / Hide。
- 未读计数独立于任务状态，按完成的助手消息 ID 去重并持久记录已读；停留在 Sandbox 或历史区不会清除，查看聊天底部后清除。

## 验证

| 项目 | 结果 |
|---|---|
| 构建 | macOS App、Swift helper、ARM64 Runtime 构建通过；bundle ad hoc deep/strict 签名通过 |
| Rust 自动化 | App 6 + protocol 1 + Runtime 8 项通过；收费模型无关的原有 Pi 集成测试本轮未重复运行 |
| Swift 未读状态 | 首次历史基线、重复消息、隐藏/历史视图、读取、重开恢复通过 |
| 原生 UI | 窄侧栏导航、设置深浅色、聊天气泡、中文粘贴、Shift+Enter、多行草稿切页保留实际操作通过 |
| Agent 设置 | 非法 token 值被拒绝；恢复原值后经确认保存、正常重启并恢复连接；历史与用量保留，密钥仍为 0600 |
| Sandbox | noVNC 实际连接；信息面板展示真实指标；浏览器关闭后仍连接，空桌面交还控制成功；工具栏重新打开浏览器成功 |
| 真实发送 | 原生输入框发送短测试消息，Pi 返回“已收到”；无工具调用 |

本轮真实发送仅新增 **1 次 DeepSeek 请求**：2350 输入 token（2176 缓存命中）、2 输出 token。Pi ledger 从 5 增至 6，没有重置额度。

测试入口：

```sh
CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
swiftc -module-cache-path .local/d1/swift-modules app/swift/UnreadTracker.swift app/tests/unread.swift -o .local/d1/unread-tests
.local/d1/unread-tests
scripts/pot prepare
scripts/pot build
scripts/pot bundle
scripts/pot open
```

构建与测试日志在忽略目录 `.local/d1/ui-*.log`。机器当前部署与资源观测记录在 `~/Workspace/Machines`。

## 修复与边界

- 发现旧 helper 只设置容器 cgroup 限额，VM 使用框架默认 1 GiB / 4 vCPU。现在同时传入 `VMResources`，为 guest 内核预留框架建议的 128 MiB。实测配置 3 GiB 后，VM 可用内存约 2.98 GiB；CPU 配置 2。
- 修复原生透明设置页与 Metal 视图叠加导致的黑背景，并对原生 layer 明确处理深色背景。
- 浏览器已关闭时交回桌面仍执行 RFB release/fence，不再要求不存在的 CDP 页面响应。
- 当前配置检查验证格式与范围；不会为了验证 API key 自动消耗模型请求。不能凭“配置已保存”推断任意新 key 可用。
- 其他执行引擎接入、跨引擎上下文迁移、任务列表、附件、完整产物预览仍属于后续产品能力；未添加假入口。基本 Markdown 不包含完整表格排版或内联图片预览。
- 当前协议尚无通用 action-review 等待事件，倾盖等待状态接到 NeedsAuth；后续新增等待事件时需继续接入。控制权 UI 不替代 D4 的独立 Agent/controller 隔离。
- 完整冷安装下载向导、下载续传、发行签名仍未交付；本次复用的是 Agent 配置表单与应用流程。

## 2026-10-04 细节打磨

App 编译和 bundle deep/strict 签名通过。实际检查侧栏合并、选中颜色、通用/Agent 设置对齐、用量限制折叠/展开、聊天复制 hover 显隐与复制后粘贴内容；测试草稿已清除。没有保存或更换凭据，没有发送模型请求。设置仍使用原生控件，消息复制使用 gpui-kit Clipboard。任务页改为居中限宽卡片和标题行新任务按钮。

### 灰色消息复制按钮对比度

助手消息的 hover 复制按钮增加独立底色、对比边框和轻阴影；浅色为白底灰边，深色为较亮灰底和浅边；按钮 hover 进一步强调底色。蓝色用户消息保持原样。编译及 bundle 签名通过。

### Sandbox 连接指示

标题保留 Sandbox，连接状态改为圆点：已连接绿色、连接中琥珀色、断开/错误红色。状态文字改为 title 悬停提示，保留键盘焦点及 aria-label。RFB connect/disconnect 立即刷新指示，状态 API 失败显示错误色。JavaScript 语法检查与 bundle 签名通过。

发现 WKWebView 可能复用旧版本 viewer 页面；LocalRuntime 的就绪探测和页面请求改为 reloadIgnoringLocalCacheData，确保升级后加载当前 Sandbox UI。Swift/Rust 构建通过。

### 聊天切页去除重复载入

Client::select 原先每次点击都清空 snapshot 并等待 500ms 轮询；现在同会话选择不改变状态，主会话使用持续更新的 main_snapshot，已读任务会话使用最多 16 条内存快照缓存。首次读取仍加载，不将其他会话内容当作目标内容。切页唤醒刷新线程，结果立即唤醒 GPUI；缓存不持久化到磁盘，Runtime boot_id 改变时清除任务缓存。

App 8 项测试通过（新增重复打开保留消息/错误、主任务切换缓存隔离两项）。App 构建与 bundle 签名通过；没有新增模型请求。冷启动 VM 等待、首次任务读取以及外层长历史虚拟化不在本次优化范围内。

## 2026-10-04：任务 / 设置 / Sandbox 统一

- 新增 `app/src/ui.rs`：共享页面、标题、说明、卡片与 Kit 设置表单。任务列表使用同一布局和主题边框。
- 设置从 AppKit 自绘页面迁至 Kit Form / Field / Input / Button；Swift 保留配置校验、原子保存与重启服务。Setup 复用表单。
- Sandbox 标题、连接状态、接管和图标操作迁至 Kit；WKWebView 仅占标题下方。环境信息与更多菜单仍是网页，应用内隐藏网页标题，统一控件圆角并同步主题。
- `cargo build --locked -p pot-app`、`cargo test --locked -p pot-app` 通过（8 tests）。使用现有本地 target 目录，bundle 已重新签名，应用已重开。
- 实机确认：三页切换、任务卡片显示、通用/Agent/Sandbox 表单、用量展开、⌘A 和粘贴、vCPU=0 的范围错误提示；恢复输入草稿，未保存配置。
- 实机确认：Sandbox 绿色连接点、环境信息包含镜像/CPU/内存/磁盘、更多菜单保留重连/浏览器/剪贴板/管理等项；深色主题同步到 viewer，最后恢复跟随系统。
- 本轮没有模型请求，没有修改认证、资源配置或任务数据。没有重新执行配置保存重启、接管桌面、最小窗口尺寸的完整回归。
- 简明规范已重写至 `docs/ui-ux-design.md`；新增普通 UI 优先复用 Kit 与 `ui.rs`，不再新增独立 AppKit 设置页面。
