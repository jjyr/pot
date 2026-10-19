// Package relay moves Assistant envelopes between existing authenticated App
// connections. Endpoints own all durable state; this package has no database,
// credential, HTTP, UI, or execution dependency.
package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/protocol"
)

type Endpoint interface {
	NodeID() string
	Directory(context.Context) (protocol.Directory, error)
	Register(context.Context, protocol.Directory) error
	Pending(context.Context, string) ([]protocol.Envelope, error)
	Deliver(context.Context, protocol.Envelope) (protocol.Receipt, error)
	Acknowledge(context.Context, protocol.Receipt) error
}

// Exchange performs one bounded pass, attempting both directions independently.
// A failed delivery never acknowledges its source, and a later pass redelivers
// the immutable original envelope. UI selection does not participate in routing.
func Exchange(ctx context.Context, a, b Endpoint) error { _, err := Pass(ctx, a, b); return err }

// Pass reports whether either bounded batch was full, so the App can continue
// draining queued work without polling an idle, connected Control.
func Pass(ctx context.Context, a, b Endpoint) (bool, error) {
	if a == nil || b == nil {
		return false, errors.New("relay requires two endpoints")
	}
	aid, bid := a.NodeID(), b.NodeID()
	if aid == bid || !(protocol.AgentRef{NodeID: aid, Kind: "assistant", AgentID: "identity"}).Valid() || !(protocol.AgentRef{NodeID: bid, Kind: "assistant", AgentID: "identity"}).Valid() {
		return false, errors.New("relay endpoints require distinct valid Control identities")
	}
	type directoryResult struct {
		value protocol.Directory
		err   error
	}
	ad, bd := make(chan directoryResult, 1), make(chan directoryResult, 1)
	go func() { value, err := a.Directory(ctx); ad <- directoryResult{value, err} }()
	go func() { value, err := b.Directory(ctx); bd <- directoryResult{value, err} }()
	av, bv := <-ad, <-bd
	if err := errors.Join(av.err, bv.err); err != nil {
		return false, fmt.Errorf("relay directories: %w", err)
	}
	if av.value.NodeID != aid || bv.value.NodeID != bid || a.NodeID() != aid || b.NodeID() != bid {
		return false, errors.New("relay Control identity changed")
	}
	if err := errors.Join(validateDirectory(av.value), validateDirectory(bv.value)); err != nil {
		return false, err
	}
	registered := make(chan error, 2)
	go func() { registered <- a.Register(ctx, bv.value) }()
	go func() { registered <- b.Register(ctx, av.value) }()
	if err := errors.Join(<-registered, <-registered); err != nil {
		return false, fmt.Errorf("relay directory registration: %w", err)
	}
	type passResult struct {
		full bool
		err  error
	}
	transfers := make(chan passResult, 2)
	go func() { full, err := transfer(ctx, a, b, aid, bid); transfers <- passResult{full, err} }()
	go func() { full, err := transfer(ctx, b, a, bid, aid); transfers <- passResult{full, err} }()
	forward, backward := <-transfers, <-transfers
	return forward.full || backward.full, errors.Join(forward.err, backward.err)
}
func validateDirectory(value protocol.Directory) error {
	if len(value.Assistants) > 1024 {
		return errors.New("relay directory exceeds 1024 assistants")
	}
	seen := map[protocol.AgentRef]bool{}
	for _, assistant := range value.Assistants {
		if !assistant.Address.Valid() || assistant.Address.NodeID != value.NodeID || seen[assistant.Address] || !utf8.ValidString(assistant.Name) || strings.TrimSpace(assistant.Name) == "" || utf8.RuneCountInString(assistant.Name) > 128 {
			return errors.New("invalid relay assistant directory")
		}
		seen[assistant.Address] = true
	}
	return nil
}
func clone(message protocol.Envelope) protocol.Envelope {
	if message.ReplyTo != nil {
		reply := *message.ReplyTo
		message.ReplyTo = &reply
	}
	return message
}
func equal(a, b protocol.Envelope) bool {
	sameReply := a.ReplyTo == nil && b.ReplyTo == nil || a.ReplyTo != nil && b.ReplyTo != nil && *a.ReplyTo == *b.ReplyTo
	return a.Kind == b.Kind && a.Owner == b.Owner && a.Digest == b.Digest && a.RequestID == b.RequestID && a.ID == b.ID && a.From == b.From && a.To == b.To && a.ConversationID == b.ConversationID && sameReply && a.Text == b.Text && a.Hops == b.Hops
}
func transfer(ctx context.Context, source, target Endpoint, sourceID, targetID string) (bool, error) {
	if source.NodeID() != sourceID || target.NodeID() != targetID {
		return false, errors.New("relay Control identity changed")
	}
	messages, err := source.Pending(ctx, targetID)
	if err != nil {
		return false, fmt.Errorf("relay pending %s: %w", sourceID, err)
	}
	if len(messages) > protocol.MailPageSize {
		return false, errors.New("relay batch exceeds protocol limit")
	}
	failures := []error{}
	for _, value := range messages {
		if err := ctx.Err(); err != nil {
			return false, errors.Join(append(failures, err)...)
		}
		message := clone(value)
		if err := message.Validate(); err != nil || message.From.NodeID != sourceID || message.To.NodeID != targetID {
			failures = append(failures, errors.New("relay message route mismatch"))
			continue
		}
		if source.NodeID() != sourceID || target.NodeID() != targetID {
			failures = append(failures, errors.New("relay Control identity changed"))
			continue
		}
		receipt, err := target.Deliver(ctx, clone(message))
		if err != nil {
			failures = append(failures, fmt.Errorf("relay deliver %s: %w", message.ID, err))
			continue
		}
		if receipt.Message.Validate() != nil || !equal(message, receipt.Message) {
			failures = append(failures, errors.New("relay delivery receipt mismatch"))
			continue
		}
		if source.NodeID() != sourceID || target.NodeID() != targetID {
			failures = append(failures, errors.New("relay Control identity changed before acknowledgement"))
			continue
		}
		if err := source.Acknowledge(ctx, protocol.Receipt{Message: clone(receipt.Message)}); err != nil {
			failures = append(failures, fmt.Errorf("relay acknowledge %s: %w", message.ID, err))
		}
	}
	return len(messages) == protocol.MailPageSize, errors.Join(failures...)
}
