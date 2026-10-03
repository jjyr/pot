---
{ tags: [ Architecture ] }
---
# Apple container：最新能力、架构与使用调研

整理日期：2026-10-02  
版本基线：`apple/container 1.5.0`；对应底层依赖 `apple/containerization 0.47.0`。  
研究范围：官方发布记录、固定 tag 文档与关键源码；未安装软件、未运行容器、未执行性能基准。

相关笔记：[Docker容器GUI与可嵌入运行时调研](docker-gui.md)。

## 0. 核心结论

1. **Apple container 是 Apple 开源的 Linux 容器工具，不是运行 macOS 应用的容器。** 面向 Apple silicon Mac，官方支持平台为 macOS 26。
2. **它不是“没有 VM 的 Docker”。** 普通 `container run` 的核心模型是每个容器拥有一个独立轻量 Linux VM，而不是让所有容器共用一台大型 Linux VM。
3. **镜像生态兼容，控制面不等价。** 可以使用 OCI 镜像、Registry 和 Dockerfile，但不能因此认为兼容 Docker Engine API、Docker socket、Compose 或所有 Docker CLI 参数。
4. **最新稳定版为 1.5.0，发布于 2026-09-29。** 已具备构建、镜像分发、自定义网络、端口转发、卷、资源统计、持久 Linux machine 和实验性本地 Kubernetes 能力，不应继续用 2025 年初版功能表评价它。
5. **底层 Containerization 是可嵌入的 Swift 库。** 如果目标是把 Linux 沙箱能力集成进 Mac 应用，应该重点研究该库，而不只是把 `container` CLI 当成可嵌入运行时。
6. **GUI/Chromium 可以作为 Linux 工作负载实现，但不是内置桌面功能。** 仍需 Linux 图形栈、显示传输和浏览器沙箱；GPU 直通及原生 macOS 窗口不能假定开箱即用。
7. **独立 VM 增加内核隔离边界，但宿主管理面仍是攻击面。** 镜像解包、Registry 认证、文件共享、SSH agent 和 kubeconfig 管理都需要安全审计；近期版本有多项相关修复。

---

## 1. 最新版本与项目状态

### 1.1 截至研究日期的版本快照

| 项目 | 核实结果 | 含义 |
|---|---|---|
| 最新稳定 CLI | `1.5.0` | GitHub releases/latest 返回的非预发布版本 |
| 发布时间 | `2026-09-29T02:33:21Z`，北京时间 09-29 10:33 | 与本笔记的 2026-10-02 查询日期区分 |
| CLI 内嵌 Containerization 版本 | `0.47.0` | 来自 `1.5.0/Package.swift` 的 `scVersion`；不是声称底层库的独立最新 release |
| builder shim 版本 | `0.13.1` | 构建工具链的另一项依赖 |
| 签名安装包 | `container-1.5.0-installer-signed.pkg` | 推荐使用官方签名包 |
| 签名安装包大小 | `118,045,087 bytes`，约 `112.6 MiB` | 只是安装包下载量，不包含工作负载镜像、首次内核准备和运行数据 |
| 官方支持运行平台 | Apple silicon + macOS 26 | 不支持 Intel Mac 作为标准运行平台 |
| 从源码构建底层库 | Apple silicon、macOS 26、Xcode 26 | 下载签名 CLI 不等于必须安装完整 Xcode |

来源：[1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0)、[固定版本 Package.swift](https://github.com/apple/container/blob/1.5.0/Package.swift)、[固定版本 README](https://github.com/apple/container/blob/1.5.0/README.md)。

### 1.2 最近几个版本的重要变化

| 版本 | 发布日期（UTC） | 与选型相关的变化 |
|---|---|---|
| `1.2.0 / 1.2.1` | 2026-07-29 / 08-07 | 本地 `k8s` 插件、运行中容器 export、构建 SSH forwarding、masked/read-only paths；1.2.0 有多项安全修复 |
| `1.2.2` | 2026-08-08 | 修复 release 安装包中 Kubernetes 插件无法工作的情况 |
| `1.3.0` | 2026-08-24 | 默认 Kata kernel 更新为 `3.32.0-debug`；Registry scheme 默认 HTTPS，移除 `auto` |
| `1.3.1` | 2026-08-29 | 修复 ID、digest、镜像加载/解包、Registry 认证等路径上的安全问题 |
| `1.4.1` | 2026-09-09 | 增加 `container clean`、增强 system status；修复 OCI layout 符号链接及 Unix socket buffer 相关问题。官方说明未发布 1.4.0 |
| `1.5.0` | 2026-09-29 | 修复空 entrypoint、localhost DNS 变更后出口网络中断；K8s 增加自定义 CNI，移除 `k8s start`，修复 guest kubeconfig 合并安全问题 |

**最新版本不能只理解为“命令更多了”。** 对任意镜像或不可信程序的执行产品，近期宿主侧解析和解包修复意味着不能长期停留在旧版本。

### 1.3 稳定性承诺

固定版本 README 的项目状态说明：

- `container` release 是产品版本，不是严格 semantic versioning。
- 同一大版本内 CLI 一般维持兼容，但并非绝对保证。
- 标为 experimental 的功能，例如 `k8s`，不保证兼容性。
- `container-apiserver` XPC API 在同一大版本内承诺前后兼容。
- 非公开 XPC helpers 不承诺跨版本兼容。
- 应用数据只保证同一大版本内向前兼容；不能因为能升级就认为能无风险降级。
- Containerization 库的源码稳定性承诺更窄：只在同一 minor 内保证，应锁定依赖并评估每次升级。

**工程建议：CLI、库、guest init、内核、builder 和工作负载镜像分别记录版本，不能仅固定一个 `container --version`。**

---

## 2. 需要区分的四层

| 名称 | 定位 | 对开发者的意义 |
|---|---|---|
| `apple/container` | 用户工具、CLI、API server、镜像/网络/运行时 helpers | 日常开发和产品控制面参考实现 |
| `apple/containerization` | Swift 容器、镜像、文件系统与 VM 管理库 | 真正需要“嵌入应用/bin”时的主要对象 |
| `Virtualization.framework` | macOS VM 与虚拟设备框架 | 负责 Linux VM 的执行与设备模型，不是 Docker 引擎 |
| `vminitd` / `vmexec` | Linux guest 内的 init、管理和执行机制 | 接收宿主请求并管理 Linux 进程 |

OCI 镜像定义应用用户空间；Linux kernel 和 guest init 由运行时额外提供。拉一个 Ubuntu 镜像不是再下载完整 Ubuntu VM，也不意味着使用镜像发行版自己的内核。

### 2.1 相比 Docker Desktop 的典型模型

```text
Docker Desktop 常见模型
macOS
  └── Linux VM
        └── Docker Engine / container runtime
              ├── container A ─┐
              ├── container B  ├── 共用这个 VM 的 Linux kernel
              └── container C ─┘

Apple container 普通运行模型
macOS
  ├── container control plane
  ├── lightweight VM A → kernel A + init + workload A
  ├── lightweight VM B → kernel B + init + workload B
  └── lightweight VM C → kernel C + init + workload C
```

这里是普通应用容器的对比。后文的 `container machine`、Kubernetes 节点是另一类工作负载：节点 VM 内部可能管理多个进程/Pod，不能机械推导“每个 Pod 都是单独的 Apple VM”。

### 2.2 直接影响

- **隔离**：其他普通容器不与当前容器共享一个 guest kernel。
- **资源**：每实例都增加 guest kernel/init 等开销；高密度并发需测量，不能只计算应用内存。
- **网络**：每容器有独立 IP，宿主可以直接访问；端口映射是方便访问的一种方式，而不是唯一方式。
- **文件共享**：可只给当前 VM 共享它需要的目录。
- **内核配置**：可按容器指定 kernel，适合内核特性或版本测试。
- **启动**：官方库设计说明宣称亚秒级启动，指优化环境下的启动设计目标/表现，不包含下载镜像、冷文件系统准备和应用首屏；本次没有复测。

---

## 3. 架构详解

### 3.1 宿主控制面

```text
container CLI / 客户端
         │ XPC
         ▼
container-apiserver（launchd 用户 LaunchAgent）
         ├── container-core-images
         │      └── Registry client + 本地 image/content store
         ├── container-network-vmnet
         │      └── vmnet 网络、IP 分配与网络管理
         └── container-runtime-linux（每容器一个 runtime helper）
                └── Containerization
                      └── Virtualization.framework
                            └── 该容器的 Linux VM
```

`container system start` 启动 API server 和相关后台服务，`container system stop` 停止服务并从 launchd 注销。

**它没有要求 Docker daemon，但并不是没有后台服务。** `container-apiserver`、helpers 和 VM 都是产品运行的一部分。

关键 macOS 集成：

| 技术 | 作用 |
|---|---|
| Virtualization.framework | VM、CPU/内存与虚拟设备 |
| vmnet | 虚拟网络 |
| XPC | CLI/API server/helper 间通信 |
| launchd | 服务生命周期 |
| Keychain | Registry 登录凭据 |
| Unified Logging | 系统日志 |

来源：[technical overview](https://github.com/apple/container/blob/1.5.0/docs/technical-overview.md)。

### 3.2 guest 内部：vminitd 与工作负载不是同一个 PID 1

```text
宿主 runtime helper
       │ gRPC over vsock
       ▼
guest kernel
       └── vminitd（VM 的初始进程）
              ├── 挂载/网络/运行环境管理
              ├── 创建与启动 containerized process
              └── I/O、signal、exit event 管理
                    └── 应用容器进程
```

vsock 是宿主与 guest 的管理通道，不需要把 guest 管理服务暴露成普通公网 TCP 端口。

要区分：

- VM 初始进程是 `vminitd`。
- 应用容器自己的进程空间中，应用可能是 PID 1。
- CLI 的 `--init` 用于应用层信号转发和孤儿进程回收，不等于“没有 --init 就没有 VM init”。

来源：[Containerization 0.47.0 README](https://github.com/apple/containerization/blob/0.47.0/README.md)、[runtime configuration](https://github.com/apple/container/blob/1.5.0/docs/runtime-configuration.md)。

### 3.3 镜像与 rootfs 路径

```text
OCI Registry
    → 选择 linux/arm64 或 linux/amd64 manifest
    → 下载 config 与 layers、存入内容存储
    → 构造 Linux ext4 rootfs
    → 准备 guest kernel + init filesystem
    → 挂载块设备、virtiofs、网络设备
    → 启动 VM → vminitd → 应用进程
```

Containerization 提供 OCI 管理、Registry、归档/解包、ext4 构建、Linux process 和 VM API。它不只是 Virtualization.framework 的薄包装。

镜像层下载可使用内容缓存，但**不能直接套用 Docker OverlayFS 的“所有运行容器共享相同 lowerdir”来估算 Apple 的实际磁盘占用**。CLI 每个容器可写文件系统使用 macOS sparse disk image；命名卷也有自己的 sparse image，实际共享/复制效果应结合磁盘统计测试。

### 3.4 构建架构

`container build` 使用独立 builder 工作负载，基于 BuildKit，经 builder shim 管理：

```text
Dockerfile / Containerfile + build context
         → container build
         → BuildKit builder VM
         → OCI image → 本地存储 / Registry
```

支持多阶段构建、build args、target、secret、SSH agent forwarding、多架构与 OCI/tar/local 输出。builder 是另一台要计入 CPU、内存和缓存的 VM。

### 3.5 底层库的 Linux backend：新能力但不是 CLI 平台承诺

Containerization `0.47.0` 文档列出两类 VM backend：

- macOS：`VZVirtualMachineManager`，使用 Virtualization.framework。
- Linux：`CHVirtualMachineManager`，使用 Cloud Hypervisor + KVM；需要 `cloud-hypervisor`、按需 `virtiofsd`、`/dev/kvm` 权限和网络准备。

Linux backend 使用 TAP/bridge/NAT 时，调用方负责相应宿主网络配置；集成测试目前只覆盖跨平台子集。

**结论：底层库已有 Linux 路线，不等于 `container 1.5.0` CLI 已变成与 macOS 一样受支持的跨平台 Docker 替代品。** 产品若要跨平台，需单独评估 API、依赖分发、网络和 Linux 测试覆盖。

---

## 4. 当前能力边界

| 能力 | 1.5.0 状态 / 注意事项 |
|---|---|
| OCI pull/push/tag/list/inspect | 已支持，默认 Registry 为 docker.io |
| Dockerfile / Containerfile build | 已支持，BuildKit 路线 |
| ARM64 Linux 工作负载 | 标准优先路径 |
| AMD64 用户空间 | Rosetta 支持，需验证目标应用兼容性 |
| create/run/start/stop/exec/logs/cp/export | 已支持；命令参数以固定 tag 文档为准 |
| 多容器并行 | 已支持，每普通容器独立 VM |
| CPU、内存、ulimit、shm | 已支持 |
| bind mounts / named volumes / tmpfs | 已支持，数据与生命周期规则不同于 Docker 的部分默认行为 |
| 自定义网络、IPv4/IPv6、端口转发 | 已支持，官方平台基线为 macOS 26 |
| 宿主与容器 DNS 集成 | 已支持，需要两侧配置；自定义网络裸 hostname 发现仍有缺口 |
| SSH agent / Unix socket 集成 | 已支持相关参数，但显式共享扩大授权面 |
| 只读 rootfs、cap-add/drop | 已支持 |
| masked/read-only paths | 已有，标为 experimental |
| 自定义 kernel/init filesystem | 已支持 |
| 嵌套虚拟化 | 有条件支持，M3+ 和支持 KVM 的 guest kernel |
| Linux machine | 已支持，面向持久开发环境 |
| 本地 Kubernetes | 实验性插件，1.5.0 已支持自定义 CNI |
| 原生 macOS GUI、macOS container | 不是目标能力 |
| Linux GUI 远程显示 | 可搭建工作负载，非内置桌面特性 |
| Linux guest 自动获得 Metal/GPU | 本次未发现标准 CLI 提供可直接依赖的 GPU 直通流程，不应假定支持 |
| Docker Engine REST API / Docker socket | 不是其公开控制模型，不应按兼容端点使用 |
| 内置 Docker Compose | 固定版本命令参考中无该能力；第三方适配应单独验证 |

“OCI 兼容”主要指镜像与分发生态，不代表每个依赖特定内核、设备、权限和 Docker 管理 API 的镜像都无需修改即可运行。

---

## 5. 安装、升级和第一批命令

### 5.1 官方安装路径

1. 在 [1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0) 下载签名 `.pkg`。
2. 双击安装；安装器向 `/usr/local` 放置文件时需要管理员授权。
3. 启动系统服务，按提示准备默认 Linux kernel。

```bash
container --version
container system start
container system version
container system status
container run --rm alpine:3.22 echo hello
```

日常操作的控制服务是用户 LaunchAgent，不代表每次 run 都要 sudo；但安装、某些 DNS/网络集成、受保护文件访问和自定义打包仍有权限要求。

离线或受限网络环境要提前准备内核、vminit、builder 镜像和工作负载镜像，不能只复制 CLI binary。

### 5.2 基本生命周期

```bash
container run -it --rm alpine:3.22 sh
container run -d --name research-web --cpus 2 --memory 1g \
  -p 127.0.0.1:8080:80 nginx:stable-alpine

container ls
container ls --all
container inspect research-web
container exec research-web nginx -v
container logs research-web
container logs --boot research-web
container stats --no-stream research-web

container stop research-web
container start research-web
```

`http://127.0.0.1:8080` 为宿主访问入口。示例镜像 tag 为教学用途，产品部署应选择验证过的架构和 digest。

删除会话及其可写层属于数据破坏操作，确认不再需要后才执行：

```bash
container stop research-web
container rm research-web
```

### 5.3 升级和降级

官方升级流程：

```bash
container system stop
/usr/local/bin/update-container.sh
container system start
container system version
```

也可以手动安装指定版本的签名包。降级时先备份，不能把保留用户数据理解为旧版本一定能读取新版本数据。

官方 uninstall 脚本：

- `uninstall-container.sh -k`：卸载工具、保留用户数据。
- `uninstall-container.sh -d`：连同用户数据删除；具有破坏性，不应作为普通排障第一步。

本次研究未执行安装、升级或卸载。

---

## 6. 构建、镜像发布与迁移

### 6.1 最小 Dockerfile 示例

```dockerfile
FROM python:3.13-alpine
WORKDIR /app
COPY . /app
EXPOSE 8000
CMD ["python3", "-m", "http.server", "8000", "--bind", "0.0.0.0"]
```

在独立测试目录放置该 Dockerfile 与测试文件，再运行：

```bash
container build -t local/research-web:demo .
container run -d --name research-built-web \
  -p 127.0.0.1:8081:8000 local/research-web:demo
```

构建上下文不要直接指向整个家目录；使用 `.dockerignore` 控制敏感数据和无关文件。

### 6.2 builder 资源

默认 builder 为 2 CPUs / 2 GiB RAM，普通容器为 4 vCPUs / 1 GiB RAM。

```bash
container builder start --cpus 4 --memory 8g
container builder status
container build -t local/research-web:demo .
```

如果 builder 已启动，修改资源需按官方文档 stop/delete/start；删除 builder 会清理其状态/缓存，应评估代价。

### 6.3 多架构

```bash
container build --arch arm64 --arch amd64 \
  -t registry.example.com/team/research-web:demo .

container run --arch arm64 --rm alpine:3.22 uname -m
container run --arch amd64 --rm alpine:3.22 uname -m
```

amd64 用户空间经 Rosetta 翻译，**不是启动一台真正 x86 guest kernel 的 VM**。`uname` 输出 x86_64 不能单独证明底层内核架构或所有 x86 系统行为都完全等价。

浏览器沙箱、JIT、特定 CPU 指令、低层调试、内核模块及性能测试都应单独验证；优先 ARM64 原生镜像。

### 6.4 Registry

```bash
container registry login registry.example.com
container image push registry.example.com/team/research-web:demo
container image pull registry.example.com/team/research-web:demo
container image inspect registry.example.com/team/research-web:demo
```

避免把密码写进命令行或镜像构建参数；登录凭据通过宿主 Keychain 集成。默认 HTTPS，不能继续依赖旧文档的 `--scheme auto`。

### 6.5 从 Docker 迁移时检查什么

- 从同一 Registry 拉取 OCI 镜像通常最直接；镜像 tag、CPU 架构、guest kernel 与应用依赖需匹配。
- 重新创建容器配置、网络和持久卷；不要认为两个引擎共用本地镜像缓存或数据目录。
- 把 Dockerfile 迁移和 Compose 编排迁移分开评估。
- 宿主 Docker socket、`--privileged`、`--device`、`--security-opt`、host IPC/network 等 Docker 参数不能机械替换为 `container`。
- image save/load 与容器文件系统 export 是不同用途；export 不等同于含完整 OCI config/history 的镜像迁移，更不应当作所有外部卷的备份。

---

## 7. 网络、DNS 与宿主互通

### 7.1 基本网络模型

- `system start` 创建 `default` vmnet 网络。
- 每个容器有自己的 IP，宿主和同网络容器可以访问它。
- 通过 `container inspect` 查看 IP；IP 可能变化，脚本不应硬编码示例地址。
- 如果容器连接多个网络，已发布端口转发至第一个网络接口的 IP。

**重要：loopback publish 只约束该端口转发监听地址，不是整个网络的访问控制。** 同网络容器仍可能直连目标 VM IP。桌面/浏览器服务本身要认证，不能只靠 `-p 127.0.0.1:...` 当跨租户安全边界。

### 7.2 宿主通过名字访问容器：需要两步

在 `~/.config/container/config.toml` 的现有配置中合并：

```toml
[dns]
domain = "test"
```

然后：

```bash
container system stop
container system start
sudo container system dns create test
```

两步分别设置容器系统 DNS domain、为 macOS 写入 `/etc/resolver/` 规则。只做其中一步不等于宿主和容器都能正常解析。

例如容器 `research-web` 可通过 `research-web.test` 访问。官方文档指出，自定义网络上仅使用裸 hostname 的 Compose 式自动发现尚有缺口，必要时使用 inspect 获取 IP；不要笼统宣称服务发现已完全等价 Docker。

### 7.3 自定义网络

```bash
container network create research-net
container network list
container run -d --name research-net-web --network research-net \
  nginx:stable-alpine
container inspect research-net-web
```

网络之间隔离；同一个网络内部不是每容器独立隔离。默认网络与自定义网络的出口、宿主访问和路由关系仍需按安全目标验证。

### 7.4 容器访问宿主 loopback 服务

官方支持创建专门域名：

```bash
sudo container system dns create host.container.internal \
  --localhost 203.0.113.113
```

之后可在容器内访问 `http://host.container.internal:<port>`。地址应避免与环境中的网络冲突。

官方明确列出的副作用：

- 创建 localhost domain 会禁用 Private Relay。
- 相关 packet filter 规则在系统重启后会被移除，需要重新验证配置。

这不是无需配置就存在的 Docker `host.docker.internal`。1.5.0 修复了 localhost DNS 变更后的出口网络问题，但没有因此取消这些已记录的限制。

---

## 8. 存储、持久化与磁盘回收

### 8.1 三种存储

| 模式 | 使用场景 | 特性 |
|---|---|---|
| 宿主 bind mount / virtiofs | Mac 编辑、Linux 编译；文件交互 | 直接暴露授权目录；大量小文件 I/O 要测试 |
| named volume | 数据库、浏览器 profile、不需要宿主直接读写的数据 | guest ext4 块文件系统，官方推荐作为改善 I/O 的选择 |
| tmpfs | 临时文件、shm、无需持久化的高速数据 | 消耗 guest 内存，停止后数据消失 |

### 8.2 挂载示例

```bash
container run --rm \
  --mount "type=bind,source=$PWD,target=/workspace,readonly" \
  alpine:3.22 ls /workspace

container volume create --opt size=10g research-data
container run --rm \
  --mount type=volume,source=research-data,target=/data \
  alpine:3.22 sh -c 'echo hello > /data/example.txt'

container run --rm \
  --mount type=volume,source=research-data,target=/data \
  alpine:3.22 cat /data/example.txt
```

共享目录尽量只读；只读挂载不会阻止程序读取并外传目录中的敏感数据。

### 8.3 两个容易误解的地方

1. named volume 默认虚拟容量为 **512 GiB**，采用 sparse image；不代表创建时占用 512 GiB 物理磁盘。
2. 匿名 volume **不会因为 `--rm` 自动删除**，与 Docker 的部分行为不同；会话程序需记录并回收它们。

不要把 sparse image 的逻辑容量、文件逻辑长度和真实分配磁盘量混为一谈。

### 8.4 回收

```bash
container system df
container clean research-net-web
```

`container clean` 是 1.4.1 加入的功能，面向运行中容器的 rootfs 和 named volume 中未使用空间。guest 内删除文件后，宿主 sparse image 空间并不自动相应缩小；不能只看 Linux `df`。

注意：[resource-usage 文档](https://github.com/apple/container/blob/1.5.0/docs/resource-usage.md) 的回收段落未完整展示新 clean 功能，需结合 release 和 command reference 阅读。

以下命令会删除资源；只在确认停止容器/未引用卷的数据不再需要后执行：

```bash
container prune
container image prune
container volume prune
```

数据库和 profile 应做应用一致性备份。不要把停止/删除运行环境当成已备份持久卷。

---

## 9. CPU、内存、运行开销与观测

### 9.1 资源配置

```bash
container run --rm --cpus 2 --memory 2g \
  --shm-size 512M alpine:3.22 sh -c 'df -h /dev/shm'
container stats --no-stream
container stats --no-stream --format json
```

`--cpus` 表示配置的虚拟 CPU 数，不应直接等同 Docker cgroup 的 CPU quota 语义。macOS 最终调度这些 VM 和宿主程序。

可 overcommit：系统不因单个/总请求超过物理资源而拒绝创建，但 CPU 竞争会降低吞吐，实际 RAM 需求超过物理内存后会产生 swap 和延迟。

### 9.2 内存回收限制

官方技术概述说明：Virtualization.framework 对 ballooning 的支持有限。guest 进程释放到 Linux 的内存页目前不能保证归还给 macOS，因此长期会话或内存密集负载可能需要重启来降低宿主使用量。

`--memory 16g` 并不代表 VM 启动时立即触碰全部 16 GiB；但“guest 已释放内存”也不代表宿主 RSS 会立即下降。应同时观察：

- `container stats` 的容器指标。
- macOS Activity Monitor/宿主整体内存压力。
- runtime helper/VM 进程的内存。
- swap、长会话峰值及重启前后变化。

### 9.3 总预算

```text
磁盘 = 安装资源 + kernel/init/builder + 本地镜像存储
     + 各会话 rootfs/可写 image + volumes + build cache + logs

内存 = 宿主控制面 + builder（若运行）
     + Σ(各 guest kernel/init + 工作负载 + 实际shm/文件缓存)
     + macOS 与其他应用余量
```

安装包 112.6 MiB 不能用来回答“每个浏览器会话有多大”。浏览器镜像下载量、rootfs、profile 和实际 RAM 必须分别测量。

### 9.4 调试分层

| 现象 | 先看什么 |
|---|---|
| CLI 连不上服务 | `container system status`、`container system version` |
| VM 无法启动 | `container logs --boot <name>` |
| 应用退出 | `container logs <name>`、entrypoint、工作目录、架构 |
| 网络/DNS 异常 | inspect networks、system DNS 配置、出口和 localhost 规则 |
| 工作负载慢 | stats、宿主内存压力、Rosetta、磁盘挂载方式 |
| 系统控制面问题 | `container system logs` |

不要先用卸载并清空数据掩盖故障；先记录版本、日志和可复现工作负载。

---

## 10. machine、Kubernetes 与嵌套虚拟化

### 10.1 Container machine：持久 Linux 开发环境

普通容器围绕应用进程；machine 围绕 Linux 环境，会运行镜像的 init system，适合长期服务、systemd 和发行版开发环境。

```bash
container machine create alpine:3.22 --name research-dev \
  --cpus 2 --memory 4g --home-mount none
container machine run -n research-dev whoami
container machine run -n research-dev
container machine ls
container machine inspect research-dev
container machine stop research-dev
```

machine run 在 machine 停止时会先启动它。镜像应具备 `/sbin/init`，不代表任意 minimal 应用镜像都适合。

**安全关键点：machine 默认映射宿主用户与家目录，`--home-mount` 默认 `rw`；默认内存为宿主内存的一半。** 这是开发便利设计，不适合作为不可信程序的默认沙箱。上例显式关闭 home mount 并限制资源。

需要共享源代码时，应评估 `ro` 或只共享专门目录的普通容器方案，避免整个 home 暴露 SSH key、云凭据、浏览器配置等。

### 10.2 本地 Kubernetes：已有，但实验性

```bash
container k8s create --name research-k8s --cpus 4 --memory 8g
container k8s list
kubectl --context research-k8s get nodes
container k8s load-image --name research-k8s local/research-web:demo
```

它会准备 node image、运行 kubeadm、安装 CNI，并合并 `~/.kube/config`。默认 node image 在 1.5.0 文档中为 `kindest/node:v1.35.5`；允许指定其他版本与自定义 CNI manifest。

用途是本地开发与测试，不是声明 Mac 已成为通用生产 Kubernetes 平台。底层普通应用容器的每容器 VM 模型，也不意味着节点内部每个 Pod 都有一个独立 Apple VM。

**1.5.0 重要限制：`container k8s start` 已移除。** 控制平面停止后，官方恢复建议是删除并重新创建集群，这会影响数据和状态，不能把它当成无损 restart。

1.5.0 也修复了把 guest kubeconfig 值合并到宿主 kubeconfig 时的安全问题；实验性并不意味着可以省略更新。

### 10.3 嵌套虚拟化

- 需要 M3 或更新的 Apple silicon。
- 需要 guest kernel 启用相应 KVM 特性；默认 kernel 不能想当然满足要求。
- CLI 使用 `--virtualization` 和自定义 `--kernel`。
- 文档对 framework 能力列出的 macOS 15 下限，不等于项目官方回退支持 macOS 15；本笔记选型基线仍为 macOS 26。

```bash
container run --rm --virtualization \
  --kernel /path/to/kvm-enabled-vmlinux \
  ubuntu:24.04 sh -c 'ls -l /dev/kvm'
```

这是给需要 KVM 的工作负载增加嵌套能力，不是 GUI、普通浏览器或 Kubernetes 的通用前提。

---

## 11. GUI、Chrome 与多会话 UI：结合前一篇调研

### 11.1 能做什么

Linux VM 用户空间中可配置：

```text
Mac 原生应用 / 宿主浏览器 / WebView
          │ HTTPS + 会话认证 + 输入事件
          ▼
该会话 VM 的 noVNC 或其他远程显示服务
          ▼
Linux X11/Wayland 显示环境
          ▼
Chromium / Chrome / 其他 Linux GUI 程序
```

有 display server 不等于有远程 UI。Xvfb 只提供虚拟屏幕，仍需 VNC/WebSocket、显示串流或截图接口。

### 11.2 多实例

普通每实例独立 VM 能提供单独的 kernel、IP、显示、profile 和资源配置。绑定宿主不同 loopback 端口可区分入口，但每会话 profile/volume、认证和网络授权仍需独立。

以下是**工作负载模板，不是已经验证成功的 Apple container Webtop 部署**；需先选择具有 linux/arm64 variant、经验证的 GUI 镜像：

```bash
export GUI_IMAGE='<已验证的linux/arm64 GUI镜像及固定tag或digest>'
container run -d --name research-gui-1 \
  --cpus 2 --memory 2g --shm-size 512M \
  -p 127.0.0.1:5801:5800 "$GUI_IMAGE"
```

镜像内部端口 5800 只是 noVNC 类镜像的示例。Selkies/Webtop 常用 HTTPS 3001，必须按目标镜像调整，并配置认证。两个方案对 WebView、编码、音频、GPU 的要求不一样。

### 11.3 浏览器兼容与 sandbox

- 优先 ARM64 Chromium；不要默认 Linux ARM64 有等同 amd64 的 Google Chrome 官方包。
- 如果必须使用 amd64 Chrome，Rosetta、browser sandbox、JIT、WebGL 与 headed 模式应作为联合兼容性测试，而不是只验证 `uname`。
- 浏览器建议普通用户运行，并验证其实际 sandbox；VM 边界不是关闭 browser sandbox 的理由。
- shm 可通过 CLI `--shm-size` 配置。
- `1.5.0` command reference 未提供 Docker 风格 `--security-opt seccomp=...`；不能原样套用 Playwright 的 Docker 安全参数。
- Containerization `0.47.0` 的 `cctl` 源码特别指出：seccomp 需 OCI runtime 路径配合，默认 vmexec 路径忽略 OCI seccomp。**库支持某种配置结构不代表当前选中的执行路径会实施过滤。**

### 11.4 暂不能承诺的能力

- 没有从标准 CLI 文档确认可供 Linux Chrome 直接使用的 Metal/GPU passthrough 方案。
- Linux UI 不会自动变成 AppKit 原生窗口。
- 摄像头、麦克风、剪贴板、文件传输、DRM 视频、WebGL 等需要显示协议和宿主 UI 配合。
- Swift 库没有替你生成 noVNC 前端、管理人工接管、实现多租户访问控制。

**适合的原型路径：先 software rendering + headed Chromium + noVNC，再测试输入、中文字体、下载和 1/5/10 个会话；不要首阶段同时引入 Rosetta、GPU 和库嵌入。**

---

## 12. 如何嵌入自己的 bin / Mac 应用

### 12.1 三种集成方式

| 路线 | 工作量 | 适合阶段 |
|---|---|---|
| 启动 CLI subprocess，读取 JSON 输出 | 最小，但依赖已安装 CLI/服务 | 快速 PoC、可接受外部安装的产品 |
| 通过公开客户端/XPC 控制已安装系统 | 更多 API/打包耦合，仍依赖服务 | 需要高效服务管理且接受系统部署 |
| 直接使用 Containerization Swift package | 最大，需要自行管理资源与生命周期 | 不希望依赖外部 container 工具、需要定制隔离的 Mac 产品 |

Go/Rust/C++ 产品可先采用 Swift sidecar + 自己定义的本地 IPC；如果用 FFI，需自行设计桥接、并发、异步回调和错误处理。Swift 库不意味着已经提供稳定 C ABI。

### 12.2 库能力与调用顺序

可研究的 API：`ContainerManager`、`LinuxContainer`、`LinuxProcess`、`Kernel`、`VZVirtualMachineManager` 及 OCI/ext4 相关模块。

```text
准备 kernel / initfs / 存储目录
       → 获取并校验 OCI image
       → 选择平台并准备 ext4 rootfs
       → 创建 VM manager / 网络
       → 创建 LinuxContainer，指定进程与资源
       → create → start
       → 连接 I/O、发送 signal、等待退出
       → stop / 删除临时资源 / 保留需要的 volume
```

固定依赖的 Swift Package 声明可以采用：

```swift
.package(
    url: "https://github.com/apple/containerization.git",
    exact: "0.47.0"
)
```

这只是依赖片段，不是完整可编译项目。完整实例应以固定 tag 的 [cctl/RunCommand.swift](https://github.com/apple/containerization/blob/0.47.0/Sources/cctl/RunCommand.swift) 为准，避免复制旧版本示例后假定 API 不变。

### 12.3 签名、权限与分发

官方 Virtualization entitlement 文件包含：

```xml
<key>com.apple.security.virtualization</key>
<true/>
```

该 entitlement 只是 VM 执行的相关要求，不是完整产品权限方案。还需评估代码签名、公证、App Sandbox、文件选择授权、vmnet 使用、网络 helper 部署和目标分发渠道。

不能由此推导“任何 unsigned bin 都能无权限运行”或“已经满足 Mac App Store 审核”。如果需要网络虚拟化和 GUI，必须在真实签名应用中验证。

### 12.4 单文件幻想与真实交付物

“嵌入库”不代表所有东西只占一个 binary：

- Swift 主程序及可能的辅助执行文件。
- Linux kernel。
- vminit/init filesystem。
- 工作负载 OCI 镜像/rootfs。
- 浏览器、字体、动态库与显示协议服务。
- profile/volume、日志、镜像缓存和更新数据。

可以打成 App bundle 或首次下载资源，但仍需要版本锁定、校验、许可合规、缓存目录和更新机制。安装包约 112.6 MiB 不是整个浏览器产品的分发体积。

### 12.5 官方示例也有版本滞后

`0.47.0` 中 `examples/ctr-example/Package.swift` 仍锁定 `0.33.4`；CLI runtime 文档自定义 init 示例使用 `0.34.0`，而实际 CLI 1.5.0 依赖为 `0.47.0`。

因此，官方示例适合理解结构，但不能不加核对就混用：

- 以当前项目 Package.swift/Package.resolved 确认版本。
- kernel/initfs 与实际库/CLI 配对。
- 先跑匹配版本测试，再集成 GUI。
- 不直接调用无兼容保证的私有 helper。

---

## 13. 安全模型：VM 边界之外还需要什么

### 13.1 相比普通共享内核容器的收益

每个普通容器自己的 guest kernel 降低了同一 Linux kernel 作为跨会话共享攻击面的范围；只共享必要目录也能缩小数据暴露。

但“VM 级隔离”不等于没有逃逸、宿主解析漏洞或跨会话数据泄露。安全边界至少分成：

```text
镜像与Registry输入
   → 宿主解析/解包/内容存储
   → 宿主控制面及虚拟设备
   → VM边界
   → guest容器进程权限
   → 浏览器自己的sandbox
   → UI/自动化/网络访问授权
```

### 13.2 不可信工作负载检查表

- 用已包含安全修复的版本；跟踪 container 与 Containerization 两个仓库的 security advisories。
- 默认不用 machine 的整个 home rw mount 执行不可信任务。
- 不透传 SSH agent、不共享 Keychain/凭据文件、不挂载不必要的宿主 Unix socket。
- 仅共享明确授权的目录；只读不代表防止读取和外传。
- UI、CDP 与管理 API 分别鉴权；同网络直连也要考虑。
- 使用普通用户、cap-drop、只读 rootfs/路径等防御，验证实际生效。
- 不把未实施的 seccomp 配置当安全保证。
- 控制出口访问，防止访问局域网、宿主服务、云 metadata 等。
- 限制 CPU、内存、文件描述符、进程数量、会话时长和可增长数据。
- 记录/回收 rootfs、volume、网络、端口和临时数据，避免匿名卷残留。
- Kubernetes 插件会写宿主 kubeconfig；不可信集群环境需审计授权和状态合并。

近期安全修复集中出现于宿主侧镜像、路径、Registry 和配置处理，说明 VM 隔离只是整体安全的一部分。这里不对“已达到恶意多租户生产隔离标准”作未经测试的承诺。

---

## 14. 与其他方案的选择

| 场景 | Apple container 的适配度 | 主要考虑 |
|---|---|---|
| Apple silicon 上运行单个 Linux 服务 | 较高 | 安装路径清晰、OCI 镜像可复用 |
| 少量独立沙箱/agent 执行环境 | 值得重点验证 | 每会话独立 kernel、Swift 集成；宿主管理面与资源回收仍需审计 |
| 重度 Docker Compose/现有 Docker API 产品 | 迁移成本较高 | 引擎与编排控制模型不兼容，不能只换命令名 |
| 本地 Kubernetes 试验 | 可用但需接受实验性 | cluster 生命周期、数据和版本变化风险 |
| 持久 Linux 开发 machine | 较高 | 默认 home 共享是便利也有风险 |
| 大规模浏览器会话 | 需要基准再判断 | 每 VM 内存、memory reclaim、图形传输和密度 |
| 原生 macOS GUI 应用隔离 | 不适合当直接答案 | 运行的是 Linux 工作负载，不是 AppKit 容器 |
| Linux 生产服务器通用容器引擎 | 不能按 CLI 已等价支持来选 | 底层 Linux backend 与用户工具支持需分别评估 |
| Rust/Go 跨平台单 bin | 集成复杂 | Swift/FFI/sidecar、kernel/rootfs、签名和双平台网络 |

Docker Desktop/Podman 等通常更适合复用成熟 Docker 工作流；Apple Containerization 更值得用于明确限定的 Mac 原生隔离产品。没有基准时不能宣称 Apple container 在所有场景都更快、更小或更安全。

---

## 15. 建议的验证顺序

### 阶段 A：基础模型验证

1. 确认目标 Mac 为 Apple silicon/macOS 26，安装固定签名版本。
2. 记录 CLI/server/library/kernel/initfs/builder 版本。
3. 验证 ARM64 hello、网络服务、stop/start、logs、stats。
4. 分别验证宿主端口转发、直连 IP、DNS 和自定义网络。
5. 测试 bind/volume、删除 guest 文件后的 `clean`、匿名 volume 回收。

### 阶段 B：目标浏览器工作负载

1. ARM64 Chromium，先 headless，后 headed + software rendering + noVNC。
2. 确认 browser sandbox 与普通用户运行。
3. 验证中文输入、字体、剪贴板、下载/上传、人工接管。
4. 1/5/10 会话固定相同分辨率、页面、标签数，记录启动、内存、CPU、带宽和输入延迟。
5. 长时间运行并制造内存峰值，测 guest 释放后宿主内存、重启与 clean 效果。
6. 只有确实需要 Google Chrome amd64 时，再测试 Rosetta 组合。

### 阶段 C：产品嵌入

1. 先通过 CLI subprocess 实现会话生命周期，控制面不要与 Docker/Apple 特定参数耦合。
2. 用固定版本 Containerization 做最小独立 Swift 原型。
3. 补齐签名、公证、网络授权、用户目录授权、资源分发与更新。
4. 再引入 WebView、会话鉴权、自动化接口和并发资源池。
5. 执行异常退出、宿主重启、网络切换、版本升级和数据备份恢复测试。

### 目前仍没有实测答案的问题

- 目标 Chromium 镜像在该版本下的 headed/sandbox 兼容性。
- 具体 Mac 上每会话实际 RAM、长会话回收和最大可承受并发。
- 目标 WebView/noVNC/Selkies 的音频、视频、编码与输入能力。
- GPU 所需的准确设备通路与可用程度。
- 目标 App Sandbox/分发渠道允许的网络和虚拟化部署方式。

这些项目应作为 PoC 验收条件，而不是在文档中预先写成“已支持”。

---

## 16. 官方来源与阅读路线

阅读顺序建议：README → Technical overview → Networking/Volumes/Resources → Command reference → Containerization API 与 cctl 源码 → Release/Security。

| 来源 | 用途 |
|---|---|
| [container 1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0) | 最新 release、breaking changes、安装包与安全修复 |
| [GitHub latest release API](https://api.github.com/repos/apple/container/releases/latest) | 本次 latest 版本快照；该 URL 会随以后发布改变 |
| [固定版本 README](https://github.com/apple/container/blob/1.5.0/README.md) | 平台、安装、升级、兼容承诺 |
| [Technical overview](https://github.com/apple/container/blob/1.5.0/docs/technical-overview.md) | 控制面、每容器 VM、内存限制；部分历史段落有滞后 |
| [Command reference](https://github.com/apple/container/blob/1.5.0/docs/command-reference.md) | run/build/machine/k8s/clean 的实际参数 |
| [Networking](https://github.com/apple/container/blob/1.5.0/docs/networking.md) | IP、DNS 两步配置、自定义网络、服务发现缺口 |
| [Host integration](https://github.com/apple/container/blob/1.5.0/docs/host-integration.md) | SSH agent、宿主 loopback、Private Relay/重启限制 |
| [Volumes](https://github.com/apple/container/blob/1.5.0/docs/volumes.md) | sparse ext4、默认容量、匿名卷生命周期 |
| [Resource usage](https://github.com/apple/container/blob/1.5.0/docs/resource-usage.md) | 默认 CPU/RAM、overcommit、stats 和磁盘 |
| [Runtime configuration](https://github.com/apple/container/blob/1.5.0/docs/runtime-configuration.md) | capabilities、init、路径保护和 nested virtualization |
| [Multiplatform images](https://github.com/apple/container/blob/1.5.0/docs/multiplatform-images.md) | ARM64/AMD64 构建及 Rosetta |
| [Container machine](https://github.com/apple/container/blob/1.5.0/docs/container-machine.md) | persistent Linux 环境、home mount、资源默认 |
| [Kubernetes](https://github.com/apple/container/blob/1.5.0/docs/kubernetes.md) | 本地 cluster、load-image、CNI 和恢复限制 |
| [Config reference](https://github.com/apple/container/blob/1.5.0/docs/container-system-config.md) | config.toml 与 kernel 配置 |
| [Config 源码](https://github.com/apple/container/blob/1.5.0/Sources/ContainerPersistence/ContainerSystemConfig.swift) | 核对文档默认 kernel 等具体值 |
| [container Package.swift](https://github.com/apple/container/blob/1.5.0/Package.swift) | Containerization 0.47.0 与 builder shim 0.13.1 |
| [Containerization 0.47.0 README](https://github.com/apple/containerization/blob/0.47.0/README.md) | 低层库、VM backend、vsock、构建要求 |
| [Containerization API docs](https://apple.github.io/containerization/documentation/) | API 阅读；在线文档可能随新版本改变 |
| [cctl RunCommand](https://github.com/apple/containerization/blob/0.47.0/Sources/cctl/RunCommand.swift) | API 生命周期、guest overhead、seccomp 执行路径 |
| [Virtualization entitlement](https://github.com/apple/containerization/blob/0.47.0/signing/vz.entitlements) | VM 签名 entitlement 示例 |
| [container releases](https://github.com/apple/container/releases) | 1.2–1.5 变化与安全修复历史 |
| [container security advisories](https://github.com/apple/container/security/advisories) | 宿主控制面安全更新 |
| [Containerization security advisories](https://github.com/apple/containerization/security/advisories) | 解包、Registry、内容存储等库级安全更新 |

### 资料冲突的处理原则

- 固定版本 README/源码和 release 优先于历史概述文字。
- technical overview 仍包含早期 macOS 15 与 initial release 描述；不据此覆盖 1.5.0 README 的 macOS 26 官方支持承诺。
- 文档里的 kernel/程序输出可能只是历史示例，不作为当前实际 kernel 版本。
- 当前源码默认 Kata archive 为 3.32.0，kernel binary path 为 `vmlinux-6.18.35-197-debug`；用户可自定义，实际运行值还需检查本机配置。
- 不把库新增能力自动等同于 CLI 已暴露、默认实施或完整受支持的能力。

## 分类与标签

- 分类：技术 / 容器与沙箱
- 标签：#AppleContainer #Containerization #macOS #AppleSilicon #Swift #虚拟化 #OCI #沙箱 #Chromium
