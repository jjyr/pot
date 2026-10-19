package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"io"
	"path/filepath"
	"sync"
	"time"
)

type taskLifecycle struct {
	wake     chan struct{}
	wg       sync.WaitGroup
	watching map[string]bool
	mu       sync.Mutex
	remote   *remoteState
}

func (s *Service) openTasks() error {
	var err error
	s.taskLife = &taskLifecycle{wake: make(chan struct{}, 1), watching: map[string]bool{}}
	s.Machines, err = controlplane.Open(filepath.Join(s.options.Root, "machines.sqlite"), controlplane.Options{RuntimeID: s.options.RuntimeID, Embedded: s.options.Embedded, CanJoin: func() error {
		definitions, err := s.Registry.Definitions(s.ctx)
		if err != nil {
			return err
		}
		hasIntegrations, err := s.Integrations.HasState(s.ctx)
		if err != nil {
			return err
		}
		if len(definitions) != 0 || len(s.Profiles.Snapshot().Profiles) != 0 || hasIntegrations {
			return errors.New("Runtime with AI configuration cannot join as Runner; use a separate data directory")
		}
		return nil
	}})
	if err != nil {
		return err
	}
	// Directory serializes this gate with enrollment admission and its CanJoin
	// configuration check, so a concurrent Provider save cannot hide AI state.
	s.Profiles.ownerAction = s.Machines.OwnerAction
	s.RunnerJournal, err = runner.Open(filepath.Join(s.options.Root, "runner.sqlite"), s.options.RuntimeID, s.options.BootID)
	if err != nil {
		return err
	}
	s.Runner = &runner.Executor{Journal: s.RunnerJournal, Executions: s.options.Manager, Context: s.ctx, Prepare: s.prepareTask}
	s.Tasks, err = tasks.Open(filepath.Join(s.options.Root, "tasks.sqlite"), tasks.Options{ControlID: s.options.RuntimeID, RuntimeID: s.options.RuntimeID, Port: taskRouter{s}, ResolveModel: s.selectTask, ResolveTarget: s.taskTarget, ResolveMachine: s.selectTaskMachine, ValidateTarget: s.validateTaskTarget, AdmitModel: s.admitTaskModel, AwaitingApproval: s.taskAwaitingApproval, PublishHook: s.publishHook, OutcomesChanged: func() { s.Changes.Notify("task-outcomes") }, PrepareOutcome: func(ctx context.Context, record tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
		return s.Schedules.PrepareOutcome(ctx, record, event)
	}})
	if err != nil {
		return err
	}
	if _, err = s.Tasks.DB().Exec(`CREATE TABLE IF NOT EXISTS task_event_cache(task_id TEXT NOT NULL,sequence INTEGER NOT NULL,event TEXT NOT NULL,PRIMARY KEY(task_id,sequence));CREATE TABLE IF NOT EXISTS task_routing(singleton INTEGER PRIMARY KEY CHECK(singleton=1),body TEXT NOT NULL)`); err != nil {
		return err
	}
	s.Schedules, err = schedule.New(schedule.Options{Tasks: s.Tasks, Ready: s.taskReady, Results: taskFiles{s}})
	if err != nil {
		return err
	}
	s.Mailbox, err = assistant.OpenMailbox(filepath.Join(s.options.Root, "tasks.sqlite"), s.options.RuntimeID)
	if err != nil {
		return err
	}
	s.taskLife.wg.Add(1)
	go s.taskLoop()
	if s.Machines.Role() == "runner" {
		s.StartRunner()
	}
	return nil
}
func (s *Service) taskAwaitingApproval(assistant, task, run string, generation uint64) bool {
	for _, approval := range s.Permissions.Pending(assistant) {
		request := approval.Request
		if request.Task == task && request.Turn == run && request.Generation == generation {
			return true
		}
	}
	return false
}
func (s *Service) wakeTasks() {
	if s.taskLife != nil {
		select {
		case s.taskLife.wake <- struct{}{}:
		default:
		}
	}
}
func (s *Service) watchTaskOwner(owner string, c *assistant.Conversation) {
	if s.taskLife == nil {
		return
	}
	s.taskLife.mu.Lock()
	if s.taskLife.watching[owner] {
		s.taskLife.mu.Unlock()
		return
	}
	s.taskLife.watching[owner] = true
	s.taskLife.wg.Add(1)
	s.taskLife.mu.Unlock()
	go func() {
		defer s.taskLife.wg.Done()
		updates, remove, err := c.Subscribe(s.ctx)
		if err != nil {
			return
		}
		defer remove()
		for {
			select {
			case <-s.ctx.Done():
				return
			case _, ok := <-updates:
				if !ok {
					return
				}
				s.wakeTasks()
			}
		}
	}()
}
func (s *Service) taskReady(ctx context.Context, owner string) (bool, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return false, err
	}
	s.watchTaskOwner(owner, c)
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	if !c.Configured() || snapshot.Turn != nil {
		return false, nil
	}
	waiting, err := s.Usage.Waiting(ctx, owner)
	if err != nil {
		return false, err
	}
	if waiting == nil {
		return true, nil
	}
	return s.Usage.Ready(ctx, owner, storage.Now())
}
func (s *Service) taskTarget(ctx context.Context, machine string) (core.ExecutionTarget, error) {
	if machine == "" {
		machine = s.options.RuntimeID
	}
	if s.Machines.Role() == "runner" {
		return core.ExecutionTarget{}, errors.New("Runner cannot own Task coordination")
	}
	if _, _, err := s.Permissions.Policy(machine); err != nil {
		return core.ExecutionTarget{}, err
	}
	if machine != s.options.RuntimeID {
		if _, err := s.Machines.Peer(machine); err != nil {
			return core.ExecutionTarget{}, err
		}
	}
	return core.RuntimeTarget(machine), nil
}
func (s *Service) taskLoop() {
	defer s.taskLife.wg.Done()
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	runnerSub := s.RunnerJournal.Subscribe()
	defer runnerSub.Close()
	mailSub := s.Mailbox.Changes.Subscribe()
	defer mailSub.Close()
	policySub := s.Permissions.Changes.Subscribe()
	defer policySub.Close()
	for _, source := range []*core.Subscription{sub, runnerSub, policySub, mailSub} {
		s.taskLife.wg.Add(1)
		go func(source *core.Subscription) {
			defer s.taskLife.wg.Done()
			for {
				if _, err := source.Next(s.ctx); err != nil {
					return
				}
				if source == runnerSub && s.Machines.Role() == "runner" {
					if peer, err := s.Machines.ControlPeer(); err == nil {
						ctx, cancel := context.WithTimeout(s.ctx, time.Second)
						_ = peer.Notify(ctx, controlplane.Request{ID: storage.ID(), Method: "event.runner.changed", Params: json.RawMessage(`{}`)})
						cancel()
					}
				}
				s.Changes.Notify("tasks")
				s.Changes.Notify("assistant-mail")
				s.Changes.Notify("schedules")
				s.wakeTasks()
			}
		}(source)
	}
	usageSub := s.Changes.Subscribe()
	defer usageSub.Close()
	s.taskLife.wg.Add(1)
	go func() {
		defer s.taskLife.wg.Done()
		var seen uint64
		for {
			topics, err := usageSub.Next(s.ctx)
			if err != nil {
				return
			}
			if topics["usage"] > seen {
				seen = topics["usage"]
				s.wakeTasks()
			}
		}
	}()
	for {
		if s.ctx.Err() != nil {
			return
		}
		if s.options.Maintenance != nil && s.options.Maintenance() {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		if err := s.Tasks.Tick(s.ctx); err != nil && s.ctx.Err() == nil {
			s.Changes.Notify("tasks")
		}
		s.resumeTaskAllowance(s.ctx)
		s.reconcileRemoteResources(s.ctx)
		if err := s.Schedules.Tick(s.ctx, storage.Now()); err != nil && s.ctx.Err() == nil {
			s.Changes.Notify("schedules")
		}
		s.deliverNotificationsForOwners(s.ctx)
		s.deliverAssistantMail(s.ctx)
		at, err := s.Schedules.NextWake(s.ctx)
		var timer *time.Timer
		var due <-chan time.Time
		if err == nil && at != nil {
			delay := time.Until(time.UnixMilli(*at))
			if delay <= 0 {
				delay = time.Minute
			}
			timer = time.NewTimer(delay)
			due = timer.C
		}
		select {
		case <-s.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.taskLife.wake:
		case <-due:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}
func (s *Service) deliverNotificationsForOwners(ctx context.Context) {
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return
	}
	for _, definition := range definitions {
		s.deliverNotifications(ctx, definition.ID)
	}
}

type taskRouter struct{ s *Service }

func (p taskRouter) original(ctx context.Context, control, task, run string) (runner.Port, error) {
	var raw []byte
	err := p.s.Tasks.DB().QueryRowContext(ctx, "SELECT body FROM tasks WHERE id=? AND json_extract(body,'$.run_id')=?", task, run).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var r tasks.Record
	if err = core.DecodeJSON(raw, &r); err != nil {
		return nil, err
	}
	if control != p.s.options.RuntimeID || r.Launch == nil {
		return nil, errors.New("original Task launch unavailable")
	}
	if r.Launch.RuntimeID == p.s.options.RuntimeID {
		return p.s.Runner, nil
	}
	return controlplane.RemotePort{Directory: p.s.Machines, RuntimeID: r.Launch.RuntimeID}, nil
}
func (p taskRouter) Launch(ctx context.Context, l runner.Launch) (runner.Run, error) {
	if l.RuntimeID == p.s.options.RuntimeID {
		return p.s.Runner.Launch(ctx, l)
	}
	return (controlplane.RemotePort{Directory: p.s.Machines, RuntimeID: l.RuntimeID}).Launch(ctx, l)
}
func (p taskRouter) Inspect(ctx context.Context, c, t, r string) (*runner.Run, error) {
	port, err := p.original(ctx, c, t, r)
	if err != nil {
		return nil, err
	}
	return port.Inspect(ctx, c, t, r)
}
func (p taskRouter) Stop(ctx context.Context, c, t, r, m string) error {
	p.s.closeRemoteRun(r)
	port, err := p.original(ctx, c, t, r)
	if err != nil {
		return err
	}
	return port.Stop(ctx, c, t, r, m)
}
func (p taskRouter) Resume(ctx context.Context, c, t, r string) error {
	port, err := p.original(ctx, c, t, r)
	if err != nil {
		return err
	}
	resume, ok := port.(tasks.Resumer)
	if !ok {
		return errors.New("destination resume unavailable")
	}
	return resume.Resume(ctx, c, t, r)
}

func (s *Service) prepareTask(ctx context.Context, r runner.Run) (runner.Prepared, error) {
	l := r.Launch
	if l.ControlID != s.options.RuntimeID {
		return s.prepareRemoteTask(ctx, r)
	}
	resources, revision, err := s.prepareTaskResources(ctx, l, func() (taskResource, error) { return s.currentTaskResource(ctx, l) })
	if err != nil {
		return runner.Prepared{}, err
	}
	config := resources.Config
	policy, _, err := s.Permissions.Policy(l.RuntimeID)
	if err != nil {
		return runner.Prepared{}, err
	}
	if _, err = s.prepareTaskWorkspace(ctx, l, policy); err != nil {
		return runner.Prepared{}, err
	}
	// The Runner independently verifies selected names and its pinned resource digest.
	roots, err := s.installTaskSkills(ctx, resources, l.TaskID, revision, l.ExecutionTarget)
	if err != nil {
		return runner.Prepared{}, err
	}
	execution, err := s.prepareExecution(ctx, l.TaskID, l.ExecutionTarget, true, "")
	if err != nil {
		return runner.Prepared{}, err
	}
	if err = s.addExecutionEnvironment(&execution, l.ExecutionTarget); err != nil {
		return runner.Prepared{}, err
	}
	execution.SkillResources.Roots = append(execution.SkillResources.Roots, roots...)
	execution.SkillResources.Selection = nil
	execution.SkillResources.ReservedNames = resources.ReservedNames
	execution.Role = core.Worker
	addresses, _ := json.Marshal(map[string]protocol.AgentRef{"self": {ControlID: l.ControlID, Kind: "task", AgentID: l.TaskID}, "owner_assistant": {ControlID: l.ControlID, Kind: "assistant", AgentID: l.AssistantID}})
	execution.ContextSections = append(execution.ContextSections, core.ContextSection{Key: "agent-addresses", Text: string(addresses)})
	engine, err := s.host(l.TaskID, l.Generation)
	if err != nil {
		return runner.Prepared{}, err
	}
	dispatch := &Dispatcher{RuntimeID: s.options.RuntimeID, Owner: l.TaskID, Turn: l.RunID, Generation: l.Generation, DefaultTarget: l.ExecutionTarget, ApprovalOwner: l.AssistantID, Task: l.TaskID, Spec: config, Usage: s.Usage, UsageScope: l.TaskID, Permissions: s.Permissions, Operations: s.Operations, Host: engine, Target: s.options.Target, Credentials: s.Integrations, Product: s.productDispatchBound(l.AssistantID, l.TaskID, l.RunID, l.Generation, true)}
	feedback := &taskFeedback{s: s, launch: l}
	model := &ModelSession{Registry: s.Providers, Usage: s.Usage, Spec: config, Scope: l.TaskID, Turn: l.RunID, Generation: l.Generation, Role: "task", Claim: feedback.claimModel}
	ports := &executionPorts{model: model, compaction: func(ctx context.Context, raw json.RawMessage) error {
		return s.taskCompaction(ctx, l.TaskID, l.RunID, raw)
	}}
	definitions := []modelprotocol.ToolDefinition{}
	for _, raw := range core.ExecutionTools(config.Params.Agent.Shell) {
		var d modelprotocol.ToolDefinition
		if err = core.DecodeJSON(raw, &d); err != nil {
			return runner.Prepared{}, err
		}
		definitions = append(definitions, d)
	}
	definitions = append(definitions, s.productTools(true)...)
	loop := &agent.Agent{Services: ports, Feedback: feedback, Dispatch: dispatch.Dispatch, Tools: definitions, Composer: s.options.Composer, Cleanup: func(ctx context.Context, _ core.ExecutionContext, abort bool) error {
		s.Integrations.Forget(credentialScope(l.TaskID, l.RunID, l.Generation))
		s.Permissions.Expire(l.AssistantID, l.RunID, l.Generation)
		result := engine.Drain()
		if s.options.CleanupTarget != nil {
			result = errors.Join(result, s.options.CleanupTarget(ctx, l.TaskID, abort))
		}
		return result
	}}
	return runner.Prepared{Agent: loop, Context: execution, Thread: core.Thread{Owner: l.TaskID, Generation: l.Generation, Config: config}, ResourceDigest: revision}, nil
}

type taskFeedback struct {
	s      *Service
	launch runner.Launch
}

func (f *taskFeedback) current(ctx context.Context) error {
	r, err := f.s.Tasks.Get(ctx, f.launch.AssistantID, f.launch.TaskID)
	if err != nil {
		return err
	}
	if r == nil || r.RunID != f.launch.RunID || r.StopMode != "" || r.Info.Phase.Scope() != "active" {
		return errors.New("Task Control binding is stale")
	}
	return nil
}
func (f *taskFeedback) claimModel(ctx context.Context, id string) error {
	if err := f.current(ctx); err != nil {
		return err
	}
	run, err := f.s.RunnerJournal.Inspect(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
	if err != nil {
		return err
	}
	if run == nil || run.Phase != runner.Running || run.BootID != f.s.options.BootID {
		return errors.New("Task Runner binding is stale")
	}
	var checkpoint agent.Checkpoint
	if err = core.DecodeJSON(run.Context, &checkpoint); err != nil {
		return err
	}
	if checkpoint.ModelRequestID == nil || *checkpoint.ModelRequestID != id {
		return errors.New("Task model request not durably checkpointed")
	}
	return nil
}
func (f *taskFeedback) LoadContext(context.Context) (json.RawMessage, error) { return nil, nil }
func (f *taskFeedback) SaveContext(context.Context, json.RawMessage) error   { return nil }
func (f *taskFeedback) RecordTurn(ctx context.Context, id string) error {
	if id != f.launch.RunID {
		return errors.New("Task turn differs")
	}
	return f.current(ctx)
}
func (f *taskFeedback) InputStreaming(ctx context.Context, id string) error {
	return f.RecordTurn(ctx, id)
}
func (f *taskFeedback) Ready(ctx context.Context) error {
	if err := f.current(ctx); err != nil {
		return err
	}
	return f.s.Tasks.ReadyGate(ctx, f.launch.AssistantID, f.launch.TaskID, f.launch.RunID)
}
func (f *taskFeedback) Steering(ctx context.Context, finalizing bool) ([]agent.SteeringInput, error) {
	return f.s.Tasks.Steering(ctx, f.launch.AssistantID, f.launch.TaskID, f.launch.RunID, finalizing)
}
func (f *taskFeedback) AcknowledgeSteering(ctx context.Context, ids []string) error {
	return f.s.Tasks.AcknowledgeInputs(ctx, f.launch.AssistantID, f.launch.TaskID, f.launch.RunID, ids)
}
func (f *taskFeedback) Observe(ctx context.Context, o agent.Observation) error {
	f.s.Changes.Notify("tasks")
	f.s.Changes.Notify("usage")
	return nil
}

func (s *Service) taskCompaction(ctx context.Context, owner, turn string, raw json.RawMessage) error {
	var report struct {
		ID     string `json:"id"`
		Action string `json:"action"`
		Tokens uint64 `json:"tokens"`
	}
	if err := core.DecodeJSON(raw, &report); err != nil {
		return err
	}
	if report.Tokens > core.MaxSafeInteger {
		return errors.New("compaction token count exceeds precision")
	}
	event := usage.CompactionEvent{ID: report.ID, Action: report.Action}
	tokens := int64(report.Tokens)
	if report.Action == "start" {
		event.Reason = "threshold"
		event.TokensBefore = &tokens
	} else if report.Action == "complete" {
		event.TokensAfter = &tokens
	}
	return s.Usage.Compaction(ctx, owner, turn, event, storage.Now())
}
func (s *Service) resumeTaskAllowance(ctx context.Context) {
	rows, err := s.Tasks.DB().QueryContext(ctx, "SELECT assistant_id,id FROM tasks WHERE phase='waiting' AND json_extract(body,'$.info.remote_waiting')=0 AND json_extract(body,'$.stop_mode')=''")
	if err != nil {
		return
	}
	pairs := [][2]string{}
	for rows.Next() {
		var owner, id string
		if rows.Scan(&owner, &id) == nil {
			pairs = append(pairs, [2]string{owner, id})
		}
	}
	rows.Close()
	for _, pair := range pairs {
		waiting, err := s.Usage.Waiting(ctx, pair[1])
		if err != nil || waiting == nil {
			continue
		}
		ready, err := s.Usage.Ready(ctx, pair[1], storage.Now())
		if err == nil && ready {
			s.Tasks.ResumeWaiting(ctx, pair[0], pair[1])
		}
	}
}
func (f *taskFeedback) Settle(ctx context.Context) error {
	return f.s.settleTaskOperations(ctx, f.launch.TaskID)
}

type taskFiles struct{ s *Service }

func (f taskFiles) ReadResult(ctx context.Context, target core.ExecutionTarget, task, path string, limit int64) (json.RawMessage, error) {
	if target.RuntimeID != f.s.options.RuntimeID {
		var raw []byte
		if err := f.s.Tasks.DB().QueryRowContext(ctx, "SELECT body FROM tasks WHERE id=?", task).Scan(&raw); err != nil {
			return nil, err
		}
		var record tasks.Record
		if err := core.DecodeJSON(raw, &record); err != nil {
			return nil, err
		}
		if record.Launch == nil || record.Launch.ExecutionTarget != target {
			return nil, errors.New("result target differs from original Task")
		}
		peer, err := f.s.Machines.Peer(target.RuntimeID)
		if err != nil {
			return nil, errors.Join(tasks.ErrResultPending, err)
		}
		var reply struct {
			Data string `json:"data"`
		}
		err = f.s.remoteCall(ctx, peer, binding(*record.Launch), "task.file", "file:"+storage.ID(), remoteFile{path, limit}, &reply)
		if err != nil {
			if errors.Is(err, controlplane.ErrUncertain) || errors.Is(err, controlplane.ErrDisconnected) {
				return nil, errors.Join(tasks.ErrResultPending, err)
			}
			return nil, err
		}
		data, err := base64.RawStdEncoding.DecodeString(reply.Data)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("remote Task file exceeds bound")
		}
		return data, nil
	}
	if !protocol.ValidFilePath(path) {
		return nil, errors.New("invalid result path")
	}
	root, err := f.s.openTaskWorkspace(ctx, task)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("Task result exceeds limit")
	}
	return data, ctx.Err()
}

func (s *Service) ReadTaskFile(ctx context.Context, owner, task, path string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 16*1024*1024 {
		return nil, errors.New("file limit outside bounds")
	}
	r, err := s.Tasks.Get(ctx, owner, task)
	if err != nil {
		return nil, err
	}
	if r == nil || r.Launch == nil {
		return nil, errors.New("Task has no fixed execution target")
	}
	return (taskFiles{s}).ReadResult(ctx, r.Launch.ExecutionTarget, task, path, limit)
}
