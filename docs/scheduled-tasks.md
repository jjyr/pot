# 定时计划

定时计划是持久定义，每次触发创建独立后台任务和 Pi 会话。Rust Runtime 决定触发、去重、并发和通知；主 Agent 通过工具管理计划，不需要常驻模型或执行 sleep/cron。

```
聊天 / 任务 → 定时计划
             │ 创建、编辑、暂停、立即运行、删除
             ▼
Runtime 调度器 → SQLite tasks.v2（计划 + 触发记录 + 任务队列）
             │ 到期，事务内创建一次执行
             ▼
共享任务队列（默认并发 5）→ 独立 Pi / 工作目录
             │ .pot-schedule-result.json
             ▼
校验结果 → 比较 fingerprint → 保存历史
             └ 有变化 / 失败 → 主聊天汇总
```

## 使用

在主聊天描述目标、时间、时区与频率，由主 Agent 创建计划。「任务 → 定时计划」只查看和管理已有计划，不提供手动新建入口。主 Agent 可使用 `pot_list_schedules`、`pot_get_schedule`、`pot_manage_schedule`。列表默认只返回摘要，详情和执行历史按需读取。

- 一次：当地日期时间 `YYYY-MM-DD HH:MM`。
- 间隔：正整数分钟，从创建或恢复时起计算。
- 每天：当地时间 `HH:MM`。
- 每周：当地时间加星期，1 为周一，7 为周日。
- 时区：IANA 名称，默认 `Asia/Shanghai`；保存的时区不随机器旅行而改变。
- 通知：默认有变化或失败时通知；可选每次通知、仅失败。首次成功观察视为变化。
- 错过触发：默认合并补跑一次；选择跳过时，迟到至少 60 秒的触发只记历史。

暂停和删除仅停止未来触发，当前排队/执行任务继续；取消当前执行在任务页面操作。暂停计划也可立即运行。前次仍在排队或执行时跳过本次，避免积压。删除保留历史，API 默认隐藏已删除定义。

隐藏窗口继续运行；Quit、睡眠、关机期间不会执行。恢复后按补跑策略处理，不唤醒 Mac。中断的模型执行不自动重放。模型未就绪或预算耗尽时暂缓调度。

夏令时：重复小时选较早时刻且只触发一次；不存在的日/周时间跳过当天；不存在的一次性时间拒绝保存。

## 结果与存储

Worker 写入 `.pot-schedule-result.json`：`summary`、稳定 `fingerprint`、非空 `evidence` 数组，以及可选 `artifacts` 相对路径数组。缺失或无效结果记失败，不能当作“无变化”。目标编辑后重置比较基线，旧执行不会覆盖新目标的基线。无变化且静默的执行不唤醒主 Agent，但保留任务和计划历史；每次检查本身仍可能产生模型费用。

计划最多 100 个未删除定义，每个保留最近 100 条已结束/跳过记录；未结束执行保留。任务沿用现有总量限制。调度队列与计划一起保存到 `tasks.v2`，自动读取旧 `tasks.v1`；旧版本程序不识别新元数据，不应直接降级。

接口：`GET /api/v1/schedules` 分页查询，`POST /api/v1/schedules` 接受 `request_id`、`action`、`schedule_id`、`spec`。写入请求重复使用相同 ID 防止重复创建；相同 ID 不同内容拒绝。运行记录链接到任务详情，沿用任务状态及归档方式。

## 验证

调度测试使用显式时钟，覆盖时区/DST、错过触发、重叠、暂停、幂等、重启恢复、持久化失败回滚、基线更新和旧数据迁移。真实 Pi 对本地模拟模型验证创建计划、两次独立执行、结构化结果、有变化汇总和无变化静默，不调用付费服务。

```sh
CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-runtime real_pi -- --ignored
```
