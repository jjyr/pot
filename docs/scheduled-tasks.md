# Scheduled tasks

Each node owns persistent schedules, triggering, deduplication, task admission and
notifications. Each admitted trigger creates an independent local background Task/Agent
session, unless its optional before-run probe skips the occurrence. Main Agent manages schedules through tools without a resident model or
sleep/cron commands.

Assistant-scoped schedule UI/API calls enter the Agent Link operation port under
`schedules.manage`. The current owner adapter advertises it; peer adapters do not.
The existing scheduler and its occurrence transactions remain in-process. Linked
task submission does not imply remote schedule management or cross-node occurrence
dispatch. See [Agent Link](agent-links.md#product-operation-port).

```
Chat / tasks -> schedules
             | Create, edit, pause, run now, delete
             ▼
Local scheduler -> SQLite tasks.sqlite (schedules + triggers + task queue)
             | Due: reserve occurrence; optional probe before Task creation
             ▼
Shared task queue (default concurrency 8) -> independent Task / current machine execution policy
             │ final reply + .pot-artifacts/ files
             ▼
Ordinary Task outcome preparation -> collect optional artifacts
             | Atomic: final Task + history + policy + outcome hook
             v
Shared Agent inbox -> eligible outcomes -> shared main chat summary
```

## Usage

Describe the goal and timing in main chat; the assistant can write a Cron rule and create the schedule. Tasks -> Schedules also creates, views and directly edits definitions. The editor shows the Cron expression, an editable IANA time zone and up to five upcoming executions from the backend before saving. Preview errors identify the relevant field; stale responses cannot replace newer input. Tools: `list_schedules`, `get_schedule`, `manage_schedule`. Lists return summaries; details/history are loaded on demand.

`manage_schedule.spec.goal` must contain non-whitespace text and accepts at most
512 KiB = 524288 UTF-8 bytes (512 x 1024). This is a byte limit, not a character
limit: a Chinese character commonly uses three UTF-8 bytes. Create, update and
persisted-definition validation use the same limit. Oversized goals report
`spec.goal` with the actual and allowed UTF-8 byte counts. The title remains
nonempty, not whitespace-only, and limited to 80 Unicode characters.

Scheduled Task admission, initial input envelopes and execution snapshots
accommodate the full goal plus the title and run instructions, including JSON
escaping. Ordinary task submission and continuation input limits remain unchanged.
This parameter/storage capacity
is separate from the selected model's token context window and output allowance;
a saved goal may exceed that model's executable context. Goals are not truncated
or summarized to fit the byte limit.

The shared UI shows a bounded, independently scrolling schedule list with a
visible count and page controls. Cards retain the owning assistant and Space
badge. Opening a card loads the full definition and paginated history; editing
returns to that detail through the normal dirty-exit guard. Removal requires
explicit confirmation. A failed action retains its captured owner and write
request ID, including Run now, so a lost response cannot create another Task on
retry.

- Once: local date/time `YYYY-MM-DD HH:MM`.
- Interval: positive integer minutes, measured from creation/resume.
- Cron: five fields, `minute hour day-of-month month day-of-week`, with no seconds.
  `0 8-20 * * *` runs at each local hour from 08:00 through 20:00 inclusive
  (13 executions per day). After 20:00 its next execution is 08:00 the next day.
- Time zone: saved IANA identifier. UI creation initializes it once from the
  browser/WebView system zone (`Intl.DateTimeFormat().resolvedOptions().timeZone`).
  Assistant tool creation without `timezone` resolves the **owning node's** OS
  system zone, regardless of client zone. Explicit zones
  override either default; omission on tool updates preserves the saved zone.
  The default follows the system **at creation**; saved plans never drift when
  the system zone changes or the device travels. Editing and locale refreshes
  preserve the selected zone. The node checks `TZ`, the `/etc/localtime`
  zoneinfo link, `/etc/timezone`, then a resolvable Go local zone name. It embeds
  IANA tzdata. An unresolvable zone (including offset-only/POSIX `TZ` or `Local`)
  requires an explicit IANA zone; there is no silent Shanghai/UTC fallback.
  Linux and macOS zoneinfo layouts are supported; other OS layouts may require
  explicit zones.
- Report to assistant: every completion by default, or failures only. Failures include execution interruption; cancellation is reported only with every-completion delivery. Skipped triggers only enter history. The owning assistant decides whether the result needs a user-facing reply; change detection belongs to the task rather than the scheduler.
- Missed triggers: coalesce into one catch-up by default; with skip, triggers at least 60 seconds late only enter history.

Pause/delete stops future triggers; queued/running tasks continue and can be interrupted on the task page. Paused schedules can run now. Skip a trigger while its previous run is queued/running to prevent backlog. Delete preserves history; APIs hide deleted definitions by default.

Schedules require their owning node to remain running. App Quit or machine
shutdown stops that node; closing a client leaves the node running. After sleep
or restart, the node applies its catch-up policy. Interrupted model execution
is not replayed automatically. Scheduling waits while the model is unready or
budget exhausted.

Cron uses `robfig/cron/v3` with only the five standard fields enabled. Lists,
ranges, positive steps, `*`, and English month/weekday names are supported.
Sunday is `0`, Monday `1`, through Saturday `6`. When both day-of-month and
day-of-week are restricted, either matches (traditional Cron OR semantics);
an unrestricted field leaves the other in control. Seconds, macros (`@daily`),
inline timezone directives, `?`, `L`, `W`, and `#` are rejected. Expressions are
bounded to 256 bytes; a rule with no occurrence within the bounded eight-year
search horizon (including leap-day gaps across non-leap centuries) fails
validation, including when saving a paused definition.
This also bounds impossible dates such as `0 0 31 FEB *`.

DST: choose the earlier occurrence of repeated wall times and trigger once;
skip nonexistent Cron wall times; reject nonexistent one-off times on save.
Elapsed intervals retain their anchor and actual duration across DST changes.
Preview and dispatch both call the same backend `Next` implementation. The
read-only `POST /api/v1/schedules/preview` accepts `kind`, `cron`, `timezone`,
`at`, `every_minutes` and returns `timezone`, `times` (epoch milliseconds), or
stable `field`/`code` identifiers and diagnostic details. It does not resolve
models, create Tasks or persist plans. The shared Web/Wails/remote-Space
transport carries this same endpoint.

The tasks database migration converts persisted daily/weekly definitions and
management response snapshots to Cron, removing the old clock/weekday fields.
It preserves explicit time zones, next due instants, generation,
policies, model/Runtime selections, run history and write receipt digests.
Once and elapsed interval definitions remain intact (90 minutes remains an
elapsed 90-minute interval). Definition edits preserve each in-flight occurrence's original notification policy.

Model selection uses optional `spec.profile` (configured Provider connection ID)
and `spec.model`. Selection is autonomous and requires no routing approval or
allowlist. Each occurrence resolves these fields anew; omitting both uses the
owning assistant's Task default, or its active configuration when that default is
unset. See [Task routing](task-coordination.md#autonomous-model-routing).

Creation saves `spec.machine_id` (defaulting to the owning node when omitted).
Updates without `machine_id` preserve the saved selection. Every occurrence
creates a new Task using the node's current execution policy. New work requires
the owning node's local execution target. Historical remote machine IDs remain
for history and recovery; they do not authorize new remote execution. No schedule
selects or retains a container.

## Results and storage

Scheduled and manual Tasks share one result contract: the worker's final reply is
the summary, and optional deliverable files go in `.pot-artifacts/` under the
original Task workspace. There is no result JSON, required evidence field or
scheduler fingerprint. Evidence and limitations belong in the reply or attached
report. See [Task results](task-coordination.md#results-and-artifacts) for file
collection limits and delivery diagnostics.

Each Task stores an explicit schedule/occurrence origin and each occurrence saves
its notification policy at creation. Task state, schedule history and the shared
Agent inbox event commit together. Artifact collection errors retain the reply
and execution status; they do not turn a completed check into a failed Task.
Historical Tasks with retired remote destinations retain their original references
and recovery fences; the Worker is not relaunched locally.

Scheduled and ordinary results use the same bounded summary batches and durable
reply acknowledgment. Policy-excluded outcomes remain in the inbox as `suppressed`
without a model call. Accepted background notification execution alone does not acknowledge an
outcome. See [Hooks](hooks.md) and [Agent messages](assistant-messages.md#completion-events).

Workers execute independently. They may message their owner when coordination is
useful, and the owner may inspect or guide a Task on demand. Model/tool rounds do
not automatically send progress messages to the owner. Terminal outcomes retain
the existing automatic delivery; this does not introduce periodic polling or an
idle assistant wakeup. Task-specific change detection and business checkpoints
follow the goal's own storage and reporting instructions.

At most 100 undeleted definitions, with the latest 100 ended/skipped records per schedule; unfinished runs remain. Tasks retain existing total limits. Queue/schedules persist as `tasks.sqlite`. Upgrades retain Task and schedule identities and original execution evidence.

API: paginated `GET /api/v1/schedules`; `POST /api/v1/schedules` accepts `request_id`, `action`, `schedule_id`, `spec`. Reuse the same write request ID for creation and management retries; reject changed content under the same ID. Run records link to task details and share task states/archive behavior.

Tests and operating commands are documented in [Development](development.md#checks).

## Before-run probes

An optional `spec.before_run` runs one local script **before any Task or Agent
session is created**. Use `manage_schedule` create/update or the schedule editor;
both save the same definition. `get_schedule` includes the complete configuration
and paginated hook results. List responses omit the script and goal. On update,
omitting `before_run` preserves it; explicit `null` removes it. A script controls
only whether this occurrence runs. It cannot change the goal, model, profile,
machine, prompt or notification policy.

```json
{
  "action": "update",
  "schedule_id": "SCHEDULE_ID",
  "spec": {
    "title": "Release monitor",
    "goal": "Report relevant new releases with evidence",
    "kind": "cron",
    "cron": "0 8-20 * * *",
    "timezone": "Asia/Shanghai",
    "before_run": {
      "command": ["python3", "/path/to/check_updates.py"],
      "cwd": "/path/to/project",
      "timeout_seconds": 30
    }
  }
}
```

Provide the other intended definition fields on update as usual. The command is
an argv array (1–64 strings, at most 16 KiB including cwd), never implicit shell
syntax. Explicit `bash`/`sh` argv is supported. Timeout defaults to 30 seconds;
0 also selects that default; the maximum is 300 seconds. `cwd` is at most 4096
bytes. Omitted/empty cwd uses the owning assistant's persistent execution workspace
(`WORKSPACE/assistants/ASSISTANT_ID`); relative cwd resolves against that workspace,
never the scheduler's process directory or a future Task directory. Symlinks are
resolved before launch. The directory must already exist. Relative script/file
arguments resolve in the resulting cwd. No tilde, environment or shell expansion
is added.

The saved machine must still be the node's local execution target. The current
machine policy and execution admission apply. Protected policy uses the existing
sandbox with cwd as its workspace and Runtime state excluded; full-access policy
uses supervised host execution. Denied policy or missing sandbox support records
a hook error, then follows normal Task admission. Saving a script grants no
execution authority. Shell exact-command grants do not authorize this separate
argv command. Probes do not ask for unattended host exceptions. They receive the
executor's reviewed environment, without credential injection, model history,
plugin credentials or login-shell initialization. The script itself is responsible
for any data or credentials it explicitly reads under that machine policy.

Stdin is a single bounded JSON object (at most 4 KiB), followed by EOF:

```json
{"schedule_id":"...","occurrence_id":"...","trigger":"scheduled","scheduled_at":"2026-10-16T00:00:00Z","timezone":"Asia/Shanghai"}
```

`occurrence_id` is stable across retries. `trigger` is `scheduled` or `manual`;
`scheduled_at` is the original trigger instant in UTC RFC 3339, not script start
time. Stdout must contain only one JSON object:

```json
{"status":"run"}
```

```json
{"status":"skip","reason":"No new content"}
```

`status` is only `run` or `skip`; `reason` is an optional string of at most 1000
UTF-8 bytes. Empty stdout, `{}`, or an object without `status` defaults to run.
Unknown/duplicate fields, nulls, arrays, multiple objects, invalid UTF-8/JSON or
other status values are errors. A nonzero exit remains an error even with valid
skip stdout. Each output stream is bounded to 16 KiB; truncating either is an
error. Stderr is diagnostic only: successful probes discard it; failed probes
retain at most 2000 bytes of diagnostic information in history. Do not print
secrets. Script output is untrusted display data and never enters the Task prompt.

Only a successful, valid skip consumes this trigger without creating a Task,
Agent session, model request, business checkpoint or assistant inbox event.
It enters history as `skipped_hook`. A recurring plan remains enabled; a scheduled
one-off occurrence is consumed even when skipped. Manual Run now also runs the
probe and remains available while paused; it does not consume the regular next
due time. Run-now responses with probes return `occurrence_id`; use detail/history
to follow it. Retrying the same request ID returns its original receipt.

```text
scheduled / Run now
    -> existing readiness, misfire, overlap and identity checks
    -> transaction: reserve occurrence + advance scheduled next due
    -> bounded async probe -> existing policy / executor / supervisor
           | skip -> history only
           | run or known error after cleanup
           v
       transaction: generation fence + enqueue original Task
           -> existing Task queue -> final reply / artifacts / inbox policy
```

The scheduler runs at most eight probe workers concurrently without holding a DB
transaction while a script executes. An unfinished probe reserves its schedule
against other occurrences, including manual runs. For plans with probes, pause/resume, edit and delete
advance the generation: an old result cannot create a new Task. Existing Tasks
already created retain their ordinary lifecycle. Misfire and previous-active
checks precede the probe; a skipped missed/overlapping trigger runs no script.

Occurrences and immutable operation receipts survive restart. Completed receipts
can finish the original occurrence without repeating the script. A receipt whose
effects or process cleanup are unknown is **not** replayed, and cannot default to
run: history becomes `hook_blocked`, with no Task. The unfinished reservation and
existing operation-scope fence prevent subsequent scripts/Tasks from bypassing
it, even after editing the plan. There is no automatic retry loop, replacement
process or automatic assertion that the old process is gone. Reconciliation of
the original execution/receipt is an operator recovery boundary; no new probe
recovery UI is introduced. A model-selection/admission preparation failure after
the probe also records a blocked occurrence for inspection. Ordinary timeout or
execution errors continue only after the executor confirms process-tree cleanup.
A crash before the executor's durable effect receipt can safely proceed to its
first launch; a crash after that boundary requires a complete receipt or stays
blocked. Probe configuration and hook results are stored in the initial Tasks
schema. Probes do not maintain or update a scheduler comparison baseline.
Older development databases follow the reset boundary in
[Database migrations](database-migrations.md).

A minimal probe for a local source marker can be saved as
`/path/to/check_updates.py` (replace paths with your own):

```python
import json
from pathlib import Path
import sys

context = json.load(sys.stdin)
# A separate collector maintains these files. Let the successful reporting
# workflow update reported-version.txt; the probe does not consume the baseline.
current = Path("current-version.txt").read_text().strip()
reported = Path("reported-version.txt")
if reported.exists() and reported.read_text().strip() == current:
    print(json.dumps({"status": "skip", "reason": "No new version"}))
else:
    print(json.dumps({"status": "run"}))
```

Errors reading these files exit nonzero and therefore allow the normal Task to
investigate. No built-in scanner, external subscription or report queue is added.
These scripts are distinct from trusted lifecycle [event hooks](hooks.md).
