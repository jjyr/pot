# POC 代码

`baseline/` 是从 Notes 迁入的历史实验：GPUI/Swift bridge、VNC、Apple library patch、引擎探针及小型 Runtime/OAuth 测试。

- [现有实验复现说明](../docs/validation/baseline-reproduction.md)
- [证据及结论](../docs/validation/baseline-report.md)
- [新 POC 实施计划](../docs/poc-plan.md)

新原型按计划建立 `macos-shell/`、`remote-desktop/`、`native-helper/` 等目录；本次初始化不提前选定完整 App 的 crate/服务布局。历史 evidence 保留失败与限制，不改写成当前原型的通过结果。
