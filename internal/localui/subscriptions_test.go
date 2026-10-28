package localui

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestNativeSubscriptionRetirementAndTokenReuse(t *testing.T) {
	type stream struct {
		ctx     context.Context
		receive func(Event) error
		finish  func()
	}
	opened := make(chan stream, 8)
	caller := Functions{Request: func(_ context.Context, _, path string, _ json.RawMessage) (json.RawMessage, error) {
		switch path {
		case "/api/v1/runtime":
			return json.RawMessage(`{"runtime_id":"control","boot_id":"boot"}`), nil
		case "/api/v1/assistants":
			return json.RawMessage(`[]`), nil
		default:
			return json.RawMessage(`{"items":[]}`), nil
		}
	}, Events: func(ctx context.Context, path string, receive func(Event) error) error {
		if path == "/api/v1/changes" {
			<-ctx.Done()
			return ctx.Err()
		}
		u, _ := url.Parse(path)
		if u.Query().Get("assistant_id") != "assistant" || u.Query().Get("locale") != "zh-CN" {
			t.Errorf("lost stream scope: %s", path)
		}
		done := make(chan struct{})
		var once sync.Once
		opened <- stream{ctx, receive, func() { once.Do(func() { close(done) }) }}
		// Keep an old goroutine alive through unsubscribe/reuse, as can happen
		// while a transport is unwinding cancellation.
		<-done
		return ctx.Err()
	}}
	m, err := Open(context.Background(), Options{Local: caller, LocalID: "control"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var mu sync.Mutex
	var events []NativeEvent
	bridge := NewNativeSubscriptions(context.Background(), m, func(_ string, value NativeEvent) { mu.Lock(); defer mu.Unlock(); events = append(events, value) })
	defer bridge.Close()
	get := func() stream {
		t.Helper()
		select {
		case value := <-opened:
			return value
		case <-time.After(time.Second):
			t.Fatal("stream did not start")
			return stream{}
		}
	}
	if err := bridge.Subscribe("token", "control", "assistant", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	old := get()
	defer old.finish()
	if err := bridge.Subscribe("token", "control", "assistant", "zh-CN"); err == nil {
		t.Fatal("duplicate live token accepted")
	}
	if err := bridge.Subscribe("foreign", "other-control", "assistant", "zh-CN"); err == nil {
		t.Fatal("foreign Space accepted")
	}
	bridge.Unsubscribe("token")
	if err := old.receive(Event{Type: "conversation", Data: json.RawMessage(`{}`)}); err != context.Canceled {
		t.Fatalf("late cancelled callback: %v", err)
	}
	if err := bridge.Subscribe("token", "control", "assistant", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	newStream := get()
	defer newStream.finish()
	old.finish()
	// The retired goroutine's cleanup must not cancel or remove the replacement.
	select {
	case <-newStream.ctx.Done():
		t.Fatal("old cleanup cancelled replacement")
	default:
	}
	bridge.Unsubscribe("token")
	select {
	case <-newStream.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("replacement token was lost during old cleanup")
	}
	newStream.finish()
	bridge.Close()
	if err := bridge.Subscribe("closed", "control", "assistant", "zh-CN"); err == nil {
		t.Fatal("closed bridge restarted")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, value := range events {
		if value.Token != "token" || value.Generation != 1 || value.Status != "connecting" {
			t.Fatalf("retired stream emitted: %+v", value)
		}
	}
}
