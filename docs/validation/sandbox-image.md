# Sandbox 成品镜像验收

日期：2026-10-04，Apple Silicon 本地 VM。实现与体积口径见 [Sandbox 镜像](../sandbox-image.md)。本次未调用真实模型，已有请求账本保持 27。

- Swift Helper / ImageBuilder / ImageCheck 优化构建通过；Python AST、shell 语法和 `git diff --check` 通过。
- 在独立镜像仓库和全新 `/data` 中导入 App 随附镜像；VM 设置 `networking: false`。桌面、broker、Chromium CDP、Runtime 与 Pi 0.87.1 启动成功，不运行 apk/npm 安装；SIGTERM 后确认 Chromium 退出标记。
- 中日韩字体检查覆盖简体、繁体、日文假名/汉字、韩文 Hangul/Hanja 示例。已有 UI 验证确认这些字符及 emoji 在终端可见；不代表全 Unicode 覆盖或 IME 输入法验收。
- 原 Fluxbox 与 Xfce 的 APK 元数据比较：归档增加 21,073,898 bytes，安装增加 52,451,059 bytes；不把基础 Alpine 层和完整桌面镜像直接比较。

本地证据在 `.local/d1/xfce-check/` 与 `.local/d1/offline-image-check*/data/`；不提交用户数据、日志中的 token 或构建产物。

最终镜像 `pot.local/sandbox:989970b5d23191baf89f`：无网络完整检查（含导入/解包、服务检查、正常退出）冷启动 10 秒，复用数据再启动 8 秒。修复了旧数据目录残留 socket 引起的递归 chown 失败。最终 App 内确认暖白 Pot 背景、三项右键菜单、Thunar 和终端可用，简繁中文、日文、韩文与 emoji 显示正常；既有任务与配置可继续读取。
