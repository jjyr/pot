package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/integrations"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"github.com/jjyr/pot/pkg/terminal"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Maintenance                                                              func() bool
	Composer                                                                 agent.Composer
	Context                                                                  context.Context
	Root, RuntimeID, BootID, Workspace, AgentHome, BuiltinSkills, PluginRoot string
	Manager                                                                  *execution.Manager
	Keys                                                                     secrets.KeySource
	Target                                                                   TargetDispatch
	CleanupTarget                                                            func(context.Context, string, bool) error

	WorkspaceHome       string // Optional destination home override for embedded/test hosts.
	PeerListen          string // Explicit TCP peer bind address; owner HTTP remains separate.
	Embedded            bool
	SandboxDirectory    string
	Node                string
	IntegrationAdapters []integrations.Adapter
}
type Service struct {
	agentLinks          *agentLinkHost
	linkPlugins         *linkhost.Host
	pluginWG            sync.WaitGroup
	pluginMu            sync.Mutex
	pluginStreams       map[string]backgroundStream
	Attachments         *attachments.Store
	Resources           *resources.Store
	mu                  sync.Mutex
	resourceMutations   sync.Mutex
	options             Options
	ctx                 context.Context
	cancel              context.CancelFunc
	Registry            *assistant.Registry
	Providers           *gateway.Registry
	Integrations        *integrations.Service
	integrationDB       *sql.DB
	integrationSecrets  *secrets.Store
	integrationAccounts *integrations.SQLAccounts
	Profiles            *ProfileStore
	Usage               *usage.Ledger
	Permissions         *PermissionStore
	Operations          *Operations
	Changes             core.Changes
	conversations       map[string]*assistant.Conversation
	hosts               map[string]*executor.Engine
	hostGenerations     map[string]uint64
	instructions        InstructionSources
	closed              bool
	Machines            *controlplane.Directory
	RunnerJournal       *runner.Journal
	Runner              *runner.Executor
	Tasks               *tasks.Service
	Schedules           *schedule.Service
	Mailbox             *assistant.Mailbox
	Terminals           *terminal.Hub
	taskLife            *taskLifecycle
}

func Open(options Options) (*Service, error) {
	if options.Root == "" || !core.Identifier(options.RuntimeID) || options.BootID == "" || options.Manager == nil {
		return nil, errors.New("Control requires private data root and shared Runtime execution manager")
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Keys == nil {
		options.Keys = secrets.SystemKey{Account: "go/" + options.RuntimeID}
	}
	if options.AgentHome == "" {
		options.AgentHome = filepath.Join(options.Root, "agent")
	}
	if options.Workspace == "" {
		options.Workspace = filepath.Join(options.Root, "agent-workspace")
	}
	if err := os.MkdirAll(options.Workspace, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(options.Context)
	s := &Service{options: options, ctx: ctx, cancel: cancel, conversations: map[string]*assistant.Conversation{}, hosts: map[string]*executor.Engine{}, linkPlugins: linkhost.New()}
	s.Terminals = terminal.New()
	success := false
	defer func() {
		if !success {
			s.Close()
		}
	}()
	var err error
	s.Resources, err = resources.Open(filepath.Join(options.Root, "resources"), filepath.Join(options.Root, "control.sqlite"))
	if err != nil {
		return nil, err
	}
	s.Attachments, err = attachments.Open(filepath.Join(options.Root, "attachments"))
	if err != nil {
		return nil, err
	}
	s.Attachments.Referenced = s.attachmentReferenced
	if err = s.Attachments.Sweep(); err != nil {
		return nil, err
	}
	s.Permissions, err = OpenPermissions(filepath.Join(options.Root, "permissions.json"))
	if err != nil {
		return nil, err
	}
	if _, _, err = s.Permissions.Policy(options.RuntimeID); err != nil {
		if err = s.Permissions.Enroll(options.RuntimeID, "local-"+options.RuntimeID); err != nil {
			return nil, err
		}
	}
	s.Permissions.Apply = s.applyPermissionRevision
	s.Permissions.PublishHook = s.publishHook
	s.Registry, err = assistant.OpenRegistry(filepath.Join(options.Root, "control.sqlite"), "main")
	if err != nil {
		return nil, err
	}
	s.Resources.ValidateOwner = func(ctx context.Context, owner string) error {
		deleted, err := s.Registry.Deleted(ctx, owner)
		if err != nil || deleted {
			return errors.New("resource owner unavailable")
		}
		return nil
	}
	s.Providers, err = gateway.Open(filepath.Join(options.Root, "control.sqlite"), options.Keys)
	if err != nil {
		return nil, err
	}
	err = s.openIntegrations()
	if err != nil {
		return nil, err
	}
	s.Profiles, err = OpenProfiles(filepath.Join(options.Root, "profiles.json"), options.PluginRoot, s.Providers)
	if err != nil {
		return nil, err
	}
	s.Usage, err = usage.Open(filepath.Join(options.Root, "usage.sqlite"))
	if err != nil {
		return nil, err
	}
	s.Operations, err = OpenOperations(filepath.Join(options.Root, "operations.sqlite"))
	if err != nil {
		return nil, err
	}
	if err = s.openTasks(); err != nil {
		return nil, err
	}
	if err = s.openAgentLinks(); err != nil {
		return nil, err
	}
	s.taskLife.wg.Add(1)
	go s.taskLoop()
	s.pluginWG.Add(1)
	go s.pluginLoop()
	success = true
	return s, nil
}
func (s *Service) Conversation(ctx context.Context, id string) (*assistant.Conversation, error) {
	if !core.Identifier(id) {
		return nil, errors.New("invalid assistant identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("Control stopped")
	}
	deleted, err := s.Registry.Deleted(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || deleted {
		return nil, errors.New("assistant unavailable")
	}
	if err != nil {
		return nil, err
	}
	if c := s.conversations[id]; c != nil {
		return c, nil
	}
	initial, rules, err := s.Registry.Initial(ctx, id)
	if err != nil {
		return nil, err
	}
	var conversation *assistant.Conversation
	welcome, err := s.Registry.Welcome(ctx, id)
	if err != nil {
		return nil, err
	}
	intent, err := s.Registry.InitialIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	options := assistant.ConversationOptions{InitialWelcome: welcome, InitialIntent: intent, Context: s.ctx, Owner: id, RuntimeID: s.options.RuntimeID, BootID: s.options.BootID, Manager: s.options.Manager, InitialSpec: initial, InitialRules: rules, RefreshConfiguration: s.refreshAssistantConfiguration, OnFinished: func(ctx context.Context, r protocol.SendMessage, result execution.Result) error {
		return s.finishAgentDelivery(ctx, id, r, result)
	}}
	options.ValidateAttachments = func(ctx context.Context, spec core.ResolvedSpec, items []core.Attachment) error {
		return s.Attachments.Validate(id, items, spec.AttachmentCapabilities())
	}
	options.Prepare = func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
		return s.prepare(ctx, conversation, thread, request)
	}
	conversation, err = assistant.OpenConversation(filepath.Join(s.options.Root, "assistants", id, "conversation.sqlite"), options)
	if err != nil {
		return nil, err
	}
	if err = conversation.StartPendingUsers(ctx); err != nil {
		conversation.Close()
		return nil, err
	}
	s.conversations[id] = conversation
	s.watchTaskOwner(id, conversation)
	return conversation, nil
}
func (s *Service) host(owner string, generation uint64) (*executor.Engine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("Control stopped")
	}
	e := s.hosts[owner]
	if e != nil && s.hostGenerations[owner] != generation {
		if err := e.Close(); err != nil {
			return nil, err
		}
		delete(s.hosts, owner)
		e = nil
	}
	if e == nil {
		worker, err := os.Executable()
		if err != nil {
			return nil, err
		}
		options := executor.Options{ProcessWorker: []string{worker}, Admit: s.executionAdmission, Node: s.options.Node, Home: s.options.AgentHome, SandboxDirectory: s.options.SandboxDirectory, ProtectedPaths: []string{s.options.Root}}
		workspace, workspaceErr := s.RunnerJournal.TaskWorkspace(s.ctx, owner)
		if workspaceErr != nil {
			return nil, workspaceErr
		}
		if workspace != nil {
			options.SandboxRead = append(options.SandboxRead, workspace.GitDirs...)
			options.SandboxWrite = append(options.SandboxWrite, workspace.GitDirs...)
			options.SandboxRead = append(options.SandboxRead, filepath.Join(s.options.Root, "resource-cache", "task-skills", owner))
		}
		e = executor.New(options)
		s.hosts[owner] = e
		if s.hostGenerations == nil {
			s.hostGenerations = map[string]uint64{}
		}
		s.hostGenerations[owner] = generation
	}
	return e, nil
}
func (s *Service) prepare(ctx context.Context, c *assistant.Conversation, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
	return s.prepareFor(ctx, c, thread, request, thread.Owner)
}
func (s *Service) prepareFor(ctx context.Context, c *assistant.Conversation, thread core.Thread, request core.TurnRequest, principal string) (assistant.RunOptions, error) {
	_, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	target := core.RuntimeTarget(s.options.RuntimeID)

	rules, err := c.Instructions(ctx)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	executionContext, err := s.prepareExecutionFor(ctx, thread.Owner, principal, target, false, rules.Text)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	if err = s.addExecutionEnvironment(&executionContext, target); err != nil {
		return assistant.RunOptions{}, err
	}
	identities, err := s.Registry.Definitions(ctx)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	for _, definition := range identities {
		if definition.ID == principal {
			identity, _ := json.Marshal(map[string]any{"name": definition.Name, "has_avatar": definition.Avatar != "", "address": protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: principal}})
			executionContext.MainPrompt += "\nCurrent assistant identity (data): " + string(identity) + ". When the user asks to change your name or avatar, use update_agent_profile to save it."
			break
		}
	}
	executionContext.ContextSections = append(executionContext.ContextSections, core.ContextSection{Key: "models", Text: s.modelContext()})
	engine, err := s.host(thread.Owner, thread.Generation)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	hostWorkspace, err := s.ExecutionWorkspace(principal)
	if err != nil {
		return assistant.RunOptions{}, err
	}
	dispatch := &Dispatcher{RuntimeID: s.options.RuntimeID, Owner: thread.Owner, Turn: request.RequestID, Generation: thread.Generation, DefaultTarget: target, Spec: thread.Config, Permissions: s.Permissions, Operations: s.Operations, Host: engine, Target: s.options.Target, ApprovalOwner: principal, HostWorkspace: hostWorkspace, Usage: s.Usage, UsageScope: principal, Credentials: s.Integrations, Product: s.productDispatchBound(principal, thread.Owner, request.RequestID, thread.Generation, false)}
	waitScope := principal
	if thread.Owner != principal {
		waitScope = thread.Owner
	}
	model := &ModelSession{WaitScope: waitScope, Registry: s.Providers, Usage: s.Usage, Spec: thread.Config, Scope: principal, Turn: request.RequestID, Generation: thread.Generation, Role: modelRole(request.RequestID), Claim: func(ctx context.Context, id string) error {
		return c.ClaimModel(ctx, request.RequestID, thread.Generation, id)
	}}
	ports := &executionPorts{model: model, compaction: func(ctx context.Context, raw json.RawMessage) error {
		var report struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Tokens uint64 `json:"tokens"`
		}
		if err := core.DecodeJSON(raw, &report); err != nil {
			return err
		}
		if report.Tokens > core.MaxSafeInteger {
			return errors.New("compaction token count exceeds supported precision")
		}
		event := usage.CompactionEvent{ID: report.ID, Action: report.Action}
		tokens := int64(report.Tokens)
		if report.Action == "start" {
			event.Reason = "threshold"
			event.TokensBefore = &tokens
		} else if report.Action == "complete" {
			event.TokensAfter = &tokens
		}
		return s.Usage.Compaction(ctx, principal, request.RequestID, event, storage.Now())
	}}
	definitions := []modelprotocol.ToolDefinition{}
	for _, raw := range core.ExecutionTools(thread.Config.Params.Agent.Shell) {
		var definition modelprotocol.ToolDefinition
		if err = core.DecodeJSON(raw, &definition); err != nil {
			return assistant.RunOptions{}, err
		}
		var schema map[string]any
		if err = core.DecodeJSON(definition.InputSchema, &schema); err != nil {
			return assistant.RunOptions{}, err
		}

		definition.InputSchema, _ = json.Marshal(schema)
		definitions = append(definitions, definition)
	}
	definitions = append(definitions, s.productTools(false)...)
	if strings.HasPrefix(request.RequestID, "pot-result-") {
		definitions, err = s.resultTools(ctx, principal, request.RequestID)
		if err != nil {
			return assistant.RunOptions{}, err
		}
		if len(definitions) > 0 {
			executionContext.Role = core.Recovery
		}
	}
	return assistant.RunOptions{ResolveAttachments: s.resolveAttachments(principal, request.RequestID), Composer: s.options.Composer, Execution: executionContext, Services: ports, Dispatch: dispatch.Dispatch, Tools: definitions, Cleanup: func(ctx context.Context, _ core.ExecutionContext, abort bool) error {
		s.Integrations.Forget(credentialScope(thread.Owner, request.RequestID, thread.Generation))
		s.Permissions.Expire(principal, request.RequestID, thread.Generation)
		err := engine.Drain()
		if s.options.CleanupTarget != nil {
			err = errors.Join(err, s.options.CleanupTarget(ctx, thread.Owner, abort))
		}
		return err
	}, Ready: func(ctx context.Context) error {
		waiting, err := s.Usage.Waiting(ctx, waitScope)
		if err != nil {
			return err
		}
		if waiting != nil {
			ready, err := s.Usage.Ready(ctx, waitScope, storage.Now())
			if err != nil {
				return err
			}
			if !ready {
				return protocol.APIError{Code: "pot_token_limit", Message: "Pot token allowance is unavailable"}
			}
		}
		return nil
	}, Observe: func(ctx context.Context, o agent.Observation) error { s.Changes.Notify("usage"); return nil }}, nil
}

type executionPorts struct {
	model      *ModelSession
	compaction func(context.Context, json.RawMessage) error
}

func (p *executionPorts) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	return p.model.Model(ctx, r)
}
func (p *executionPorts) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, errors.New("RPC operation unavailable")
}
func (p *executionPorts) Compaction(ctx context.Context, r json.RawMessage) error {
	return p.compaction(ctx, r)
}
func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.cancel()
	conversations := []*assistant.Conversation{}
	for _, c := range s.conversations {
		conversations = append(conversations, c)
	}
	engines := []*executor.Engine{}
	for _, e := range s.hosts {
		engines = append(engines, e)
	}
	s.mu.Unlock()
	linkErr := s.closeAgentLinks()
	s.pluginWG.Wait()
	var result error = linkErr
	if s.Terminals != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result = errors.Join(result, s.Terminals.Close(ctx))
		cancel()
	}
	if s.taskLife != nil {
		s.taskLife.wg.Wait()
	}
	if s.Runner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		result = errors.Join(result, s.Runner.Close(ctx))
		cancel()
	}
	for _, c := range conversations {
		result = errors.Join(result, c.Close())
	}
	for _, e := range engines {
		result = errors.Join(result, e.Close())
	}
	if s.Machines != nil {
		result = errors.Join(result, s.Machines.Close())
	}
	if s.RunnerJournal != nil {
		result = errors.Join(result, s.RunnerJournal.Close())
	}
	if s.Tasks != nil {
		result = errors.Join(result, s.Tasks.Close())
	}
	if s.Mailbox != nil {
		result = errors.Join(result, s.Mailbox.Close())
	}
	if s.Operations != nil {
		result = errors.Join(result, s.Operations.Close())
	}
	if s.Usage != nil {
		result = errors.Join(result, s.Usage.Close())
	}
	if s.Integrations != nil {
		result = errors.Join(result, s.Integrations.Close())
	}
	if s.integrationSecrets != nil {
		result = errors.Join(result, s.integrationSecrets.Close())
	}
	if s.integrationDB != nil {
		result = errors.Join(result, s.integrationDB.Close())
	}
	if s.Providers != nil {
		result = errors.Join(result, s.Providers.Close())
	}
	if s.Registry != nil {
		result = errors.Join(result, s.Registry.Close())
	}
	if s.Resources != nil {
		result = errors.Join(result, s.Resources.Close())
	}
	return result
}

// Keep timeout conversion explicit before a provider attempt; Go durations are
// signed nanoseconds while protocol request timeouts use safe JSON milliseconds.
func validRequestTimeout(ms uint64) bool {
	return ms > 0 && ms <= uint64((1<<63-1)/int64(time.Millisecond))
}

func modelRole(requestID string) string {
	if strings.HasPrefix(requestID, "pot-result-") {
		return "summary"
	}
	return "pot"
}
