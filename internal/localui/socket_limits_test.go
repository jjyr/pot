package localui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestGenericPathPreservation(t *testing.T) {
	ctx := testCtx(t)
	type called struct {
		method string
		path   string
	}
	got := make(chan called, 3)
	fc := &fakeCaller{call: func(_ context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		got <- called{method: method, path: path}
		return json.RawMessage(`{"ok":true}`), nil
	}}
	c, done := connect(t, ctx, SocketOptions{
		Caller:             fc,
		InsecureSkipVerify: true,
	}, nil)
	defer done()

	valid := []struct {
		id     string
		method string
		path   string
	}{
		{`v1`, `GET`, `/api/preview/session?space_id=node&x=a%3Ab`},
		{`v2`, `GET`, `/oauth/consent`},
		{`v3`, `GET`, `/api/v1/agent-links/link%3Aowner`},
	}
	for _, tc := range valid {
		writeMsg(t, ctx, c, outMsg{Type: `request`, ID: tc.id, Method: tc.method, Path: tc.path})
		m := readMsg(t, ctx, c)
		if m.Type != `response` || m.ID != tc.id || m.Status != http.StatusOK {
			t.Fatalf(`valid response %s: %+v`, tc.id, m)
		}
		select {
		case a := <-got:
			if a.method != tc.method || a.path != tc.path {
				t.Fatalf(`caller path mismatch: got %s %s want %s %s`, a.method, a.path, tc.method, tc.path)
			}
		case <-ctx.Done():
			t.Fatalf(`caller not invoked for %s`, tc.id)
		}
	}
	if n := atomic.LoadInt32(&fc.callCount); n != 3 {
		t.Fatalf(`valid call count = %d`, n)
	}

	invalid := []string{
		`api/v1/agent-links/x`,
		`http://evil.example/api/v1/agent-links/x`,
		`//evil.example/api/v1/agent-links/x`,
		`/api/v1/agent-links/../secret`,
		`/api/v1/agent-links/%2e%2e/secret`,
		`/api/v1/agent-links/a%2Fb`,
		`/api/v1/agent-links/a%5Cb`,
	}
	for i, path := range invalid {
		before := atomic.LoadInt32(&fc.callCount)
		writeMsg(t, ctx, c, outMsg{Type: `request`, ID: fmt.Sprintf(`bad%d`, i), Method: `GET`, Path: path})
		m := readMsg(t, ctx, c)
		if m.Status < 400 {
			t.Fatalf(`invalid path accepted %q: %+v`, path, m)
		}
		if after := atomic.LoadInt32(&fc.callCount); after != before {
			t.Fatalf(`caller invoked for invalid path %q: before=%d after=%d`, path, before, after)
		}
	}
	if n := atomic.LoadInt32(&fc.callCount); n != 3 {
		t.Fatalf(`final call count = %d`, n)
	}
}

func TestStaleOpIDReuse(t *testing.T) {
	ctx := testCtx(t)
	started := make(chan struct{}, 1)
	slowRelease := make(chan struct{})
	exited := make(chan struct{})
	var releaseOnce sync.Once
	releaseSlow := func() { releaseOnce.Do(func() { close(slowRelease) }) }
	defer releaseSlow()
	fc := &fakeCaller{call: func(_ context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		switch path {
		case `/slow`:
			started <- struct{}{}
			<-slowRelease
			close(exited)
			return json.RawMessage(`{"old":true}`), nil
		case `/fast`:
			return json.RawMessage(`{"fast":true}`), nil
		default:
			return nil, fmt.Errorf(`unexpected path %q`, path)
		}
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()

	writeMsg(t, ctx, c, outMsg{Type: `request`, ID: `r`, Method: `GET`, Path: `/slow`})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(`slow request did not start`)
	}

	writeMsg(t, ctx, c, outMsg{Type: `cancel`, ID: `r`})
	m := readMsg(t, ctx, c)
	if m.Type != `response` || m.ID != `r` || m.Status != http.StatusOK {
		t.Fatalf(`cancel ack: %+v`, m)
	}

	writeMsg(t, ctx, c, outMsg{Type: `request`, ID: `r`, Method: `GET`, Path: `/fast`})
	m = readMsg(t, ctx, c)
	if m.Type != `response` || m.ID != `r` || m.Status != http.StatusOK || !strings.Contains(string(m.Result), `fast`) {
		t.Fatalf(`fresh reused id response: %+v`, m)
	}

	releaseSlow()
	select {
	case <-exited:
	case <-ctx.Done():
		t.Fatal(`slow caller did not exit`)
	}

	writeMsg(t, ctx, c, outMsg{Type: `request`, ID: `next`, Method: `GET`, Path: `/fast`})
	for {
		m = readMsg(t, ctx, c)
		if m.ID == `r` {
			t.Fatalf(`stale response leaked before barrier: %+v`, m)
		}
		if m.ID == `next` {
			if m.Type != `response` || m.Status != http.StatusOK || !strings.Contains(string(m.Result), `fast`) {
				t.Fatalf(`barrier response: %+v`, m)
			}
			break
		}
	}
}

func TestBoundedCancelledWorkload(t *testing.T) {
	ctx := testCtx(t)
	const n = 64
	started := make(chan struct{}, n)
	callerDone := make(chan struct{}, n)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseAll()

	fc := &fakeCaller{call: func(_ context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		switch path {
		case `/slow`:
			started <- struct{}{}
			<-release
			callerDone <- struct{}{}
			return json.RawMessage(`{"old":true}`), nil
		case `/fast`:
			return json.RawMessage(`{"fast":true}`), nil
		default:
			return nil, fmt.Errorf(`unexpected path %q`, path)
		}
	}}
	opts := SocketOptions{Caller: fc, InsecureSkipVerify: true}
	c, done := connect(t, ctx, opts, nil)
	defer done()

	for i := 0; i < n; i++ {
		writeMsg(t, ctx, c, outMsg{Type: `request`, ID: fmt.Sprintf(`r%d`, i), Method: `GET`, Path: `/slow`})
	}
	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatalf(`only %d/%d slow requests started`, i, n)
		}
	}

	for i := 0; i < n; i++ {
		writeMsg(t, ctx, c, outMsg{Type: `cancel`, ID: fmt.Sprintf(`r%d`, i)})
	}
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		m := readMsg(t, ctx, c)
		if m.Type != `response` || m.Status != http.StatusOK || !strings.HasPrefix(m.ID, `r`) {
			t.Fatalf(`cancel ack %d: %+v`, i, m)
		}
		seen[m.ID] = true
	}
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf(`r%d`, i)] {
			t.Fatalf(`missing cancel ack r%d`, i)
		}
	}

	writeMsg(t, ctx, c, outMsg{Type: `request`, ID: `overflow`, Method: `GET`, Path: `/fast`})
	m := readMsg(t, ctx, c)
	if m.Type != `response` || m.ID != `overflow` || m.Status != http.StatusTooManyRequests {
		t.Fatalf(`overflow response: %+v`, m)
	}

	releaseAll()
	for i := 0; i < n; i++ {
		select {
		case <-callerDone:
		case <-ctx.Done():
			t.Fatalf(`caller did not finish: %d/%d`, i, n)
		}
	}
}

func TestStreamEndErrorStatus(t *testing.T) {
	ctx := testCtx(t)
	fc := &fakeCaller{stream: func(_ context.Context, path string, receive func(Event) error) error {
		if err := receive(Event{Type: `change`, ID: `e1`, Data: json.RawMessage(`{"n":1}`)}); err != nil {
			return err
		}
		return SocketError{Status: http.StatusForbidden, APIError: protocol.APIError{Code: `forbidden`, Message: `forbidden`}}
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()

	writeMsg(t, ctx, c, outMsg{Type: `subscribe`, ID: `s`, Path: `/api/v1/ev`})
	m := readMsg(t, ctx, c)
	if m.Type != `response` || m.ID != `s` || m.Status != http.StatusOK {
		t.Fatalf(`subscribe ack: %+v`, m)
	}
	m = readMsg(t, ctx, c)
	if m.Type != `event` || m.ID != `s` || m.Event != `change` {
		t.Fatalf(`event: %+v`, m)
	}
	m = readMsg(t, ctx, c)
	if m.Type != `end` || m.ID != `s` || m.Status != http.StatusForbidden {
		t.Fatalf(`end status: %+v`, m)
	}
	if !strings.Contains(string(m.Error), `forbidden`) {
		t.Fatalf(`end error: %+v`, m)
	}
}
