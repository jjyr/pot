# Docker 容器 GUI 与可嵌入运行时调研

> 历史研究：版本与“当前”均指原文核对日期。实施现状见 [开发计划](../development-plan.md)，不是本报告中的建议或旧状态。

整理日期：2026-10-02

## 摘要

- Docker 并不自带桌面；GUI 来自容器里的显示服务器、应用和显示传输层，或者宿主机的显示服务。
- 最适合独立、多实例、远程访问的方案是：每个容器拥有独立虚拟显示，经 noVNC 或 Selkies 输出到浏览器。
- 多个 GUI 容器可以并行，软件渲染通常不要求 `--privileged`；真正限制通常是浏览器的 CPU、内存和视频编码开销。
- “嵌入 bin”要分清：程序控制外部运行时、链接运行时库、把整个应用环境打成一个文件。这三件事的成本完全不同。
- Linux 可评估 `crun/libcrun`、youki 的 `libcontainer` 或 Bubblewrap；macOS ARM64 可评估 `libkrun`，Apple silicon/macOS 26 可评估 Apple Containerization。

## 1. 支持 UI 后有多大？

### 1.1 先确定体积口径

| 指标 | 含义 | 注意事项 |
|---|---|---|
| 镜像下载量 | Registry 中压缩后的层大小 | 与解压体积不同；架构、版本、压缩格式影响结果 |
| 镜像逻辑大小 | 解压后各层大小之和 | 多镜像会共享层，不能直接累加作为实际磁盘占用 |
| 容器增量占用 | 可写层、浏览器缓存、profile 等 | 同镜像启动多个容器不会每次复制全部基础镜像 |
| 运行内存 | 应用、显示服务、编码器的实际内存 | 多个会话的应用内存通常不能像磁盘镜像层那样共享 |
| 平台额外开销 | Docker Desktop 的 Linux VM 等 | macOS/Windows 还需要 Linux 虚拟化环境 |

### 1.2 核实到的 GUI 基础镜像样本

查询时，项目 README 链接的 Shields.io 镜像大小徽章返回：

| 镜像 tag | 徽章显示的 image size |
|---|---:|
| `jlesage/baseimage-gui:alpine-3.22-v4` | 66.3 MiB |
| `jlesage/baseimage-gui:ubuntu-24.04-v4` | 92.2 MiB |

来源：[项目 README](https://github.com/jlesage/docker-baseimage-gui)、[Alpine 大小徽章](https://img.shields.io/docker/image-size/jlesage/baseimage-gui/alpine-3.22-v4)、[Ubuntu 大小徽章](https://img.shields.io/docker/image-size/jlesage/baseimage-gui/ubuntu-24.04-v4)。

**这些是第三方动态徽章数据，不是本机解压后的实测值，也不包含 Chrome。** 不能用它代表完整浏览器容器的体积。本次 Docker Hub API 获取未成功，尚未固定架构、digest 并交叉验证压缩层总量。

### 1.3 用于规划的量级，非实测结论

| 方案 | 量级判断 |
|---|---|
| 最小系统 + 虚拟显示 + 单个轻量 GUI 程序 | 有机会控制在数百 MB 解压体积以内，取决于应用依赖 |
| Chromium + 字体 + GUI/远程显示依赖 | 通常应按数百 MB 到 1 GB 以上的解压体积预留 |
| XFCE/KDE 等完整桌面 + 浏览器 + 常用工具 | 应按 GB 级评估，可能达到数 GB |

这些是工程预算量级，不是某个版本的保证。浏览器会话可先按每实例约 0.5–2 GiB 的内存预算做试验；页面、标签数、视频、分辨率都会显著改变结果，复杂页面可能超过此范围。

若只需要 Chrome 窗口，不必安装完整桌面。若只做网页自动化，headless 模式可以省去显示服务器和远程桌面层，但浏览器本体仍然存在。

本地测量方法：

```bash
docker image inspect <image> --format '{{.Size}}'
docker system df -v
docker ps --size
docker stats --no-stream
```

分别观察镜像逻辑大小、共享层/存储占用、容器可写层和运行内存。卷、浏览器缓存、日志、Docker Desktop VM 磁盘需另行检查。

## 2. 多个容器能同时支持 UI 吗？需要什么权限？

**可以。推荐每个容器独立显示服务、独立 profile 和持久化目录。**

### 2.1 不同显示方式的权限

| 模式 | 所需能力 | 主要风险 |
|---|---|---|
| 容器内虚拟显示 + noVNC/Selkies，软件渲染 | 普通容器运行权限，端口映射 | 远程桌面入口要认证；不需要宿主显示 socket |
| 容器连接宿主 X11 | X11 socket、`DISPLAY`、Xauthority 授权 | 同一 X server 上的客户端之间隔离较弱；不应使用 `xhost +` 全局开放 |
| 容器连接宿主 Wayland | compositor socket、运行目录及 UID/权限适配 | 暴露宿主图形会话接口，且不是所有应用都原生支持 |
| Linux GPU 加速 | 所需 `/dev/dri` render 节点及对应组权限；NVIDIA 通常需要驱动和 Container Toolkit | 扩大设备访问面；驱动/架构兼容性额外复杂 |

GUI 本身通常不要求 `--privileged`、`SYS_ADMIN`、host 网络或挂载 Docker socket。

应区分三种权限：

1. **调用 Docker 的权限**：rootful Docker socket / docker 组权限通常相当于宿主 root 权限。
2. **容器内用户权限**：应尽量让浏览器运行于普通用户，容器内 root 与宿主 root 不是同一个概念。
3. **浏览器 sandbox 权限**：namespace 和 seccomp 必须允许浏览器建立内部沙箱；不是直接关闭 sandbox。

Rootless Docker/Podman 可以降低宿主权限需求，但仍依赖 Linux 用户命名空间、UID/GID 映射等宿主配置。设备访问、网络及 cgroup 资源控制需在目标系统验证。

### 2.2 多实例隔离要求

- 相同内部端口可重复使用，映射到不同宿主端口，如 `127.0.0.1:5801:5800` 和 `127.0.0.1:5802:5800`。
- profile、下载目录、缓存和会话凭据不要跨不可信用户共享。
- 对每实例配置 CPU、内存、PID 上限，避免某一页面耗尽宿主资源。
- Chromium 要有足够 `/dev/shm`。Docker 默认通常为 64 MiB，可从每容器 `--shm-size=512m` 或 `1g` 开始试验；这是容量上限，不是立即占用的固定内存。
- Playwright 官方示例推荐 `--ipc=host`，但它削弱 IPC 隔离。多租户应优先尝试独立 IPC + 扩大 shm，再验证目标工作负载。
- 不要为解决启动问题而直接给所有实例 `--privileged`、`seccomp=unconfined` 或 `--no-sandbox`。

## 3. 可以直接访问 UI 吗？

可以，但“直接”的含义有三种：

### A. 在浏览器打开容器桌面/应用

```text
容器 GUI 应用 → 虚拟显示 → VNC/WebSocket → noVNC → 用户浏览器
```

noVNC 是 HTML VNC 客户端，不是显示服务器；需要 VNC 服务，若 VNC 服务不原生支持 WebSocket，需 websockify 等桥接。

也可使用 Selkies 等浏览器桌面流方案。当前 LinuxServer Webtop 文档的 HTTPS 入口为 `https://<host>:3001`；jlesage GUI 基础镜像示例 Web 入口为 `http://<host>:5800`，VNC 为 5900。

**只启动 Xvfb 不会自动产生可访问的 UI。** Xvfb 提供虚拟屏幕，还需截图接口或远程显示服务。

### B. 把容器里的窗口显示到宿主桌面

Linux 可以通过 X11/Wayland socket；Xpra 等可做单应用远程显示。不必输出完整桌面，但宿主授权、安全及兼容性处理更复杂。

macOS 上 Linux 容器不能直接创建原生 AppKit 窗口。可用浏览器远程桌面、VNC 客户端或 XQuartz；Linux GUI 不会因此变成真正原生 macOS UI。

### C. 嵌入你自己的应用界面

可将 noVNC 客户端放入应用 WebView，或用它的 JavaScript API 集成连接、缩放和输入。是否能 iframe 嵌入取决于认证、CSP、frame headers 和来源策略；流媒体方案还需验证 WebView 对编码、HTTPS 和 WebRTC 的支持。

**这里嵌入的是远程画面与输入通道，不是把 Linux 窗口变成本地 UI 控件。**

远程访问默认绑定 loopback，经 SSH 隧道或带认证的 HTTPS 网关访问。WebSocket 也要验证会话权限；不要裸露 VNC、CDP 或未经认证的桌面入口。

## 4. 可以运行/调用 Chrome 等浏览器吗？

**可以。** 根据需求分三种：

| 需求 | 推荐方式 |
|---|---|
| 自动访问网页、截图、提取 DOM、下载文件 | headless Chromium + Playwright/Puppeteer/Selenium/CDP |
| 人可以看见并操作浏览器 | headed Chrome/Chromium + 虚拟显示 + noVNC/Selkies |
| 自动化为主，必要时人工接管 | 在同一个 headed 浏览器上同时保留自动化连接和远程显示 |

CDP 是浏览器调试/控制接口，不是桌面显示协议；它不等价于“打开一个可交互的 Chrome 远程窗口”。它也不能直接控制任意其他 GUI 应用。

### 兼容与安全

- Playwright 在 root 用户下运行时会失去 Chromium sandbox；对于不可信网页，官方建议普通用户 + 合适的 seccomp profile。
- Alpine 使用 musl，不能默认认为所有官方浏览器包都兼容。Playwright 的 Firefox/WebKit 构建不支持 Alpine；可选发行版 Chromium，但要验证版本与自动化兼容性。
- Google Chrome 官方 Linux 常见分发为 amd64；Linux ARM64 不要默认认为有同等支持的官方 Chrome 包，应优先验证 ARM64 Chromium/Playwright 支持的构建。
- Apple silicon 上模拟 amd64 浏览器有额外性能和兼容风险，尤其与 sandbox、图形加速组合时。
- 字体、输入法、剪贴板、音频、摄像头、WebGL、DRM/受保护视频、下载上传都要按产品需求单独测试。
- Linux 容器中的 GPU 能力不等于 macOS 宿主 GPU 自动可用；取决于 VM 与运行时是否提供对应设备及驱动通道。

## 5. 有更适合嵌入 bin 的 Docker 类工具吗？

### 5.1 先明确你想消除什么

- **不希望用户安装 Docker daemon**：可选择 daemonless OCI runtime 或库。
- **希望程序通过 API 创建隔离环境**：可链接 libcrun/libcontainer/libkrun 等，或控制 sidecar runtime。
- **希望只有一个可执行文件，完全没有其他资源**：难度高得多。浏览器、字体、动态库、rootfs、VM 内核不会因为运行时可嵌入就消失；通常仍需随包资源或首次下载/释放。

### 5.2 方案比较

| 方案 | 适用平台/集成形态 | 能否承载 GUI/Chrome | 取舍 |
|---|---|---|---|
| `crun / libcrun` | Linux，C OCI runtime，可构建共享库供 FFI 链接 | 可以，提供 Linux rootfs 后自行搭建显示层 | 较匹配“无 Docker daemon + 嵌入 bin”；镜像获取、解包、网络、存储与生命周期要自己补 |
| youki `libcontainer` | Linux，Rust OCI runtime 的库组件 | 可以，GUI 栈仍需自行提供 | Rust 产品值得评估；宿主 namespace/cgroup/seccomp 需求仍存在，库 API/版本要验证 |
| Bubblewrap `bwrap` | Linux，通常作为小型子进程调用 | 可以隔离宿主已有 GUI 应用 | 无需完整容器镜像；不是 Docker 引擎，也不是内置固定安全策略的完整沙箱，依赖宿主应用和库 |
| Podman | Linux daemonless CLI/API；macOS 仍需 VM | 可以 | 不要求常驻 Docker daemon，但通常不是直接链接进单个 bin 的轻量库；较适合 sidecar |
| `libkrun` | Linux KVM；macOS ARM64 HVF，动态库/C API | 可承载 Linux GUI 用户空间；具体显示/设备通道需集成测试 | 最贴近可链接的轻量 VM 路线之一；不是完整 Docker 替代品，需内核/固件、rootfs 和 UI 通道 |
| Apple `Containerization` | Swift package；Apple silicon/macOS 26 为官方支持路径 | 可在 Linux VM 内运行 GUI 栈，经网络显示 | 适合 Swift/macOS 产品，每容器独立轻量 VM；不是零资源开销，需 Linux 内核/rootfs 与虚拟化签名权限 |
| Firecracker / Cloud Hypervisor | Linux/KVM，通常作为 sidecar 微 VM 管理器 | 软件渲染 + 网络远程 UI 原理上可行 | 适合更强隔离/多租户；不是开箱即用 GUI，也不天然是单 bin 库方案 |

这里的“可以”指能够承载相应 Linux 进程，不表示上述运行时都提供内置远程桌面、自动镜像管理或 Chrome 开箱即用支持。

普通 OCI 容器共享宿主内核。处理不可信用户或任意程序时，microVM 可增加内核隔离边界，但安全仍取决于设备、网络、目录共享和管理面配置。

## 建议选型

### 需求仅限网页自动化

先选 headless Chromium + Playwright，不安装完整桌面。若只需要 profile 隔离，可用 browser context；若需要进程/权限/租户隔离，不能把 context 当作容器安全边界。

### 需要多会话 GUI，且允许 Docker 作为外部依赖

先验证：每会话一个容器 + headed Chromium + 独立虚拟显示 + noVNC/Selkies + 认证网关。优先软件渲染，确认性能不足后再做 GPU 方案。

### 要嵌入 Linux 原生 bin

优先 PoC：`libcrun`（C/FFI）或 youki `libcontainer`（Rust）。如果仅隔离已安装的应用，不需要 OCI 镜像，先比较 Bubblewrap，避免重造镜像管理系统。

### 要嵌入 macOS 应用

如果必须运行 Linux 环境：比较 `libkrun` 与 Apple Containerization。若只要隔离浏览器会话而不需要 Linux 容器，原生浏览器子进程/profile/browser context 通常更简单，但隔离保证不同。

## 参考架构与落地路径

### 架构一：最快验证容器 GUI 的可行性

```text
本地浏览器
    │ HTTPS + 认证
    ▼
宿主 loopback 端口 / SSH 隧道
    │
    ▼
Webtop 容器
    ├── 桌面或窗口管理器
    ├── Chromium / 其他 GUI 应用
    ├── 显示服务器 / compositor
    └── Selkies 显示、输入与音频通道
```

这条路线适合先证明“能显示、能输入、能运行浏览器”，不代表最终最小镜像方案。Webtop 包含桌面及工具，有冗余，也有容器内提权便利功能；用于面向不可信租户的产品时，需要审计和裁剪。

可以按下面模板启动两个软件渲染会话。`GUI_IMAGE` 应设为实际可用、匹配宿主架构并经验证的 `linuxserver/webtop` 固定 tag 或 digest；模板不代表已经测试的部署配置。

```bash
export GUI_IMAGE='lscr.io/linuxserver/webtop:<已验证的固定tag或digest>'
export GUI_PASSWORD='<替换为强密码>'

docker run -d --name gui-session-1 --init \
  --cpus=2 --memory=2g --pids-limit=512 --shm-size=512m \
  -p 127.0.0.1:3001:3001 \
  -e CUSTOM_USER=viewer -e PASSWORD="$GUI_PASSWORD" \
  --mount type=volume,source=gui-session-1-config,target=/config \
  "$GUI_IMAGE"

docker run -d --name gui-session-2 --init \
  --cpus=2 --memory=2g --pids-limit=512 --shm-size=512m \
  -p 127.0.0.1:3002:3001 \
  -e CUSTOM_USER=viewer -e PASSWORD="$GUI_PASSWORD" \
  --mount type=volume,source=gui-session-2-config,target=/config \
  "$GUI_IMAGE"
```

入口分别为 `https://localhost:3001`、`https://localhost:3002`，镜像默认自签名证书会触发浏览器证书提示。这里的 Basic Auth 仅适合可信本地测试；生产应使用受信证书、统一认证和会话授权。环境变量中的密码可被有 Docker 管理权限的人查看；真正的凭据管理应按产品的 secret 注入机制实现。

上述资源上限是 PoC 起点，不是性能保证，也没有要求 `--privileged`、host IPC 或 GPU。某个镜像/浏览器是否需要额外 namespace 权限，应结合宿主内核、镜像版本和错误日志验证，而不是直接全面放宽权限。

### 架构二：产品化的多实例浏览器 UI

```text
应用 bin / 控制面
    ├── 镜像版本与资源准备
    ├── 创建、停止和回收会话
    ├── 每会话认证令牌与端口/路由分配
    └── 状态监控与资源限制
          │
          ▼
会话运行环境（容器或 microVM）
    ├── Chromium：普通用户 + browser sandbox
    ├── 独立 profile / 下载目录 / 临时目录
    ├── 独立显示服务 + 远程显示入口
    └── 仅内网/loopback 可达的自动化接口
          │
          ▼
认证网关 → WebView/noVNC 客户端 + 人工接管
```

先用 Docker 实现控制面，可以把“会话生命周期”和“具体容器运行时”分离。之后再用 libcrun/libcontainer/libkrun 替换后端，避免同时调试浏览器、显示协议、镜像解包和底层隔离。

### 嵌入运行时后，仍需自己承担的模块

1. **镜像准备**：获取 OCI manifest、选择架构、校验 digest、下载层、解压并正确处理 whiteout，构造 rootfs。
2. **隔离配置**：UID/GID、namespace、挂载、只读路径、网络、seccomp、capabilities、cgroup 或 VM 资源限制。
3. **浏览器与 GUI**：进程启动、sandbox、显示服务、编码、输入、字体和 profile 管理。
4. **生命周期**：init/reaping、健康检查、超时、异常退出、临时目录/端口回收和崩溃恢复。
5. **分发升级**：运行时库、浏览器、rootfs、VM 内核的版本锁定、安全更新与签名。
6. **管理面安全**：控制接口授权、日志脱敏、CDP 不对外开放、持久化数据权限与删除策略。

“可链接的 OCI runtime”通常只解决其中部分执行与隔离问题，不会自动提供 Docker Engine 的全部镜像、网络和存储管理能力。

## 并发容量预算与测量口径

### 磁盘预算

```text
实际磁盘 ≈ 唯一镜像层 + 各会话可写层 + profile/下载/日志卷 + 平台 VM 开销
```

相同镜像的 10 个容器不是 10 倍基础镜像，但 10 个用户的下载、缓存和 profile 会分别增长。采用独立 VM 的运行时是否共享镜像块、写时复制及缓存，要按实现验证，不能直接套用 Docker OverlayFS 的假设。

### 内存预算

```text
宿主预算 ≈ 平台/控制面开销 + Σ(浏览器 + 显示服务 + 编码器 + 实际shm占用) + 余量
```

例如，若试验测得每个会话在目标页面下实际使用 1 GiB，10 个会话就应先按约 10 GiB 会话内存再加平台开销和余量规划。这只是算术示例，不是已测得的 Chrome 用量。microVM 还要计入每个 guest 内核和 guest 内存管理开销。

shm 属于内存预算的一部分，不能误以为设置 `--shm-size=1g` 后它既不计入内存限制，也不能误以为启动时就固定耗尽 1 GiB。

### 基准场景

| 维度 | 固定/变化项 | 建议记录 |
|---|---|---|
| 版本 | 镜像 digest、架构、浏览器版本、运行时、宿主内核 | 可重复性 |
| 并发 | 1、5、10 个独立会话 | 稳态与峰值资源、OOM/崩溃次数 |
| 页面 | 空白页、一般业务页、复杂 SPA、视频/WebGL 页面 | 每种工作负载的资源差异 |
| 显示 | 固定分辨率与帧率，再比较 720p/1080p | 带宽、编码 CPU、输入延迟 |
| 启动 | 冷镜像下载、已缓存镜像、profile 已存在 | 下载时间、进程启动时间、可交互首屏时间 |
| 功能 | 中文输入、剪贴板、文件上传/下载、音频 | 可用性与宿主授权需求 |
| 隔离 | 跨会话目录、网络、CDP 与 UI 访问尝试 | 不应发生的跨租户访问 |

别把 Docker 的 container memory usage、进程 RSS 与整个宿主/VM 使用量当成同一个指标；应至少记录容器统计和宿主总量，必要时用 PSS 分析共享内存，避免重复计数。

## 安全与部署检查清单

- GUI、VNC、CDP 与控制 API 分别设置授权，不以“端口难猜”代替访问控制。
- 浏览器普通用户运行，确认 sandbox 实际启用，而不只看启动参数。
- 不挂载宿主 Docker socket、家目录、SSH key 或不必要的显示/DBus socket。
- 默认不使用 privileged、host 网络/IPC 或宽泛设备映射。
- 容器网络默认可能访问互联网、宿主服务和局域网；若允许任意 URL，需评估 SSRF、云 metadata 与内网探测，并按需做出口限制。
- 为镜像与浏览器建立更新机制；固定 digest 是可重复性措施，不是永久不升级的理由。
- 限制 CPU、内存、PID、会话时长、磁盘和下载大小；进程退出后回收环境。
- 剪贴板、文件传输、麦克风/摄像头默认按最小权限开启；多租户不要共享持久化卷。
- 对 adversarial 多租户，评估独立内核 microVM，而不是把普通 Docker 当成与 VM 完全等价的安全边界。

## 尚需验证的项目

1. 目标是 Linux 服务器、macOS 桌面还是跨平台？bin 的语言是 Rust/Go/C++/Swift？
2. UI 是 Chrome 一个窗口，还是允许启动任意桌面应用？
3. 用户/网页是否不可信？需要进程隔离还是独立内核边界？
4. 对固定架构与 digest 记录压缩下载量、解压体积和实际磁盘增量。
5. 对 1/5/10 个会话测 CPU、RSS、shm、首屏时间、输入延迟和带宽；先固定同样网页、分辨率及标签数。
6. 验证中文字体、输入、剪贴板、下载、WebGL、音频和人工接管，不只验证能启动。

本笔记为文档调研，未启动容器或执行性能基准；量级预算与官方已确认能力已分开标注。

## 证据与来源

- [jlesage docker-baseimage-gui](https://github.com/jlesage/docker-baseimage-gui)：轻量 GUI 基础镜像、Web/VNC 端口、认证、硬件设备接入。
- [LinuxServer Webtop](https://github.com/linuxserver/docker-webtop)：桌面变体、Selkies、HTTPS、shm 和 GPU 配置；默认认证需额外配置，不能裸露互联网。
- [noVNC](https://github.com/novnc/noVNC)：浏览器 VNC 客户端/JavaScript 集成。
- [Playwright Docker 文档](https://playwright.dev/docs/docker)：root、浏览器 sandbox、seccomp、IPC 与发行版限制。
- [Docker Rootless mode](https://docs.docker.com/engine/security/rootless/)：无 root 运行的宿主前提。
- [Docker OverlayFS storage driver](https://docs.docker.com/engine/storage/drivers/overlayfs-driver/)：共享镜像层与容器可写层。
- [crun](https://github.com/containers/crun)：可作为库集成，构建 libcrun 需启用 shared libraries。
- [youki](https://github.com/youki-dev/youki) 与 [libcontainer](https://github.com/youki-dev/youki/tree/main/crates/libcontainer)：Rust OCI runtime 与库组件。
- [Bubblewrap](https://github.com/containers/bubblewrap)：进程沙箱机制及安全边界取决于配置。
- [libkrun](https://github.com/containers/libkrun)：动态库、C API、Linux KVM/macOS ARM64 HVF、virtio 设备和目标限制。
- [Apple container](https://github.com/apple/container) 与 [Containerization](https://github.com/apple/containerization)：轻量 VM 容器与 Swift 集成库、平台要求。
- [Podman](https://podman.io/)：daemonless 容器工具，macOS 场景仍需 Linux 虚拟机。
- [Firecracker](https://github.com/firecracker-microvm/firecracker) 与 [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor)：Linux/KVM microVM 路线，作为进一步选型资料；本次未做 GUI 原型验证。

## 分类与标签

- 分类：技术 / 容器与沙箱
- 标签：#Docker #GUI #Chromium #noVNC #容器 #沙箱 #可嵌入运行时
