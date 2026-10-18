# Scheduled tasks

Control owns schedules in both local and remote modes. Schedules are persistent definitions; each trigger creates an independent background task/Agent session. Control owns triggering, deduplication, task admission and notifications. The destination Runtime manages actual Agent execution and local concurrency. Main Agent manages schedules through tools without a resident model or sleep/cron commands.

```
Chat / tasks -> schedules
             | Create, edit, pause, run now, delete
             ▼
Runtime scheduler -> SQLite tasks.sqlite (schedules + triggers + task queue)
             | Due: create one run transactionally
             ▼
Shared task queue (default concurrency 8) -> independent Task / current machine execution policy
             │ .pot-schedule-result.json
             ▼
Task outcome preparation -> validate result -> compare fingerprint
             | Atomic: final Task + history + baseline + policy + outcome hook
             v
Shared Agent inbox -> eligible outcomes -> shared main chat summary
```

## Usage

Describe goal, time, time zone, and frequency in main chat; main Agent creates the schedule. Tasks -> Schedules only views/manages existing definitions without manual creation. Tools: `list_schedules`, `get_schedule`, `manage_schedule`. Lists return summaries; details/history are loaded on demand.

The shared UI shows a bounded, independently scrolling schedule list with a
visible count and page controls. Cards retain the owning assistant and Space
badge. Opening a card loads the full definition and paginated history; editing
returns to that detail through the normal dirty-exit guard. Removal requires
explicit confirmation. A failed action retains its captured owner and write
request ID, including Run now, so a lost response cannot create another Task on
retry.

- Once: local date/time `YYYY-MM-DD HH:MM`.
- Interval: positive integer minutes, measured from creation/resume.
- Daily: local time `HH:MM`.
- Weekly: local time and weekday, 1 = Monday, 7 = Sunday.
- Time zone: IANA name, default `Asia/Shanghai`; saved zones do not change when the machine travels.
- Notifications: changes/failures by default; optionally every run or failures only. First successful observation counts as changed. Failures include execution interruption; cancellation is reported only with every-run notifications. Skipped triggers only enter history.
- Missed triggers: coalesce into one catch-up by default; with skip, triggers at least 60 seconds late only enter history.

Pause/delete stops future triggers; queued/running tasks continue and can be interrupted on the task page. Paused schedules can run now. Skip a trigger while its previous run is queued/running to prevent backlog. Delete preserves history; APIs hide deleted definitions by default.

Schedules require their owning Runtime to remain running. Local App Quit/sleep/shutdown stops its Runtime; closing a remote client does not stop Control. Runtime resume applies catch-up policy without waking the execution machine. Interrupted model execution is not replayed automatically. Scheduling waits while the model is unready or budget exhausted.

DST: choose the earlier occurrence of repeated hours and trigger once; skip nonexistent daily/weekly times for that day; reject nonexistent one-off times on save.

Model selection uses optional `spec.profile` (configured Provider connection ID)
and `spec.model`. Selection is autonomous and requires no routing approval or
allowlist. Each occurrence resolves these fields anew; omitting both uses the
owning assistant's active configuration. See [Task routing](task-coordination.md#autonomous-model-routing).

Creation saves `spec.machine_id` (defaulting to the owning Control when omitted). Updates without
`machine_id` preserve the selected machine. Every occurrence creates a new Task,
which reads that machine's current execution policy at dispatch. No schedule
selects or retains a container. Remote machine IDs restore while offline;
execution requires the selected machine to be available.

## Results and storage

Worker writes `.pot-schedule-result.json`: `summary`, stable `fingerprint`, nonempty `evidence` array, optional relative-path `artifacts` array. Control reads the result from the task's immutable execution target through the bounded file transport, including remote machines. Results must be ordinary files no larger than 16 KiB. Missing, oversized or invalid results fail rather than count as unchanged. Editing a goal resets the comparison baseline; old runs cannot overwrite the new baseline. Silent unchanged runs do not wake main Agent but retain task/schedule history; each check may still incur model costs.

Each Task stores an explicit schedule/occurrence origin and each occurrence saves
its notification policy at creation. Task coordination validates the report before
publishing the final outcome. Task state, schedule history, baseline and the shared
Agent inbox event commit together. A missing or invalid report produces one failed
event. An unavailable remote transport keeps the Task active until its original
result can be read; the Worker is not relaunched.

Scheduled and ordinary results use the same bounded summary batches and durable
reply acknowledgment. Unchanged or policy-excluded outcomes remain in the inbox
as `suppressed` without a model call. Accepted summary execution alone does not
acknowledge an outcome. See [Hooks](hooks.md) and
[Agent messages](assistant-messages.md#completion-events).

At most 100 undeleted definitions, with the latest 100 ended/skipped records per schedule; unfinished runs remain. Tasks retain existing total limits. Queue/schedules persist as `tasks.sqlite`. Old task metadata is not imported.

API: paginated `GET /api/v1/schedules`; `POST /api/v1/schedules` accepts `request_id`, `action`, `schedule_id`, `spec`. Reuse the same write request ID for creation and management retries; reject changed content under the same ID. Run records link to task details and share task states/archive behavior.

Tests and operating commands are documented in [Development](development.md#checks).
