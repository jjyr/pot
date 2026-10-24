package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/nodeidentity"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
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
}

func (s *Service) openTasks() error {
	var err error
	s.taskLife = &taskLifecycle{wake: make(chan struct{}, 1), watching: map[string]bool{}}
	s.Machines, err = nodeidentity.Open(filepath.Join(s.options.Root, "machines.sqlite"), nodeidentity.Options{RuntimeID: s.options.RuntimeID})
	if err != nil {
		return err
	}
	s.Profiles.ownerAction = s.Machines.OwnerAction
	if err = storage.ImportLocalRuns(s.options.Root); err != nil {
		return err
	}
	if err = storage.ImportTaskSubmissions(s.options.Root); err != nil {
		return err
	}
	s.tasksDB, err = storage.OpenDatabase(filepath.Join(s.options.Root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		return err
	}
	s.RunJournal, err = tasks.OpenJournal(s.tasksDB, filepath.Join(s.options.Root, "cache", "resources"), s.options.RuntimeID, s.options.BootID)
	if err != nil {
		return err
	}
	s.TaskRuns = &tasks.Executor{Journal: s.RunJournal, Executions: s.options.Manager, Context: s.ctx, Prepare: s.prepareTask}
	s.Tasks, err = tasks.Open(s.tasksDB, tasks.Options{OwnerAvailable: s.assistantAvailable, NodeID: s.options.RuntimeID, Executor: s.TaskRuns, ResolveModel: s.selectTask, ValidateRequirements: s.validateTaskRequirements, AdmitModel: s.admitTaskModel, AwaitingApproval: s.taskAwaitingApproval, PublishHook: s.publishHook, OutcomesChanged: func() { s.Changes.Notify("task-outcomes") }, PrepareOutcome: func(ctx context.Context, record tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
		return s.prepareTaskOutcome(ctx, record, event)
	}})
	if err != nil {
		return err
	}
	s.Schedules, err = schedule.New(schedule.Options{Context: s.ctx, Probes: scheduleProbes{s}, Tasks: s.Tasks, Ready: s.taskReady})
	if err != nil {
		return err
	}
	s.Mailbox, err = assistant.OpenMailbox(s.tasksDB, s.options.RuntimeID, assistant.MailboxOptions{OwnerAvailable: s.assistantAvailable})
	if err != nil {
		return err
	}
	return nil
}
func (s *Service) assistantAvailable(ctx context.Context, owner string) error {
	deleted, err := s.Registry.Deleted(ctx, owner)
	if err != nil {
		return err
	}
	if deleted {
		return protocol.APIError{Code: "assistant_unavailable"}
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
	if deleted, err := s.Registry.Deleted(ctx, owner); err != nil || deleted {
		return false, err
	}
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
func (s *Service) taskLoop() {
	defer s.taskLife.wg.Done()
	sub := s.Tasks.Subscribe()
	defer sub.Close()
	runSub := s.RunJournal.Subscribe()
	defer runSub.Close()
	mailSub := s.Mailbox.Changes.Subscribe()
	defer mailSub.Close()
	policySub := s.Permissions.Changes.Subscribe()
	defer policySub.Close()
	for _, source := range []*core.Subscription{sub, runSub, policySub, mailSub} {
		s.taskLife.wg.Add(1)
		go func(source *core.Subscription) {
			defer s.taskLife.wg.Done()
			for {
				if _, err := source.Next(s.ctx); err != nil {
					return
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

func (s *Service) prepareTask(ctx context.Context, r tasks.Run) (tasks.Prepared, error) {
	l := r.Launch
	if l.ControlID != s.options.RuntimeID {
		return tasks.Prepared{}, protocol.APIError{Code: "original_execution_unavailable"}
	}
	resources, revision, err := s.prepareTaskResources(ctx, l)
	if err != nil {
		return tasks.Prepared{}, err
	}
	config := resources.Config
	policy, _, err := s.Permissions.Policy(l.RuntimeID)
	if err != nil {
		return tasks.Prepared{}, err
	}
	if _, err = s.prepareTaskWorkspace(ctx, l, policy); err != nil {
		return tasks.Prepared{}, err
	}
	// Task execution verifies selected names and its pinned resource digest.

	execution, err := s.prepareExecution(ctx, l.TaskID, l.ExecutionTarget, true, "")
	if err != nil {
		return tasks.Prepared{}, err
	}
	if err = s.addExecutionEnvironment(&execution, l.ExecutionTarget); err != nil {
		return tasks.Prepared{}, err
	}
	execution.SkillResources.Roots = append(execution.SkillResources.Roots, resources.Roots...)
	execution.SkillResources.Selection = nil
	execution.SkillResources.ReservedNames = resources.ReservedNames
	execution.Role = core.Worker
	addresses, _ := json.Marshal(map[string]protocol.AgentRef{"self": {NodeID: l.ControlID, Kind: "task", AgentID: l.TaskID}, "owner_assistant": {NodeID: l.ControlID, Kind: "assistant", AgentID: l.AssistantID}})
	execution.ContextSections = append(execution.ContextSections, core.ContextSection{Key: "agent-addresses", Text: string(addresses)})
	engine, err := s.host(l.TaskID, l.Generation)
	if err != nil {
		return tasks.Prepared{}, err
	}
	dispatch := &Dispatcher{RuntimeID: s.options.RuntimeID, Owner: l.TaskID, Turn: l.RunID, Generation: l.Generation, DefaultTarget: l.ExecutionTarget, ApprovalOwner: l.AssistantID, Task: l.TaskID, Spec: config, Usage: s.Usage, UsageScope: l.TaskID, Permissions: s.Permissions, Operations: s.Operations, Host: engine, Credentials: s, Product: s.productDispatchBound(l.AssistantID, l.TaskID, l.RunID, l.Generation, true)}
	feedback := &taskFeedback{s: s, launch: l}
	model := &ModelSession{Registry: s.Providers, Usage: s.Usage, Spec: config, Scope: l.TaskID, Turn: l.RunID, Generation: l.Generation, Role: "task", Claim: feedback.claimModel}
	ports := &executionPorts{model: model, compaction: func(ctx context.Context, raw json.RawMessage) error {
		return s.taskCompaction(ctx, l.TaskID, l.RunID, raw)
	}}
	definitions := []modelprotocol.ToolDefinition{}
	for _, raw := range core.ExecutionTools(config.Params.Agent.Shell) {
		var d modelprotocol.ToolDefinition
		if err = core.DecodeJSON(raw, &d); err != nil {
			return tasks.Prepared{}, err
		}
		definitions = append(definitions, d)
	}
	definitions = append(definitions, s.productTools(true)...)
	mcpTools, err := s.pluginMCPTools(ctx, dispatch.Product)
	if err != nil {
		return tasks.Prepared{}, err
	}
	definitions = append(definitions, mcpTools...)
	loop := &agent.Agent{Services: ports, Feedback: feedback, Dispatch: dispatch.Dispatch, Tools: definitions, Composer: s.options.Composer, Cleanup: func(ctx context.Context, _ core.ExecutionContext, abort bool) error {
		s.Integrations.Forget(credentialScope(l.TaskID, l.RunID, l.Generation))
		s.Permissions.Expire(l.AssistantID, l.RunID, l.Generation)
		result := engine.Drain()
		if s.options.CleanupTarget != nil {
			result = errors.Join(result, s.options.CleanupTarget(ctx, l.TaskID, abort))
		}
		return result
	}}
	return tasks.Prepared{MessageCompleted: s.messageFileCompletion(l.AssistantID, l.TaskID, l.RunID, execution.Workspace), Agent: loop, Context: execution, Thread: core.Thread{Owner: l.TaskID, Generation: l.Generation, Config: config}, ResourceDigest: revision}, nil
}

type taskFeedback struct {
	s      *Service
	launch tasks.Launch
}

func (f *taskFeedback) current(ctx context.Context) error {
	r, err := f.s.Tasks.Get(ctx, f.launch.AssistantID, f.launch.TaskID)
	if err != nil {
		return err
	}
	if r == nil || r.RunID != f.launch.RunID || r.StopMode != "" || r.Info.Phase.Scope() != "active" {
		return errors.New("Task Control binding is stale")
	}
	run, err := f.s.RunJournal.Inspect(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
	if err != nil {
		return err
	}
	if run == nil || run.Phase != tasks.Running || run.BootID != f.s.options.BootID || run.StopRequested != "" {
		return errors.New("task execution is fenced")
	}

	return nil
}
func (f *taskFeedback) claimModel(ctx context.Context, id string) error {
	if err := f.current(ctx); err != nil {
		return err
	}
	run, err := f.s.RunJournal.Inspect(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
	if err != nil {
		return err
	}
	if run == nil || run.Phase != tasks.Running || run.BootID != f.s.options.BootID {
		return errors.New("local task run binding is stale")
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
func (f *taskFeedback) LoadContext(ctx context.Context) (json.RawMessage, error) {
	if err := f.current(ctx); err != nil {
		return nil, err
	}
	r, err := f.s.RunJournal.Inspect(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
	if err != nil {
		return nil, err
	}
	return r.Context, nil
}
func (f *taskFeedback) SaveContext(ctx context.Context, raw json.RawMessage) error {
	return f.s.RunJournal.SaveContext(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID, raw)
}
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
	return s.recordCompaction(ctx, owner, turn, raw)
}
func (s *Service) resumeTaskAllowance(ctx context.Context) {
	active, err := s.Tasks.LocalActive(ctx)
	if err != nil {
		return
	}
	for _, task := range active {
		if task.Phase != protocol.TaskWaiting || task.RemoteWaiting {
			continue
		}
		waiting, err := s.Usage.Waiting(ctx, task.ID)
		if err != nil || waiting == nil {
			continue
		}
		ready, err := s.Usage.Ready(ctx, task.ID, storage.Now())
		if err == nil && ready {
			_ = s.Tasks.ResumeWaiting(ctx, task.AssistantID, task.ID)
		}
	}
}

func (f *taskFeedback) Settle(ctx context.Context) error {
	if err := f.s.settleTaskOperations(ctx, f.launch.TaskID); err != nil {
		return err
	}
	return f.s.RunJournal.Settle(ctx, f.launch.ControlID, f.launch.TaskID, f.launch.RunID)
}

type taskFiles struct{ s *Service }

func (f taskFiles) ReadFile(ctx context.Context, target core.ExecutionTarget, task, path string, limit int64) ([]byte, error) {
	if target.RuntimeID != f.s.options.RuntimeID {
		return nil, protocol.APIError{Code: "original_execution_unavailable"}
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
	return (taskFiles{s}).ReadFile(ctx, r.Launch.ExecutionTarget, task, path, limit)
}
