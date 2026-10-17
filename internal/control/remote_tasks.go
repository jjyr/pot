package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type taskBinding struct {
	ControlID   string `json:"control_id"`
	AssistantID string `json:"assistant_id"`
	TaskID      string `json:"task_id"`
	RunID       string `json:"run_id"`
	Generation  uint64 `json:"generation"`
}

func binding(l runner.Launch) taskBinding {
	return taskBinding{l.ControlID, l.AssistantID, l.TaskID, l.RunID, l.Generation}
}

type remoteRequest struct {
	Binding taskBinding     `json:"binding" required:"true"`
	Data    json.RawMessage `json:"data" required:"true"`
}
type remoteState struct {
	mu            sync.Mutex
	runs          map[string]*remoteAuthority
	runnerStarted bool
	prepared      map[string]remotePrepared
}
type remotePrepared struct {
	launch    runner.Launch
	execution core.ExecutionContext
	engine    ToolEngine
	emits     map[string]func(core.Update) error
}
type remoteAuthority struct {
	mu      sync.Mutex
	model   *ModelSession
	streams map[string]*modelStream
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
}
type modelStream struct {
	mu       sync.Mutex
	body     io.ReadCloser
	sequence uint64
	ended    bool
}

func (s *Service) remote() *remoteState {
	s.taskLife.mu.Lock()
	defer s.taskLife.mu.Unlock()
	if s.taskLife.remote == nil {
		s.taskLife.remote = &remoteState{runs: map[string]*remoteAuthority{}, prepared: map[string]remotePrepared{}}
	}
	return s.taskLife.remote
}
func (s *Service) ControlServer(owner http.Handler) http.Handler {
	return &controlplane.Server{Directory: s.Machines, Handler: s.controlRPC, OwnerHandler: owner, OnEnroll: s.enrollRemotePolicy, OnPresence: func(string, bool) { s.Changes.Notify("machines"); s.wakeTasks() }}
}
func (s *Service) enrollRemotePolicy(ctx context.Context, m controlplane.Machine) error {
	epoch := "member-" + strconv.FormatUint(m.Epoch, 10)
	_, old, err := s.Permissions.Policy(m.Identity.RuntimeID)
	if err == nil && old == epoch {
		return nil
	}
	return s.Permissions.Enroll(m.Identity.RuntimeID, epoch)
}
func (s *Service) RunnerConnect(ctx context.Context) (*controlplane.Peer, error) {
	connector := controlplane.Connector{Directory: s.Machines, HTTPClient: s.options.ControlHTTPClient, Handler: controlplane.RunnerHandler(s.Machines, remoteRunnerPort{s}, s.runnerRPC)}
	return connector.Connect(ctx)
}

// RunnerServe reconnects the outward session only. Agent/command/model requests
// whose acknowledgments were lost remain fenced in their original journals.
func (s *Service) RunnerServe(ctx context.Context) error {
	remote := s.remote()
	remote.mu.Lock()
	if remote.runnerStarted {
		remote.mu.Unlock()
		return nil
	}
	remote.runnerStarted = true
	remote.mu.Unlock()
	defer func() { remote.mu.Lock(); remote.runnerStarted = false; remote.mu.Unlock() }()
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.Machines.Role() != "runner" {
			return nil
		}
		peer, err := s.RunnerConnect(ctx)
		if err == nil {
			delay = time.Second
			select {
			case <-ctx.Done():
				peer.Close()
				return ctx.Err()
			case <-peer.Done():
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
func (s *Service) StartRunner() {
	if s.taskLife == nil {
		return
	}
	s.taskLife.wg.Add(1)
	go func() { defer s.taskLife.wg.Done(); _ = s.RunnerServe(s.ctx) }()
}
func (s *Service) remoteCall(ctx context.Context, peer *controlplane.Peer, b taskBinding, method, id string, data any, out any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	params, err := json.Marshal(remoteRequest{b, raw})
	if err != nil {
		return err
	}
	reply, err := peer.Call(ctx, controlplane.Request{ID: id, Method: method, Params: params})
	if err != nil {
		return err
	}
	if out != nil {
		return core.DecodeJSON(reply, out)
	}
	return nil
}
func decodeRemote(raw json.RawMessage) (remoteRequest, error) {
	var r remoteRequest
	err := core.DecodeJSON(raw, &r)
	if err == nil {
		b := r.Binding
		if !core.Identifier(b.ControlID) || !core.Identifier(b.AssistantID) || !core.Identifier(b.TaskID) || !core.Identifier(b.RunID) || b.Generation == 0 {
			err = errors.New("invalid run-bound callback")
		}
	}
	return r, err
}
func (s *Service) controlBinding(ctx context.Context, p controlplane.Principal, b taskBinding, allowStopped bool) (runner.Launch, error) {
	if p.Owner || b.ControlID != s.options.RuntimeID {
		return runner.Launch{}, controlplane.ErrUnauthorized
	}
	r, err := s.Tasks.Get(ctx, b.AssistantID, b.TaskID)
	if err != nil {
		return runner.Launch{}, err
	}
	if r == nil || r.Launch == nil || r.RunID != b.RunID || r.Generation != b.Generation || r.Launch.RuntimeID != p.RuntimeID {
		return runner.Launch{}, controlplane.ErrUnauthorized
	}
	if !allowStopped && (r.StopMode != "" || r.Info.Phase.Scope() != "active") {
		return runner.Launch{}, errors.New("original Task no longer admits effects")
	}
	return *r.Launch, nil
}
func (s *Service) runnerBinding(ctx context.Context, p controlplane.Principal, b taskBinding, allowStopped bool) (runner.Run, error) {
	m := s.Machines.Membership()
	if m == nil || p.Owner || p.RuntimeID != m.Enrollment.Control.RuntimeID || p.Epoch != m.Enrollment.Epoch || b.ControlID != p.RuntimeID {
		return runner.Run{}, controlplane.ErrUnauthorized
	}
	r, err := s.RunnerJournal.Inspect(ctx, b.ControlID, b.TaskID, b.RunID)
	if err != nil {
		return runner.Run{}, err
	}
	if r == nil || r.Launch.AssistantID != b.AssistantID || r.Launch.Generation != b.Generation || r.Launch.RuntimeID != s.options.RuntimeID {
		return runner.Run{}, controlplane.ErrUnauthorized
	}
	if !allowStopped && (r.Phase != runner.Running || r.BootID != s.options.BootID || r.StopRequested != "" || r.Finalizing) {
		return runner.Run{}, errors.New("original Runner lease no longer admits effects")
	}
	return *r, nil
}
func (s *Service) controlRPC(ctx context.Context, p controlplane.Principal, request controlplane.Request) (json.RawMessage, error) {
	if request.Method == "event.runner.changed" {
		s.wakeTasks()
		s.Changes.Notify("tasks")
		return json.RawMessage("null"), nil
	}
	r, err := decodeRemote(request.Params)
	if err != nil {
		return nil, err
	}
	allowStopped := request.Method == "task.model.close" || request.Method == "task.settle" || request.Method == "task.recovery.operations" || request.Method == "task.recovery.settle"
	launch, err := s.controlBinding(ctx, p, r.Binding, allowStopped)
	if err != nil {
		return nil, err
	}
	peer, err := s.Machines.Peer(p.RuntimeID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	var result any
	switch request.Method {
	case "task.recovery.operations":
		result, err = s.Operations.RecoveryRecords(ctx, launch.TaskID, launch.RunID)
	case "task.recovery.settle":
		var records []OperationRecord
		if err = core.DecodeJSON(r.Data, &records); err == nil {
			err = s.settleRecoveryOperations(ctx, launch, records)
		}
		result = map[string]bool{"settled": err == nil}
	case "task.resources":
		result, err = s.currentTaskResource(ctx, launch)
	case "task.ready":
		err = s.Tasks.ReadyGate(ctx, launch.AssistantID, launch.TaskID, launch.RunID)
		result = map[string]bool{"ready": err == nil}
	case "task.steering":
		var q struct {
			Finalizing bool `json:"finalizing"`
		}
		if err = core.DecodeJSON(r.Data, &q); err == nil {
			result, err = s.Tasks.Steering(ctx, launch.AssistantID, launch.TaskID, launch.RunID, q.Finalizing)
		}
	case "task.ack":
		var ids []string
		if err = core.DecodeJSON(r.Data, &ids); err == nil {
			var checkpoint agent.Checkpoint
			err = s.remoteCheckpoint(ctx, peer, launch, &checkpoint)
			if err == nil {
				for _, id := range ids {
					found := false
					for _, saved := range checkpoint.SteeringIDs {
						if saved == id {
							found = true
							break
						}
					}
					if !found {
						return nil, errors.New("remote steering not durably checkpointed")
					}
				}
				err = s.Tasks.AcknowledgeInputs(ctx, launch.AssistantID, launch.TaskID, launch.RunID, ids)
			}
		}
		result = map[string]bool{"accepted": err == nil}
	case "task.observe":
		var observation agent.Observation
		err = core.DecodeJSON(r.Data, &observation)
		if err == nil {
			s.Changes.Notify("tasks")
			s.Changes.Notify("usage")
		}
		result = map[string]bool{"accepted": err == nil}
	case "task.compaction":
		err = s.taskCompaction(ctx, launch.TaskID, launch.RunID, r.Data)
		result = map[string]bool{"accepted": err == nil}
	case "task.model.open":
		var modelRequest core.ModelRequest
		if err = core.DecodeJSON(r.Data, &modelRequest); err != nil {
			return nil, err
		}
		authority, err := s.authority(ctx, peer, launch)
		if err != nil {
			return nil, err
		}
		authority.mu.Lock()
		if authority.closed || len(authority.streams) >= 4 || authority.streams[modelRequest.ID] != nil {
			authority.mu.Unlock()
			return nil, errors.New("model stream admission conflict")
		}
		authority.streams[modelRequest.ID] = &modelStream{}
		authority.mu.Unlock()
		body, err := authority.model.Model(authority.ctx, modelRequest)
		authority.mu.Lock()
		stream := authority.streams[modelRequest.ID]
		if err != nil {
			delete(authority.streams, modelRequest.ID)
			authority.mu.Unlock()
			return nil, err
		}
		if authority.closed {
			authority.mu.Unlock()
			body.Close()
			return nil, errors.New("Task model was stopped")
		}
		stream.body = body
		authority.mu.Unlock()
		result = map[string]string{"stream_id": modelRequest.ID}
	case "task.model.read":
		var q streamRead
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		authority, err := s.authority(ctx, peer, launch)
		if err != nil {
			return nil, err
		}
		authority.mu.Lock()
		stream := authority.streams[q.ID]
		authority.mu.Unlock()
		if stream == nil {
			return nil, errors.New("original model stream missing")
		}
		stream.mu.Lock()
		if stream.body == nil || stream.ended || q.Sequence != stream.sequence {
			stream.mu.Unlock()
			return nil, errors.New("model stream sequence conflict")
		}
		stream.sequence++
		buffer := make([]byte, 64*1024)
		n, readErr := stream.body.Read(buffer)
		reply := streamChunk{Data: base64.RawStdEncoding.EncodeToString(buffer[:n])}
		if readErr != nil {
			stream.ended = true
			reply.EOF = errors.Is(readErr, io.EOF)
			if !reply.EOF {
				reply.Error = readErr.Error()
			}
			stream.body.Close()
		}
		stream.mu.Unlock()
		result = reply
	case "task.model.close":
		var q struct {
			ID string `json:"id"`
		}
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		state := s.remote()
		state.mu.Lock()
		authority := state.runs[launch.RunID]
		state.mu.Unlock()
		if authority != nil {
			authority.mu.Lock()
			stream := authority.streams[q.ID]
			delete(authority.streams, q.ID)
			authority.mu.Unlock()
			if stream != nil && stream.body != nil {
				stream.body.Close()
			}
		}
		result = map[string]bool{"closed": true}
	case "task.dispatch":
		var q remoteDispatch
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		if _, err = runner.BindTarget(q.Call.Arguments, launch.ExecutionTarget); err != nil {
			return nil, err
		}
		authority, err := s.authority(ctx, peer, launch)
		if err != nil {
			return nil, err
		}
		stopDispatch := context.AfterFunc(authority.ctx, cancel)
		defer stopDispatch()
		dispatcher := &Dispatcher{RuntimeID: s.options.RuntimeID, Owner: launch.TaskID, ApprovalOwner: launch.AssistantID, Task: launch.TaskID, Turn: launch.RunID, Generation: launch.Generation, DefaultTarget: launch.ExecutionTarget, Spec: authority.model.Spec, Credentials: s.Integrations, Usage: s.Usage, UsageScope: launch.TaskID, Permissions: s.Permissions, Operations: s.Operations, Target: func(ctx context.Context, operation Operation, execution executor.Request, emit func(core.Update) error) (json.RawMessage, error) {
			var response executeReply
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			settle, done := settlementContext(ctx)
			defer done()
			err := s.remoteCall(settle, peer, binding(launch), "task.execute", "execute:"+launch.RunID+":"+execution.ID, remoteExecute{operation, execution}, &response)
			if err != nil {
				if errors.Is(err, controlplane.ErrUncertain) {
					return nil, errors.Join(core.ErrCleanupUnconfirmed, err)
				}
				return nil, err
			}
			if response.CleanupUnconfirmed {
				return nil, core.ErrCleanupUnconfirmed
			}
			if response.Error != "" {
				return response.Value, errors.New(response.Error)
			}
			return response.Value, nil
		}}
		sequence := 0
		raw, err := dispatcher.Dispatch(ctx, core.ExecutionContext{Owner: launch.TaskID, Role: core.Worker, Workspace: q.Workspace}, q.Call, func(update core.Update) error {
			sequence++
			return s.remoteCall(ctx, peer, binding(launch), "task.tool_update", fmt.Sprintf("tool-update:%s:%s:%d", launch.RunID, q.Call.ID, sequence), update, nil)
		})
		return raw, err
	case "task.settle":
		s.Permissions.Expire(launch.AssistantID, launch.RunID, launch.Generation)
		err = s.settleTaskOperations(ctx, launch.TaskID)
		result = map[string]bool{"settled": err == nil}
	default:
		return nil, errors.New("unknown Control Task callback")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func (s *Service) authority(ctx context.Context, peer *controlplane.Peer, l runner.Launch) (*remoteAuthority, error) {
	state := s.remote()
	state.mu.Lock()
	defer state.mu.Unlock()
	if a := state.runs[l.RunID]; a != nil && a.ctx.Err() == nil {
		return a, nil
	}
	var resources taskResource
	if err := s.remoteCall(ctx, peer, binding(l), "task.prepared.resources", "prepared:"+storage.ID(), nil, &resources); err != nil {
		return nil, err
	}
	if err := validateTaskResource(l, resources); err != nil {
		return nil, err
	}
	runctx, cancel := context.WithCancel(s.ctx)
	context.AfterFunc(runctx, func() { s.Integrations.Forget(credentialScope(l.TaskID, l.RunID, l.Generation)) })
	a := &remoteAuthority{streams: map[string]*modelStream{}, ctx: runctx, cancel: cancel}
	go func() {
		select {
		case <-peer.Done():
			cancel()
		case <-runctx.Done():
		}
	}()
	a.model = &ModelSession{Registry: s.Providers, Usage: s.Usage, Spec: resources.Config, Scope: l.TaskID, Turn: l.RunID, Generation: l.Generation, Role: "task", Claim: func(ctx context.Context, id string) error {
		if _, err := s.controlBinding(ctx, controlplane.Principal{RuntimeID: l.RuntimeID}, binding(l), false); err != nil {
			return err
		}
		var checkpoint agent.Checkpoint
		if err := s.remoteCheckpoint(ctx, peer, l, &checkpoint); err != nil {
			return err
		}
		if checkpoint.ModelRequestID == nil || *checkpoint.ModelRequestID != id {
			return errors.New("remote model request not checkpointed")
		}
		return nil
	}}
	state.runs[l.RunID] = a
	return a, nil
}
func (s *Service) remoteCheckpoint(ctx context.Context, peer *controlplane.Peer, l runner.Launch, out *agent.Checkpoint) error {
	return s.remoteCall(ctx, peer, binding(l), "task.checkpoint", "checkpoint:"+storage.ID(), nil, out)
}

type streamRead struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
}
type streamChunk struct {
	Data  string `json:"data"`
	EOF   bool   `json:"eof"`
	Error string `json:"error"`
}
type remoteDispatch struct {
	Call      modelprotocol.ToolCall `json:"call"`
	Workspace string                 `json:"workspace"`
}
type remoteExecute struct {
	Operation Operation        `json:"operation"`
	Request   executor.Request `json:"request"`
}
type executeReply struct {
	Value              json.RawMessage `json:"value"`
	Error              string          `json:"error"`
	CleanupUnconfirmed bool            `json:"cleanup_unconfirmed"`
}

func (s *Service) runnerRPC(ctx context.Context, p controlplane.Principal, request controlplane.Request) (json.RawMessage, error) {
	if request.Method == "machine.inventory" {
		m := s.Machines.Membership()
		if m == nil || p.RuntimeID != m.Enrollment.Control.RuntimeID || p.Epoch != m.Enrollment.Epoch {
			return nil, controlplane.ErrUnauthorized
		}

		value, err := s.localMachine(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	r, err := decodeRemote(request.Params)
	if err != nil {
		return nil, err
	}
	record, err := s.runnerBinding(ctx, p, r.Binding, request.Method == "task.recovery" || request.Method == "task.checkpoint" || request.Method == "task.file" || request.Method == "task.file.chunk" || request.Method == "task.events")
	if err != nil {
		return nil, err
	}
	switch request.Method {
	case "task.recovery":
		var q struct {
			Action string `json:"action"`
		}
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		if err = recoveryAction(q.Action); err != nil {
			return nil, err
		}
		snapshot, recoveryErr := s.recoverRun(ctx, record, q.Action)
		if recoveryErr != nil {
			return nil, recoveryErr
		}
		return json.Marshal(snapshot)
	case "task.prepared.resources":
		resources, _, err := s.readPreparedTaskResources(ctx, record.Launch)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resources)
	case "task.checkpoint":
		if len(record.Context) == 0 {
			return nil, errors.New("Task checkpoint missing")
		}
		return record.Context, nil
	case "task.events":
		var q struct {
			After uint64 `json:"after"`
			Limit uint64 `json:"limit"`
		}
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		page, err := s.RunnerJournal.Events(ctx, record.Launch.ControlID, record.Launch.TaskID, record.Launch.RunID, q.After, q.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	case "task.file.chunk":
		var q protocol.FileQuery
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		chunk, err := s.localTaskFileChunk(ctx, record.Launch.ExecutionTarget, record.Launch.TaskID, q.Path, q.Offset, q.Revision)
		if err != nil {
			return nil, err
		}
		return json.Marshal(chunk)
	case "task.file":
		var q remoteFile
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		if q.Limit < 1 || q.Limit > 16*1024*1024 {
			return nil, errors.New("Task file limit outside bounds")
		}
		data, err := (taskFiles{s}).ReadResult(ctx, record.Launch.ExecutionTarget, record.Launch.TaskID, q.Path, q.Limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Data string `json:"data"`
		}{base64.RawStdEncoding.EncodeToString(data)})
	case "task.tool_update":
		var update core.Update
		if err := core.DecodeJSON(r.Data, &update); err != nil {
			return nil, err
		}
		if update.Type != "tool_state" || update.Target != nil && *update.Target != record.Launch.ExecutionTarget {
			return nil, controlplane.ErrUnauthorized
		}
		switch update.Phase {
		case "preparing", "waiting_approval", "running", "denied":
		default:
			return nil, controlplane.ErrUnauthorized
		}
		state := s.remote()
		state.mu.Lock()
		prepared := state.prepared[record.Launch.RunID]
		emit := prepared.emits[update.ID]
		state.mu.Unlock()
		if emit == nil {
			return nil, errors.New("tool update has no active original call")
		}
		if err := emit(update); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"accepted": true})
	case "task.execute":
		var q remoteExecute
		if err = core.DecodeJSON(r.Data, &q); err != nil {
			return nil, err
		}
		operation := q.Operation
		if operation.Owner != record.Launch.TaskID || operation.Turn != record.Launch.RunID || operation.Generation != record.Launch.Generation || operation.Target != record.Launch.ExecutionTarget || !operation.Worker || q.Request.ID != operation.CallID {
			return nil, controlplane.ErrUnauthorized
		}
		state := s.remote()
		state.mu.Lock()
		prepared, ok := state.prepared[record.Launch.RunID]
		state.mu.Unlock()
		if !ok || q.Request.CWD != prepared.execution.Workspace {
			return nil, errors.New("Task fixed execution namespace differs")
		}
		resources, _, err := s.readPreparedTaskResources(ctx, record.Launch)
		if err != nil {
			return nil, err
		}
		policyHash, _ := core.HashJSON(q.Request.Policy)
		expectedPolicy, _ := core.HashJSON(resources.Config.Params.Agent.Shell)
		if policyHash != expectedPolicy {
			return nil, errors.New("approved execution shell policy differs")
		}
		var arguments map[string]any
		if err = core.DecodeJSON(operation.Args, &arguments); err != nil {
			return nil, err
		}
		checker := &Dispatcher{Spec: resources.Config}
		name, args, err := checker.prepare(operation.Tool, arguments, operation.Target)
		if err != nil {
			return nil, err
		}
		actual, _ := core.HashJSON(q.Request.Args)
		expected, _ := core.HashJSON(args)
		if name != q.Request.Operation || actual != expected {
			return nil, errors.New("execution differs from authorized operation")
		}
		// Only nonsecret request fields participate in durable command receipts.
		durable := q
		durable.Request.Environment = nil
		durableData, err := json.Marshal(durable)
		if err != nil {
			return nil, err
		}
		receipt, claim, err := s.RunnerJournal.BeginCommand(ctx, record.Launch.ControlID, record.Launch.TaskID, record.Launch.RunID, operation.CallID, durableData)
		if err != nil {
			return nil, err
		}
		if !claim {
			if receipt.Phase != "complete" {
				return nil, core.ErrCleanupUnconfirmed
			}
			return receipt.Reply, nil
		}
		var value json.RawMessage

		emit := func(event executor.Event) error {
			state.mu.Lock()
			callback := prepared.emits[operation.CallID]
			state.mu.Unlock()
			if callback == nil {
				return errors.New("tool output has no active original call")
			}
			return callback(core.Update{Type: "tool_output", ID: operation.CallID, Stream: event.ToolOutput.Stream, Text: event.ToolOutput.Text})
		}
		result, runErr := prepared.engine.Execute(ctx, q.Request, emit)
		err = runErr
		if result != nil {
			value, _ = json.Marshal(result)
		}

		if len(value) == 0 {
			value = json.RawMessage("null")
		}
		reply := executeReply{Value: value}
		if err != nil {
			reply.Error = err.Error()
			reply.CleanupUnconfirmed = core.IsCleanupUnconfirmed(err)
		}
		raw, marshalErr := json.Marshal(reply)
		if marshalErr != nil {
			return nil, marshalErr
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if reply.CleanupUnconfirmed {
			_ = s.RunnerJournal.MarkCommandUnknown(cleanup, record.Launch.ControlID, record.Launch.TaskID, record.Launch.RunID, operation.CallID)
			return nil, core.ErrCleanupUnconfirmed
		}
		if err = s.RunnerJournal.CompleteCommand(cleanup, record.Launch.ControlID, record.Launch.TaskID, record.Launch.RunID, operation.CallID, raw); err != nil {
			return nil, errors.Join(core.ErrCleanupUnconfirmed, err)
		}
		return raw, nil
	default:
		return nil, errors.New("unknown destination Task callback")
	}
}

type remoteFile struct {
	Path  string `json:"path"`
	Limit int64  `json:"limit"`
}

func (s *Service) prepareRemoteTask(ctx context.Context, r runner.Run) (runner.Prepared, error) {
	peer, err := s.Machines.ControlPeer()
	if err != nil {
		return runner.Prepared{}, err
	}
	l := r.Launch
	resources, revision, err := s.prepareTaskResources(ctx, l, func() (taskResource, error) {
		var resources taskResource
		err := s.remoteCall(ctx, peer, binding(l), "task.resources", "resources:"+l.RunID, nil, &resources)
		return resources, err
	})
	if err != nil {
		return runner.Prepared{}, err
	}
	roots, err := s.installTaskSkills(ctx, resources, l.TaskID, revision, l.ExecutionTarget)
	if err != nil {
		return runner.Prepared{}, err
	}
	execution, err := s.prepareExecution(ctx, l.TaskID, l.ExecutionTarget, true, "")
	if err != nil {
		return runner.Prepared{}, err
	}
	execution.Role = core.Worker
	execution.SkillResources.Roots = append(execution.SkillResources.Roots, roots...)
	execution.SkillResources.Selection = nil
	execution.SkillResources.ReservedNames = resources.ReservedNames
	engine, err := s.host(l.TaskID, l.Generation)
	if err != nil {
		return runner.Prepared{}, err
	}
	state := s.remote()
	state.mu.Lock()
	state.prepared[l.RunID] = remotePrepared{launch: l, execution: execution, engine: engine, emits: map[string]func(core.Update) error{}}
	state.mu.Unlock()
	services := &remoteServices{s: s, peer: peer, launch: l}
	feedback := &remoteFeedback{services: services}
	tools := []modelprotocol.ToolDefinition{}
	for _, raw := range core.ExecutionTools(resources.Config.Params.Agent.Shell) {
		var d modelprotocol.ToolDefinition
		if err = core.DecodeJSON(raw, &d); err != nil {
			return runner.Prepared{}, err
		}
		tools = append(tools, d)
	}
	loop := &agent.Agent{Services: services, Feedback: feedback, Tools: tools, Composer: s.options.Composer, Dispatch: func(ctx context.Context, e core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
		state := s.remote()
		state.mu.Lock()
		prepared, ok := state.prepared[l.RunID]
		if ok {
			prepared.emits[call.ID] = emit
		}
		state.mu.Unlock()
		if !ok {
			return nil, errors.New("Task original prepared execution unavailable")
		}
		defer func() { state.mu.Lock(); delete(prepared.emits, call.ID); state.mu.Unlock() }()
		var result json.RawMessage
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		settle, done := settlementContext(ctx)
		defer done()
		err := s.remoteCall(settle, peer, binding(l), "task.dispatch", "dispatch:"+l.RunID+":"+call.ID, remoteDispatch{call, e.Workspace}, &result)
		if errors.Is(err, controlplane.ErrUncertain) {
			return nil, errors.Join(core.ErrCleanupUnconfirmed, err)
		}
		return result, err
	}, Cleanup: func(ctx context.Context, _ core.ExecutionContext, abort bool) error {
		err := engine.Drain()
		if s.options.CleanupTarget != nil {
			err = errors.Join(err, s.options.CleanupTarget(ctx, l.TaskID, abort))
		}
		state.mu.Lock()
		delete(state.prepared, l.RunID)
		state.mu.Unlock()
		return err
	}}
	return runner.Prepared{Agent: loop, Context: execution, Thread: core.Thread{Owner: l.TaskID, Generation: l.Generation, Config: resources.Config}, ResourceDigest: revision}, nil
}

type remoteServices struct {
	s      *Service
	peer   *controlplane.Peer
	launch runner.Launch
}

func (p *remoteServices) call(ctx context.Context, method, id string, data any, out any) error {
	return p.s.remoteCall(ctx, p.peer, binding(p.launch), method, id, data, out)
}
func (p *remoteServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	var reply struct {
		ID string `json:"stream_id"`
	}
	if err := p.call(ctx, "task.model.open", "model:"+r.ID, r, &reply); err != nil {
		return nil, err
	}
	if reply.ID != r.ID {
		return nil, errors.New("model stream identity changed")
	}
	return &remoteReader{services: p, ctx: ctx, id: r.ID}, nil
}
func (p *remoteServices) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, errors.New("Task RPC unavailable")
}
func (p *remoteServices) Compaction(ctx context.Context, raw json.RawMessage) error {
	return p.call(ctx, "task.compaction", "compaction:"+storage.ID(), raw, nil)
}

type remoteReader struct {
	services *remoteServices
	ctx      context.Context
	id       string
	sequence uint64
	buffer   []byte
	terminal error
	closed   sync.Once
}

func (r *remoteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.buffer) == 0 && r.terminal == nil {
		var chunk streamChunk
		err := r.services.call(r.ctx, "task.model.read", "read:"+r.id+":"+strconv.FormatUint(r.sequence, 10), streamRead{r.id, r.sequence}, &chunk)
		r.sequence++
		if err != nil {
			r.terminal = err
			break
		}
		r.buffer, err = base64.RawStdEncoding.DecodeString(chunk.Data)
		if err != nil {
			r.terminal = err
			break
		}
		if chunk.EOF {
			r.terminal = io.EOF
		} else if chunk.Error != "" {
			r.terminal = errors.New(chunk.Error)
		}
	}
	if len(r.buffer) > 0 {
		n := copy(p, r.buffer)
		r.buffer = r.buffer[n:]
		return n, nil
	}
	return 0, r.terminal
}
func (r *remoteReader) Close() error {
	var err error
	r.closed.Do(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
		defer cancel()
		err = r.services.call(ctx, "task.model.close", "close:"+r.id, struct {
			ID string `json:"id"`
		}{r.id}, nil)
	})
	return err
}

type remoteFeedback struct{ services *remoteServices }

func (f *remoteFeedback) LoadContext(context.Context) (json.RawMessage, error) { return nil, nil }
func (f *remoteFeedback) SaveContext(context.Context, json.RawMessage) error   { return nil }
func (f *remoteFeedback) RecordTurn(ctx context.Context, id string) error {
	if id != f.services.launch.RunID {
		return errors.New("Task turn identity changed")
	}
	return f.Ready(ctx)
}
func (f *remoteFeedback) InputStreaming(ctx context.Context, id string) error {
	return f.RecordTurn(ctx, id)
}
func (f *remoteFeedback) Ready(ctx context.Context) error {
	return f.services.call(ctx, "task.ready", "ready:"+storage.ID(), nil, nil)
}
func (f *remoteFeedback) Steering(ctx context.Context, finalizing bool) ([]agent.SteeringInput, error) {
	var inputs []agent.SteeringInput
	err := f.services.call(ctx, "task.steering", "steering:"+storage.ID(), struct {
		Finalizing bool `json:"finalizing"`
	}{finalizing}, &inputs)
	return inputs, err
}
func (f *remoteFeedback) AcknowledgeSteering(ctx context.Context, ids []string) error {
	return f.services.call(ctx, "task.ack", "ack:"+storage.ID(), ids, nil)
}
func (f *remoteFeedback) Observe(ctx context.Context, o agent.Observation) error {
	return f.services.call(ctx, "task.observe", "observe:"+storage.ID(), o, nil)
}
func (f *remoteFeedback) Settle(ctx context.Context) error {
	return f.services.call(ctx, "task.settle", "settle:"+storage.ID(), nil, nil)
}
