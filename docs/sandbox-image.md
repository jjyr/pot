# Sandbox 镜像

Sandbox 从 App 内置的 Linux ARM64 OCI 成品镜像启动。桌面、字体、Chromium、noVNC、Pi、AGY 和 Pot Runtime 都在构建时装入；启动不执行 `apk add` / `npm ci`，不拉取 Alpine 基础镜像。内核与 vminit 也随 App 提供。

## 构建与启动

运行入口见[开发指南](development.md)。`scripts/pot bundle` 调用 `scripts/build-image`：

1. 在独立构建 VM 中，以锁定的 Alpine digest 为基础，按 `image/packages.lock` 安装系统包，按 npm lock 安装 Pi。
2. 写入 `/opt/pot` 和 `/opt/pi`，导出 rootfs 并生成 OCI 镜像及 `sandbox-image.json`。
3. 将镜像、vminit 和内核打入 App。构建输入 hash 用作本地镜像版本；相同输入复用缓存，APK/npm 下载另有缓存。
4. Helper 首次导入本地镜像，创建 VM 并运行 `/opt/pot/desktop.sh`。首次导入/解包仍有成本，但不依赖软件源或 npm 在线可用。

构建需要网络和 Apple Virtualization 权限；镜像启动无需网络，模型调用和网页访问另需联网。构建 VM 不挂载用户 workspace 或凭据。用户配置、聊天数据库、浏览器 profile、工作文件单独挂在 `/data`，升级镜像不打包或清空这些数据。运行中临时安装到 rootfs 的软件不会进入下次新建的 VM，长期依赖应写入镜像。

产物在 `.local/d1/resources/`，缓存和构建证据在 `.local/d1/image-build/`，不提交 Git。镜像更新后需重新打包并重启 App。

## 桌面与字体

使用精简 Xfce：`xfwm4`、`xfsettingsd`、`xfdesktop`、Thunar、Xfce Terminal；无面板、登录管理器、屏保或合成器。暖白 Pot 背景，adw-gtk3 控件，Adwaita 图标，右键菜单提供 Browser / Files / Terminal。主题默认值按版本迁移，不在每次启动覆盖用户字体和应用偏好。

UTF-8 locale 配合 Noto Sans、Noto CJK 的 SC/TC/JP/KR 字形与 emoji fallback，覆盖简体中文、繁体中文、日文和韩文。字体显示支持不等于输入法支持；尚未预装 CJK IME，也不承诺覆盖所有 Unicode 字符。

## 大小对比（2026-10-04，ARM64）

| 口径 | 原 Fluxbox | 新 Xfce | 增量 |
|---|---:|---:|---:|
| APK 包归档合计 | 415.60 MiB | 435.70 MiB | 20.10 MiB |
| APK 安装体积合计 | 1,018.06 MiB | 1,068.08 MiB | 50.02 MiB |
| 系统包数量 | 314 | 350 | 36 |

旧锁文件与新镜像 `/lib/apk/db/installed` 元数据对比；两者都包含 CJK，不重复计算。归档合计不同于整个 OCI 压缩率。

新成品镜像压缩层 **656,413,994 bytes（626.00 MiB）**，rootfs tar **1,758,677,504 bytes（1.64 GiB）**。旧方案仅下载约 3.93 MiB 的 Alpine 基础层，再安装整个环境，不能将这个基础层大小当作旧桌面总大小。此处尚无同内容的旧成品 OCI 镜像对照。

## 无网络验收

```sh
codesign --force --sign - --entitlements helper/virtualization.entitlements .local/d1/helper-build/release/PotImageCheck
.local/d1/helper-build/release/PotImageCheck .local/d1/offline-image-check .local/d1/resources scripts/tests
```

`PotImageCheck` 在全新镜像仓库与数据目录导入本地镜像，用 `networking: false` 启动 VM。检查 broker、Chromium CDP、Runtime、Pi 版本、桌面进程、四种语言的示例字形和浏览器正常退出；结果位于验证目录 `data/offline-result.json`，不调用模型。重跑时使用新的验证目录，确保验证冷启动。
