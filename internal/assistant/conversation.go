package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type RunOptions struct {
	MessageCompleted   func(message, text string)
	Execution          core.ExecutionContext
	Services           core.ExecutionServices
	Dispatch           agent.Dispatch
	Cleanup            agent.Cleanup
	Composer           agent.Composer
	Tools              []modelprotocol.ToolDefinition
	ResolveAttachments func(context.Context, core.ResolvedSpec, []modelprotocol.Message) ([]modelprotocol.Message, error)
	PrepareContext     agent.PrepareContext
	Ready              func(context.Context) error
	Observe            func(context.Context, agent.Observation) error
	Settle             func(context.Context) error
}
type ConversationOptions struct {
	ReplyCommitted func(protocol.Message)
	ProjectUI      func(context.Context, []protocol.UIMessage) error
	IdlePolicy     func(context.Context, core.Thread) (IdleCompactionPolicy, error)
	CompactGate    func(context.Context, func() error) error

	Context                                  context.Context
	Owner, ConversationID, RuntimeID, BootID string
	Manager                                  *execution.Manager
	InitialSpec                              *core.ResolvedSpec
	InitialRules                             string
	InitialWelcome                           string
	InitialIntent                            json.RawMessage
	RefreshConfiguration                     func(context.Context, core.Thread, json.RawMessage) (*core.ResolvedSpec, error)
	OnFinished                               func(context.Context, protocol.SendMessage, execution.Result) error
	OnResumeStarting                         func(context.Context, protocol.SendMessage) error
	MaxRequests, MaxTurnRequests             uint64 // zero is unlimited; independent of token allowances
	ValidateAttachments                      func(context.Context, core.ResolvedSpec, []core.Attachment) error
	Prepare                                  func(context.Context, core.Thread, core.TurnRequest) (RunOptions, error)
}
type savedTurn struct {
	AttemptID      string                 `json:"attempt_id,omitempty"`
	Owner          string                 `json:"owner"`
	BootID         string                 `json:"boot_id"`
	Generation     uint64                 `json:"generation"`
	Request        protocol.SendMessage   `json:"request"`
	UserMessageID  string                 `json:"user_message_id"`
	State          string                 `json:"state"`
	Cancelling     bool                   `json:"cancelling"`
	Paused         bool                   `json:"paused"`
	Resuming       bool                   `json:"resuming"`
	ReplyMessageID string                 `json:"reply_message_id"`
	Inputs         []protocol.SendMessage `json:"inputs,omitempty"`
	UserMessages   []string               `json:"user_messages,omitempty"`
}

func (t *savedTurn) attemptID() string {
	if t.AttemptID != "" {
		return t.AttemptID
	}
	return t.Request.RequestID
}

func validateConversationRequest(request protocol.SendMessage) error {
	if isCompactRequest(request.RequestID) {
		if !core.Identifier(request.RequestID) || !core.Identifier(request.ConversationID) || request.Text != "" || len(request.Attachments) != 0 || len(request.References) != 0 {
			return errors.New("invalid internal compaction")
		}
		return nil
	}
	if strings.HasPrefix(request.RequestID, "pot-result-") {
		if !core.Identifier(request.RequestID) || !core.Identifier(request.ConversationID) || request.Text == "" || len(request.Text) > 4*inbox.MaxBatchBytes {
			return errors.New("invalid internal result batch")
		}
		return nil
	}
	return request.Validate()
}

func (t *savedTurn) includes(id string) bool {
	return id == t.UserMessageID || slices.Contains(t.UserMessages, id)
}

func (t *savedTurn) inputText() string {
	if len(t.Inputs) <= 1 {
		return t.Request.Text
	}
	texts := make([]string, 0, len(t.Inputs))
	for _, input := range t.Inputs {
		texts = append(texts, input.Text)
	}
	raw, _ := json.Marshal(texts)
	return "Handle these user messages together in their original order. Later messages may supplement or correct earlier ones. Preserve each request's constraints:\n" + string(raw)
}

type Conversation struct {
	mu                          sync.Mutex
	store                       *storage.Store
	options                     ConversationOptions
	ctx                         context.Context
	stop                        context.CancelFunc
	idleDone                    chan struct{}
	done                        chan struct{}
	input                       chan protocol.SendMessage
	thread                      *core.Thread
	turn                        *savedTurn
	recovery                    *savedTurn
	runCancel                   context.CancelFunc
	messages                    []protocol.Message
	cursor                      int64
	modelCount                  uint64
	blocked, closed, finalizing bool
	lastError                   error
	finishError                 error
	completion                  *turnCompletion
	finishedRequest             string
	subscribers                 map[chan protocol.Snapshot]struct{}
	uiSubscribers               map[chan struct{}]struct{}
	closeOnce                   sync.Once
	closeErr                    error
	controlChanged              chan struct{}
}

func OpenConversation(path string, options ConversationOptions) (*Conversation, error) {
	if !core.Identifier(options.Owner) || options.Manager == nil || options.Prepare == nil || options.RuntimeID == "" || options.BootID == "" {
		return nil, errors.New("conversation needs fixed owner, runtime/boot identity, manager and preparation port")
	}
	if options.MaxRequests > core.MaxSafeInteger || options.MaxTurnRequests > core.MaxSafeInteger {
		return nil, errors.New("model request limits exceed supported integer range")
	}
	if options.ConversationID == "" {
		options.ConversationID = options.Owner
	}
	if !core.Identifier(options.ConversationID) {
		return nil, errors.New("invalid conversation identity")
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	store, err := storage.Open(path)
	if err != nil {
		return nil, err
	}
	store.ReplyCommitted = options.ReplyCommitted
	if err := store.InitializeInstructions(options.InitialRules); err != nil {
		store.Close()
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			store.Close()
		}
	}()
	err = storage.Transaction(context.Background(), store.DB(), func(tx *sql.Tx) error {
		var initialized bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM metadata WHERE key='conversation.initialized')").Scan(&initialized); err != nil {
			return err
		}
		if !initialized {
			if options.InitialWelcome != "" {
				if err := initializeWelcome(tx, options); err != nil {
					return err
				}
			}
			if len(options.InitialRules) > 262144 {
				return errors.New("initial rules exceed limit")
			}
			if len(options.InitialIntent) > 0 && string(options.InitialIntent) != "null" {
				if !json.Valid(options.InitialIntent) {
					return errors.New("invalid initial configuration intent")
				}
				if err := writeConfiguration(context.Background(), tx, configurationState{Intent: options.InitialIntent}); err != nil {
					return err
				}
			}
			if _, err := tx.Exec("INSERT INTO metadata VALUES('conversation.initialized','true')"); err != nil {
				return err
			}
		}
		for key, value := range map[string]string{"conversation.owner": options.Owner, "conversation.id": options.ConversationID} {
			var previous string
			err := tx.QueryRow("SELECT value FROM metadata WHERE key=?", key).Scan(&previous)
			if err == nil {
				if previous != value {
					return errors.New("conversation owner or identity mismatch")
				}
			} else if errors.Is(err, sql.ErrNoRows) {
				if _, err := tx.Exec("INSERT INTO metadata VALUES (?,?)", key, value); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if options.InitialSpec != nil {
		if err := InitializeThread(context.Background(), store, options.Owner, *options.InitialSpec); err != nil {
			return nil, err
		}
	}
	thread, err := CurrentThread(context.Background(), store)
	if err != nil {
		return nil, err
	}
	if thread != nil && thread.Owner != options.Owner {
		return nil, errors.New("thread belongs to another assistant")
	}
	ctx, stop := context.WithCancel(options.Context)
	c := &Conversation{store: store, options: options, ctx: ctx, stop: stop, done: make(chan struct{}), idleDone: make(chan struct{}), input: make(chan protocol.SendMessage, 1), thread: thread, subscribers: map[chan protocol.Snapshot]struct{}{}, controlChanged: make(chan struct{})}
	if c.messages, err = store.Messages(context.Background()); err != nil {
		stop()
		return nil, err
	}
	for i := len(c.messages) - 1; i >= 0; i-- {
		if message := c.messages[i]; message.Role == protocol.Assistant {
			if message.Status == protocol.Failed && message.Error != "" {
				c.lastError = errors.New(message.Error)
			}
			break
		}
	}
	if c.cursor, err = store.Cursor(context.Background()); err != nil {
		stop()
		return nil, err
	}
	if err = c.finishCompactionMessagesLocked(context.Background(), "interrupted"); err != nil {
		stop()
		return nil, err
	}
	if err = store.DB().QueryRow("SELECT COUNT(*) FROM assistant_model_attempts").Scan(&c.modelCount); err != nil {
		stop()
		return nil, err
	}
	if value, exists, err := store.Get(context.Background(), "conversation.blocked"); err != nil {
		stop()
		return nil, err
	} else if exists && value == "true" {
		c.blocked = true
	}
	if raw, exists, err := store.Get(context.Background(), "conversation.recovery"); err != nil {
		stop()
		return nil, err
	} else if exists && raw != "null" {
		var recovered savedTurn
		if err := core.DecodeJSON([]byte(raw), &recovered); err != nil {
			stop()
			return nil, err
		}
		if recovered.Owner != options.Owner || thread == nil || recovered.Generation != thread.Generation || recovered.UserMessageID == "" || validateConversationRequest(recovered.Request) != nil || recovered.Request.ConversationID != options.ConversationID {
			stop()
			return nil, errors.New("persisted recovery turn binding mismatch")
		}
		c.recovery = &recovered
	}
	if raw, exists, err := store.Get(context.Background(), "conversation.completion"); err != nil {
		stop()
		return nil, err
	} else if exists && raw != "null" {
		var completion turnCompletion
		if err := core.DecodeJSON([]byte(raw), &completion); err != nil {
			stop()
			return nil, err
		}
		if err := completion.validate(options, thread); err != nil {
			stop()
			return nil, err
		}
		c.completion = &completion
	}
	if raw, exists, err := store.Get(context.Background(), "conversation.turn"); err != nil {
		stop()
		return nil, err
	} else if exists && raw != "null" {
		var turn savedTurn
		if err := core.DecodeJSON([]byte(raw), &turn); err != nil {
			stop()
			return nil, err
		}
		if turn.Owner != options.Owner || thread == nil || turn.Generation != thread.Generation || (turn.UserMessageID == "" && !isCompactRequest(turn.Request.RequestID)) || validateConversationRequest(turn.Request) != nil || turn.Request.ConversationID != options.ConversationID {
			stop()
			return nil, errors.New("persisted turn binding mismatch")
		}
		c.turn = &turn
		if c.completion != nil {
			if c.completion.Turn.Request.RequestID != turn.Request.RequestID {
				stop()
				return nil, errors.New("completion differs from active turn")
			}
		} else if isCompactRequest(turn.Request.RequestID) {
			if err := c.abandonIdleLocked(context.Background()); err != nil {
				stop()
				return nil, err
			}
			if err := c.startNextUserLocked(context.Background()); err != nil {
				stop()
				return nil, err
			}
		} else if turn.State != "waiting" {
			c.blocked = true
			c.lastError = errors.New("previous execution was interrupted; inspect its receipts before continuing")
			c.recovery = &turn
			c.turn = nil
			if err := c.appendTurnLocked(context.Background()); err != nil {
				stop()
				return nil, err
			}
		}
	}
	if c.completion != nil {
		turn := c.completion.Turn
		c.turn = &turn
		c.finalizing = true
	}
	go c.idleLoop()
	pending := c.completion
	go func() {
		defer close(c.done)
		driver := Driver{Manager: options.Manager, Services: c}
		if pending != nil {
			if err := driver.finish(ctx, pending.Turn.Request, pending.result()); err != nil {
				return
			}
		}
		_ = driver.Serve(ctx, c.input)
	}()
	ok = true
	return c, nil
}
func (c *Conversation) stateLocked() protocol.Snapshot {
	phase := protocol.Ready
	var detail *string
	if c.thread == nil {
		phase = protocol.NeedsConfiguration
	}
	if c.blocked || c.finishError != nil || c.lastError != nil && c.turn == nil {
		phase = protocol.AgentFailed
	}
	if c.lastError != nil {
		message := c.lastError.Error()
		detail = &message
	}
	var turn *protocol.TurnState
	if c.turn != nil {
		turn = &protocol.TurnState{UserMessageID: c.turn.UserMessageID, Cancelling: c.turn.Cancelling, Paused: c.turn.Paused, State: c.turn.State}
		if c.finishError != nil {
			turn.State = "unknown"
		}
		if c.turn.Paused {
			message := "Paused before the next model request"
			detail = &message
		}
		if c.turn.State == "waiting" {
			message := "Waiting for Pot token allowance"
			detail = &message
		}
	}
	caps := core.AttachmentCapabilities{}
	if c.thread != nil {
		caps = c.thread.Config.AttachmentCapabilities()
		if c.turn == nil {
			if pending, err := c.configurationLocked(context.WithoutCancel(c.ctx)); err == nil && pending.PendingSpec != nil {
				caps = pending.PendingSpec.AttachmentCapabilities()
			}
		}
	}
	model := ""
	output := uint64(0)
	if c.thread != nil {
		model = c.thread.Config.Model
		output = c.thread.Config.Params.MaxOutputTokens
	}
	var budget *protocol.RequestBudget
	if c.options.MaxRequests > 0 {
		budget = &protocol.RequestBudget{Used: c.modelCount, Limit: c.options.MaxRequests, MaxOutputTokens: output}
	}
	return protocol.Snapshot{Budget: budget, APIVersion: protocol.APIVersion, BootID: c.options.BootID, Cursor: c.cursor, Agent: protocol.AgentStatus{Attachments: caps, Model: model, Phase: phase, Detail: detail}, ConversationID: c.options.ConversationID, Turn: turn, Continuation: c.continuationLocked()}
}
func (c *Conversation) snapshotLocked() protocol.Snapshot {
	state := c.stateLocked()
	state.Messages = slices.Clone(c.messages)
	for i := range state.Messages {
		if state.Messages[i].Compaction != nil {
			value := *state.Messages[i].Compaction
			state.Messages[i].Compaction = &value
		}
		state.Messages[i].Attachments = slices.Clone(state.Messages[i].Attachments)
		if state.Messages[i].RequestID != nil {
			request := *state.Messages[i].RequestID
			state.Messages[i].RequestID = &request
		}
	}
	return state
}
func (c *Conversation) Snapshot(ctx context.Context) (protocol.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Snapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked(), nil
}
func (c *Conversation) changedLocked() {
	for ch := range c.uiSubscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	for ch := range c.subscribers {
		select {
		case <-ch:
		default:
		}
		ch <- c.snapshotLocked()
	}
}
func (c *Conversation) Subscribe(ctx context.Context) (<-chan protocol.Snapshot, func(), error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, errors.New("conversation closed")
	}
	ch := make(chan protocol.Snapshot, 1)
	ch <- c.snapshotLocked()
	c.subscribers[ch] = struct{}{}
	c.mu.Unlock()
	var once sync.Once
	removed := make(chan struct{})
	remove := func() {
		once.Do(func() { c.mu.Lock(); delete(c.subscribers, ch); close(ch); c.mu.Unlock(); close(removed) })
	}
	go func() {
		select {
		case <-ctx.Done():
			remove()
		case <-c.done:
			remove()
		case <-removed:
		}
	}()
	return ch, remove, nil
}
func (c *Conversation) Events(ctx context.Context, after int64) ([]protocol.Event, error) {
	return c.store.Events(ctx, after)
}
func (c *Conversation) putMessageLocked(ctx context.Context, m protocol.Message, metadata map[string]string) error {
	return c.putMessagesLocked(ctx, []protocol.Message{m}, metadata)
}
func (c *Conversation) putMessagesLocked(ctx context.Context, messages []protocol.Message, metadata map[string]string) error {
	events, err := c.store.SaveMessagesWithMetadataMutation(ctx, messages, metadata, nil)
	if err != nil {
		return err
	}
	for _, m := range messages {
		found := false
		for i := range c.messages {
			if c.messages[i].ID == m.ID {
				c.messages[i] = m
				found = true
				break
			}
		}
		if !found {
			c.messages = append(c.messages, m)
		}
	}
	c.cursor = *events[len(events)-1].Cursor
	c.changedLocked()
	return nil
}
func (c *Conversation) appendTurnLocked(ctx context.Context) error {
	return c.appendTurnMetadataLocked(ctx, nil)
}
func (c *Conversation) appendTurnMetadataLocked(ctx context.Context, metadata map[string]string) error {
	raw, err := json.Marshal(c.turn)
	if err != nil {
		return err
	}
	var turn *protocol.TurnState
	if c.turn != nil {
		turn = &protocol.TurnState{UserMessageID: c.turn.UserMessageID, Cancelling: c.turn.Cancelling, Paused: c.turn.Paused, State: c.turn.State}
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["conversation.turn"] = string(raw)
	metadata["conversation.blocked"] = fmt.Sprint(c.blocked)
	recoveryRaw, err := json.Marshal(c.recovery)
	if err != nil {
		return err
	}
	metadata["conversation.recovery"] = string(recoveryRaw)
	event, err := c.store.AppendEvent(ctx, protocol.Event{Type: "turn", Turn: turn}, metadata)
	if err != nil {
		return err
	}
	c.cursor = *event.Cursor
	c.changedLocked()
	return nil
}
func (c *Conversation) Send(ctx context.Context, request protocol.SendMessage) (protocol.SendMessageAccepted, error) {
	if strings.HasPrefix(request.RequestID, "pot-result-") || isCompactRequest(request.RequestID) {
		return protocol.SendMessageAccepted{}, errors.New("reserved maintenance identity")
	}
	return c.accept(ctx, request, protocol.User)
}

// Report atomically claims an automatic result summary using a stable ID. It is
// system transcript data and enters the shared summary lane. Control may supply
// recovery tools for delivered failures.
func (c *Conversation) Report(ctx context.Context, id, text string) (protocol.SendMessageAccepted, error) {
	if !strings.HasPrefix(id, "pot-result-") {
		return protocol.SendMessageAccepted{}, errors.New("invalid notification identity")
	}
	return c.accept(ctx, protocol.SendMessage{RequestID: id, ConversationID: c.options.ConversationID, Text: text}, protocol.System)
}
func (c *Conversation) validateAttachmentsLocked(ctx context.Context, items []core.Attachment) error {
	if len(items) == 0 {
		return nil
	}
	if c.thread == nil || c.options.ValidateAttachments == nil {
		return errors.New("attachments unavailable")
	}
	caps := c.thread.Config.AttachmentCapabilities()
	for _, a := range items {
		if !caps.Allows(a.MIMEType) {
			return errors.New("current model does not support this attachment type")
		}
	}
	return c.options.ValidateAttachments(ctx, c.thread.Config, items)
}
func (c *Conversation) accept(ctx context.Context, request protocol.SendMessage, role protocol.Role) (protocol.SendMessageAccepted, error) {
	request.Attachments = slices.Clone(request.Attachments)
	request.References = slices.Clone(request.References)
	if err := validateConversationRequest(request); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.ctx.Err() != nil {
		return protocol.SendMessageAccepted{}, errors.New("conversation closed")
	}
	if request.ConversationID != c.options.ConversationID {
		return protocol.SendMessageAccepted{}, errors.New("wrong conversation")
	}
	// Ordinary sends cannot adopt an identity already accepted as guidance,
	// whether it entered the active mailbox or the next-user inbox.
	_, guidance, err := c.store.Get(ctx, "conversation.user_source."+request.RequestID)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	var steering bool
	if err := c.store.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM assistant_steering WHERE id=?)", request.RequestID).Scan(&steering); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if guidance || steering {
		return protocol.SendMessageAccepted{}, errors.New("request identity already used by guidance")
	}
	old, err := c.store.Duplicate(ctx, request.RequestID)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if old != nil {
		if old.Role != role || old.ConversationID != request.ConversationID || old.Text != request.Text || (!slices.Equal(old.Attachments, request.Attachments) || !slices.Equal(old.References, request.References)) {
			return protocol.SendMessageAccepted{}, errors.New("request identity already has different content")
		}
		return protocol.SendMessageAccepted{MessageID: old.ID, Duplicate: true}, nil
	}
	if c.turn == nil {
		generation := uint64(0)
		if c.thread != nil {
			generation = c.thread.Generation
		}
		if err := c.applyConfigurationBoundaryLocked(ctx); err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if c.thread != nil && c.thread.Generation != generation {
			if err := c.appendTurnLocked(ctx); err != nil {
				return protocol.SendMessageAccepted{}, err
			}
		}
	}
	if err := c.validateReferencesLocked(request.References); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if err := c.validateAttachmentsLocked(ctx, request.Attachments); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if c.blocked {
		return protocol.SendMessageAccepted{}, agent.ErrUncertainEffects
	}
	if c.turn != nil {
		if role == protocol.User {
			return c.queueUserLocked(ctx, request)
		}
		return protocol.SendMessageAccepted{}, errors.New("a conversation turn is already active or waiting")
	}
	if role == protocol.System {
		var pending int
		if err := c.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_user_inbox").Scan(&pending); err != nil {
			return protocol.SendMessageAccepted{}, err
		}
		if pending != 0 {
			return protocol.SendMessageAccepted{}, errors.New("user input has priority")
		}
	}
	if c.thread == nil {
		return protocol.SendMessageAccepted{}, errors.New("configure an assistant before sending messages")
	}
	message := protocol.Message{ID: storage.ID(), ConversationID: request.ConversationID, Role: role, Text: request.Text, Attachments: request.Attachments, References: request.References, Status: protocol.Accepted, CreatedAtMS: storage.Now(), RequestID: &request.RequestID}
	c.turn = &savedTurn{Owner: c.options.Owner, BootID: c.options.BootID, Generation: c.thread.Generation, Request: request, UserMessageID: message.ID, State: "queued"}
	raw, _ := json.Marshal(c.turn)
	if err := c.putMessageLocked(ctx, message, map[string]string{"conversation.turn": string(raw), "conversation.retry": ""}); err != nil {
		c.turn = nil
		return protocol.SendMessageAccepted{}, err
	}
	c.lastError = nil
	c.input <- request
	return protocol.SendMessageAccepted{MessageID: message.ID}, nil
}
func (c *Conversation) Cancel(ctx context.Context, request protocol.CancelTurn) error {
	if !core.Identifier(request.RequestID) || !core.Identifier(request.UserMessageID) {
		return errors.New("invalid cancellation identity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := "conversation.cancel." + request.RequestID
	old, exists, err := c.store.Get(ctx, key)
	if err != nil {
		return err
	}
	if exists {
		if old != request.UserMessageID {
			return errors.New("conflicting cancellation identity")
		}
		return nil
	}
	if c.turn == nil || c.turn.UserMessageID != request.UserMessageID {
		return errors.New("turn is no longer active")
	}
	previous := *c.turn
	c.turn.Cancelling = true
	// A failed completion write still belongs to the driver's retry loop.
	// Record cancellation intent without retiring the turn underneath that loop.
	if c.turn.State == "waiting" && !c.finalizing {
		c.turn = nil
		if err := c.appendTurnMetadataLocked(ctx, map[string]string{key: request.UserMessageID}); err != nil {
			c.turn = &previous
			return err
		}
		for _, message := range slices.Clone(c.messages) {
			if message.Queued || message.Role == protocol.User && message.RequestID != nil && !previous.includes(message.ID) {
				continue
			}
			if message.Status == protocol.Accepted || message.Status == protocol.Streaming {
				message.Status = protocol.MessageCancelled
				if message.Tool != nil {
					tool := *message.Tool
					tool.Phase = "stopped"
					tool.FinishedAtMS = storage.Now()
					message.Tool = &tool
				}
				if err := c.putMessageLocked(ctx, message, nil); err != nil {
					return err
				}
			}
		}
		if c.options.OnFinished != nil {
			if err := c.options.OnFinished(ctx, previous.Request, execution.Result{Phase: execution.Cancelled}); err != nil {
				return err
			}
		}
		return c.startNextUserLocked(ctx)
	}
	metadata := map[string]string{key: request.UserMessageID}
	var completion *turnCompletion
	if c.turn.State == "waiting" && c.completion == nil {
		// Commit the cancellation and its terminal receipt together, so a restart
		// before the driver's next retry cannot strand a cancelling waiting turn.
		pending := turnCompletion{Turn: *c.turn, Phase: execution.Cancelled, Error: context.Canceled.Error()}
		raw, err := json.Marshal(pending)
		if err != nil {
			c.turn = &previous
			return err
		}
		metadata["conversation.completion"] = string(raw)
		completion = &pending
	}
	if err := c.appendTurnMetadataLocked(ctx, metadata); err != nil {
		c.turn = &previous
		return err
	}
	if completion != nil {
		c.completion = completion
	}
	if c.runCancel != nil {
		c.runCancel()
	}
	return nil
}
func (c *Conversation) Resume(ctx context.Context) error { return c.resumeOriginal(ctx, "") }
func (c *Conversation) ResumeOriginal(ctx context.Context, turn string) error {
	if turn == "" {
		return errors.New("original turn is required")
	}
	return c.resumeOriginal(ctx, turn)
}
func (c *Conversation) resumeOriginal(ctx context.Context, turn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.ctx.Err() != nil {
		return errors.New("conversation closed")
	}
	if turn != "" && (c.turn == nil || c.turn.Request.RequestID != turn) {
		return errors.New("original allowance turn changed")
	}
	if turn != "" && c.turn != nil && c.turn.Resuming && (c.turn.State == "queued" || c.turn.State == "running") {
		return nil
	}
	if c.turn == nil || c.turn.State != "waiting" || c.turn.Cancelling || c.blocked {
		return errors.New("no safe waiting turn to resume")
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		return err
	}
	var checkpoint agent.Checkpoint
	if err := core.DecodeJSON(raw, &checkpoint); err != nil {
		return err
	}
	if checkpoint.Started != nil || checkpoint.ModelRequestID != nil {
		return agent.ErrUncertainEffects
	}
	c.turn.Resuming = true
	c.turn.State = "queued"
	c.turn.BootID = c.options.BootID
	c.lastError = nil
	if err := c.appendTurnLocked(ctx); err != nil {
		return err
	}
	c.input <- c.turn.Request
	return nil
}
func (c *Conversation) Configure(ctx context.Context, spec core.ResolvedSpec, expectedGeneration uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.turn != nil || c.blocked {
		return errors.New("conversation is not ready for configuration changes")
	}
	if c.thread == nil {
		if expectedGeneration != 0 {
			return errors.New("thread generation changed")
		}
		if err := InitializeThread(ctx, c.store, c.options.Owner, spec); err != nil {
			return err
		}
		thread, err := CurrentThread(ctx, c.store)
		if err != nil {
			return err
		}
		c.thread = thread
	} else {
		thread, err := ConfigureNextGeneration(ctx, c.store, expectedGeneration, spec)
		if err != nil {
			return err
		}
		c.thread = &thread
	}
	return c.appendTurnLocked(ctx)
}
func (c *Conversation) Instructions(ctx context.Context) (Rules, error) {
	return c.store.Instructions(ctx)
}

// Thread returns an independent persisted configuration copy for owner editors.
func (c *Conversation) Thread(ctx context.Context) (*core.Thread, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CurrentThread(ctx, c.store)
}
func (c *Conversation) SaveInstructions(ctx context.Context, text, revision string) (Rules, error) {
	return c.store.SaveInstructions(ctx, text, revision)
}
func (c *Conversation) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.stop()
		if c.runCancel != nil {
			c.runCancel()
		}
		c.mu.Unlock()
		<-c.done
		<-c.idleDone
		c.mu.Lock()
		if c.turn != nil && c.turn.State == "queued" {
			c.turn = nil
			c.closeErr = c.appendTurnLocked(context.Background())
		}
		c.mu.Unlock()
		c.closeErr = errors.Join(c.closeErr, c.store.Close())
	})
	return c.closeErr
}

// ClaimModel is called by the owning Control under the fixed model route before
// its network claim. Every count is durable and never reclaimed after uncertainty.
func (c *Conversation) ClaimModel(ctx context.Context, turn string, generation uint64, id string) error {
	if !core.Identifier(id) {
		return errors.New("invalid model request identity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn == nil || c.turn.Request.RequestID != turn || c.turn.Generation != generation || c.turn.Cancelling || c.turn.State != "running" || c.turn.BootID != c.options.BootID {
		return errors.New("stale or cancelled model attempt")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_model_attempts").Scan(&count); err != nil {
			return err
		}
		if c.options.MaxRequests > 0 && uint64(count) >= c.options.MaxRequests {
			return errors.New("lifetime model request budget exhausted")
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_model_attempts WHERE turn_id=?", turn).Scan(&count); err != nil {
			return err
		}
		if c.options.MaxTurnRequests > 0 && uint64(count) >= c.options.MaxTurnRequests {
			return errors.New("per-turn model request budget exhausted")
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO assistant_model_attempts VALUES (?,?,?,?,?)", id, c.options.Owner, generation, turn, storage.Now())
		return err
	})
	if err != nil {
		return err
	}
	c.modelCount++
	if c.options.MaxRequests > 0 {
		budget := protocol.RequestBudget{Used: c.modelCount, Limit: c.options.MaxRequests, MaxOutputTokens: c.thread.Config.Params.MaxOutputTokens}
		event, err := c.store.AppendEvent(ctx, protocol.Event{Type: "budget", Budget: &budget}, nil)
		if err != nil {
			return err
		}
		c.cursor = *event.Cursor
		c.changedLocked()
	}
	return nil
}
func (c *Conversation) Configured() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.thread != nil }
func (c *Conversation) Identity(request protocol.SendMessage) execution.Identity {
	c.mu.Lock()
	defer c.mu.Unlock()
	generation := uint64(0)
	if c.turn != nil && c.turn.Request.RequestID == request.RequestID {
		generation = c.turn.Generation
	}
	role := "assistant"
	if strings.HasPrefix(request.RequestID, "pot-result-") {
		role = "summary"
	}
	return execution.Identity{Owner: c.options.Owner, RequestID: request.RequestID, Generation: generation, Role: role}
}
func (c *Conversation) Failed(err error) { c.mu.Lock(); c.lastError = err; c.mu.Unlock() }
func (c *Conversation) Execute(ctx context.Context, request protocol.SendMessage) execution.Result {
	c.mu.Lock()
	if c.turn == nil || !c.turn.Request.Equal(request) || c.turn.Cancelling || c.blocked {
		c.mu.Unlock()
		return execution.Result{Phase: execution.Cancelled, Err: context.Canceled}
	}
	if !c.turn.Resuming && c.turn.State == "queued" && !strings.HasPrefix(request.RequestID, "pot-result-") && !isCompactRequest(request.RequestID) {
		if err := c.aggregateUsersLocked(ctx); err != nil {
			c.mu.Unlock()
			return execution.Result{Phase: execution.Failed, Err: err}
		}
	}
	if isCompactRequest(request.RequestID) {
		if err := c.startIdleLocked(ctx); err != nil {
			c.mu.Unlock()
			return execution.Result{Phase: execution.Cancelled, Err: err}
		}
	}
	var compactSchedule json.RawMessage
	if isCompactRequest(request.RequestID) {
		compactSchedule = c.idleScheduleLocked(ctx)
	}
	inputAttachments := append([]core.Attachment(nil), c.turn.Request.Attachments...)
	if len(c.turn.Inputs) > 0 {
		inputAttachments = nil
		for _, r := range c.turn.Inputs {
			inputAttachments = append(inputAttachments, r.Attachments...)
		}
	}
	inputText := c.turn.inputText()
	var modelInputs []core.ConversationInput
	if !strings.HasPrefix(request.RequestID, "pot-result-") && !isCompactRequest(request.RequestID) {
		requests, ids := c.turn.Inputs, c.turn.UserMessages
		if len(requests) == 0 {
			requests = []protocol.SendMessage{c.turn.Request}
			ids = []string{c.turn.UserMessageID}
		}
		for i, input := range requests {
			modelInputs = append(modelInputs, core.ConversationInput{ID: ids[i], Text: input.Text, Attachments: input.Attachments, References: input.References})
		}
	}
	threadPointer, loadErr := CurrentThread(ctx, c.store)
	if loadErr != nil || threadPointer == nil {
		c.mu.Unlock()
		if loadErr == nil {
			loadErr = errors.New("thread configuration missing")
		}
		return execution.Result{Phase: execution.Failed, Err: loadErr}
	}
	thread := *threadPointer
	resuming := c.turn.Resuming
	// Resume admission has already verified the original checkpoint and cleanup.
	// Restore its receiver claim before any resumed model or tool can run,
	// including a stopped allowance checkpoint retained across restart.
	if (c.turn.AttemptID != "" || c.turn.Resuming) && c.options.OnResumeStarting != nil {
		if err := c.options.OnResumeStarting(ctx, request); err != nil {
			c.mu.Unlock()
			return execution.Result{Phase: execution.Failed, Err: err}
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	runCtx = c.bindMessageSenderLocked(runCtx)
	c.runCancel = cancel
	c.finalizing = false
	c.mu.Unlock()
	defer cancel()
	options, err := c.options.Prepare(runCtx, thread, core.TurnRequest{RequestID: request.RequestID, Text: inputText, Attachments: inputAttachments, Inputs: modelInputs})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return execution.Result{Phase: execution.Cancelled, Err: err}
		}
		return execution.Result{Phase: execution.Failed, Err: err}
	}
	if options.Execution.Owner != thread.Owner || options.Services == nil || options.Cleanup == nil {
		return execution.Result{Phase: execution.Failed, Err: errors.New("invalid fixed assistant execution ports")}
	}
	if strings.HasPrefix(request.RequestID, "pot-result-") {
		options.Execution.Role = core.Summary
	}
	binding := &turnFeedback{conversation: c, thread: thread, request: request, options: options, gate: core.NewEventGate(thread.Generation, request.RequestID), toolMessages: map[string]string{}}
	cleanup := func(ctx context.Context, execution core.ExecutionContext, abort bool) error {
		if err := options.Cleanup(ctx, execution, abort); err != nil {
			return err
		}
		return c.recordCleanup(ctx, request.RequestID, thread.Generation)
	}
	loop := agent.Agent{MessageID: binding.messageID, Services: options.Services, Feedback: binding, Dispatch: options.Dispatch, Cleanup: cleanup, Composer: options.Composer, Tools: options.Tools, PrepareContext: options.PrepareContext, ResolveAttachments: options.ResolveAttachments}
	if isCompactRequest(request.RequestID) {
		loop.CompactOnly = true
		loop.CompactSchedule = compactSchedule
		var schedule struct {
			ExpiresMS int64 `json:"expires_ms"`
		}
		if err = json.Unmarshal(compactSchedule, &schedule); err != nil {
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		loop.CompactDeadline = time.UnixMilli(schedule.ExpiresMS)
		c.mu.Lock()
		var raw string
		err = c.store.DB().QueryRowContext(runCtx, "SELECT prefix FROM assistant_idle_compaction WHERE singleton=1").Scan(&raw)
		c.mu.Unlock()
		if err != nil {
			return execution.Result{Phase: execution.Failed, Err: err}
		}
		if err = json.Unmarshal([]byte(raw), &loop.IdlePrefix); err != nil {
			return execution.Result{Phase: execution.Failed, Err: err}
		}
	}
	loop.ResolveReferences = binding.resolveReferences
	err = loop.Execute(runCtx, options.Execution, thread, core.TurnRequest{RequestID: request.RequestID, Text: inputText, Attachments: inputAttachments, Inputs: modelInputs}, resuming, binding.event)
	phase := execution.Succeeded
	if err != nil {
		phase = execution.Failed
		var uncertain *composition.UnconfirmedCleanupError
		if errors.Is(err, agent.ErrUncertainEffects) || errors.As(err, &uncertain) || binding.gate.Finish().Kind == "unknown" {
			phase = execution.Interrupted
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			phase = execution.Cancelled
		}
	}
	return execution.Result{Phase: phase, Err: err}
}
func IsWaiting(err error) bool {
	var value protocol.APIError
	if errors.As(err, &value) {
		return value.Code == "pot_token_limit"
	}
	var pointer *protocol.APIError
	return errors.As(err, &pointer) && pointer != nil && pointer.Code == "pot_token_limit"
}
func (c *Conversation) FinishTurn(ctx context.Context, request protocol.SendMessage, result execution.Result) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finishedRequest == request.RequestID && (c.turn == nil || c.turn.Request.RequestID != request.RequestID) {
		if err := c.startNextUserLocked(ctx); err != nil {
			return err
		}
		c.finishError, c.lastError = nil, result.Err
		return c.appendTurnLocked(ctx)
	}
	if c.turn == nil || c.turn.Request.RequestID != request.RequestID {
		return errors.New("turn completion binding changed")
	}
	c.runCancel = nil
	c.finalizing = true
	if isCompactRequest(request.RequestID) {
		c.lastError = nil
		if err := c.abandonIdleLocked(ctx); err != nil {
			return err
		}
		c.finishedRequest = request.RequestID
		c.finalizing, c.finishError = false, nil
		c.changedLocked()
		return c.startNextUserLocked(ctx)
	}

	if c.completion != nil {
		result = c.completion.result()
	}
	if IsWaiting(result.Err) && !c.turn.Cancelling {
		c.turn.State = "waiting"
		if err := c.appendTurnLocked(ctx); err != nil {
			return err
		}
		c.finalizing, c.finishError = false, nil
		c.lastError = result.Err
		c.changedLocked()
		return nil
	}
	if IsWaiting(result.Err) && c.turn.Cancelling {
		result = execution.Result{Phase: execution.Cancelled, Err: context.Canceled}
	}
	if err := c.recordCompletionLocked(ctx, result); err != nil {
		return err
	}
	if result.Phase == execution.Interrupted {
		c.blocked = true
	}
	if raw, err := c.store.LoadContext(ctx); err != nil {
		return err
	} else if len(raw) > 0 {
		var checkpoint struct {
			Started        *string `json:"started"`
			ModelRequestID *string `json:"model_request_id"`
		}
		if err := json.Unmarshal(raw, &checkpoint); err != nil {
			return err
		}
		if checkpoint.Started != nil || checkpoint.ModelRequestID != nil {
			c.blocked = true
		}
	}
	for _, m := range slices.Clone(c.messages) {
		if m.Role == protocol.User && m.RequestID != nil && !c.turn.includes(m.ID) {
			continue
		}
		if m.Status == protocol.Accepted || m.Status == protocol.Streaming {
			if m.Tool != nil {
				tool := *m.Tool
				diagnostic := ""
				if result.Err != nil {
					diagnostic = result.Err.Error()
				}
				tool.Apply(core.Update{Type: "turn_finished", AtMS: storage.Now(), Outcome: &core.Outcome{Kind: string(result.Phase), Error: diagnostic}})
				m.Tool = &tool
			}
			m.Status = protocol.Complete
			if result.Phase == execution.Cancelled {
				m.Status = protocol.MessageCancelled
			} else if result.Phase != execution.Succeeded {
				m.Status = protocol.Failed
			}
			if err := c.putMessageLocked(ctx, m, nil); err != nil {
				return err
			}
		}
	}
	if result.Phase != execution.Succeeded {
		// Persist one terminal reply even when preparation failed before the Agent
		// emitted message_started. The detail belongs to this turn across reloads.
		var reply protocol.Message
		for _, message := range c.messages {
			if message.ID == c.turn.ReplyMessageID {
				reply = message
				break
			}
		}
		if reply.ID == "" {
			digest, _ := core.HashJSON([]string{"completion", c.turn.attemptID()})
			reply = protocol.Message{ID: "finish-" + digest, ConversationID: c.options.ConversationID, Role: protocol.Assistant, CreatedAtMS: storage.Now()}
		}
		reply.ReplyTo = c.turn.UserMessageID
		reply.Status = protocol.Failed
		if result.Phase == execution.Cancelled {
			reply.Status = protocol.MessageCancelled
		} else if result.Err != nil {
			reply.Error = result.Err.Error()
		}
		if err := c.putMessageLocked(ctx, reply, nil); err != nil {
			return err
		}
		c.turn.ReplyMessageID = reply.ID
	}
	if (result.Phase == execution.Failed || result.Phase == execution.Cancelled) && !c.blocked {
		raw, _ := json.Marshal(c.turn)
		if err := c.store.Set(ctx, "conversation.retry", string(raw)); err != nil {
			return err
		}
	}
	if c.blocked {
		saved := *c.turn
		c.recovery = &saved
	}
	if c.options.OnFinished != nil {
		if err := c.options.OnFinished(ctx, request, result); err != nil {
			return err
		}
	}
	previous := c.turn
	c.turn = nil
	if err := c.appendTurnMetadataLocked(ctx, map[string]string{"conversation.completion": "null"}); err != nil {
		c.turn = previous
		return err
	}
	c.completion = nil
	c.finishedRequest = request.RequestID
	c.finalizing, c.finishError = false, nil
	c.lastError = result.Err
	c.changedLocked()
	return c.startNextUserLocked(ctx)
}

// Retire fences new inputs atomically with an idle owner's deletion commit.
func (c *Conversation) Retire(commit func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn != nil || c.finalizing {
		return protocol.APIError{Code: "assistant_busy"}
	}
	if err := commit(); err != nil {
		return err
	}
	c.closed = true
	c.stop()
	return nil
}
