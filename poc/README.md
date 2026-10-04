# POC 代码

本目录保存实验代码与小型证据；结论、版本固定、复现与限制写在 [docs/validation](../docs/validation/)。当前状态总览见 [文档导航](../docs/README.md#当前状态2026-10-03)，计划与下一阶段见 [POC 计划](../docs/poc-plan.md)。

| 目录 | 阶段 | 入口（从仓库根目录运行） | 报告 |
|---|---|---|---|
| [macos-shell/](macos-shell/) | P0/P1 GPUI + Swift bridge + tray | `poc/macos-shell/poc doctor/build/run/status/stop` | [macos-shell](../docs/validation/macos-shell.md) |
| [remote-desktop/](remote-desktop/) | P2 远程 Docker 桌面 | `POT_REMOTE_SSH=… poc/remote-desktop/poc doctor/build/run/tunnel` | [remote-docker-gui](../docs/validation/remote-docker-gui.md) |
| [integrated-desktop/](integrated-desktop/) | P3 同窗口 viewer + 控制权 broker | `poc/integrated-desktop/poc prepare/run/open`（需 P2 运行中） | [integrated-desktop](../docs/validation/integrated-desktop.md) |
| [native-helper/](native-helper/) | P4 Swift helper + 本地 VM + 独立 App | `poc/native-helper/poc prepare/build/bundle/open` | [native-helper-bundle](../docs/validation/native-helper-bundle.md) |
| [baseline/](baseline/) | 从 Notes 迁入的历史实验 | 见复现说明 | [baseline-report](../docs/validation/baseline-report.md)、[baseline-reproduction](../docs/validation/baseline-reproduction.md) |

- 运行前先读各目录 README。构建、下载与运行状态默认在被忽略的 `.local/poc`；`POT_POC_ROOT` 可覆盖，所有命令须使用相同值。
- 远程连接目标从 `~/Workspace/Machines` 选择，不在本仓库保存连接账号或秘密。
- `baseline/` 的历史 evidence 保留失败与限制，不改写成当前原型的通过结果；部分探针依赖 `/tmp/pot-research-20261003`。
- 轻量回归（fixture，不是原生桌面验收）：在 `baseline/` 下运行 `python3 test_runtime.py`、`python3 test_adversarial.py`、`python3 local_oauth_poc.py`，共 29 项。
