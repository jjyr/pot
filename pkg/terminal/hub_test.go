package terminal

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShellDetachReplayResizeOwnerIsolationAndShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := New()
	defer h.Close(ctx)
	info, err := h.Create("alpha", t.TempDir(), Create{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Get("beta", info.ID); err == nil {
		t.Fatal("terminal crossed owner scope")
	}
	s, err := h.Get("alpha", info.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, output, detach, err := s.Attach()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.Attach(); err == nil {
		t.Fatal("duplicate attachment accepted")
	}
	if err = s.Resize(Size{101, 33}); err != nil {
		t.Fatal(err)
	}
	if err = s.Write([]byte("printf 'terminal-\\165nicode-\\344\\270\\255\\346\\226\\207\\n'; stty size\n")); err != nil {
		t.Fatal(err)
	}
	text := ""
	for !strings.Contains(text, "terminal-unicode-中文") || !strings.Contains(text, "33 101") {
		select {
		case <-output:
			data, ok := s.Drain()
			if !ok {
				t.Fatal("terminal client fell behind")
			}
			text += string(data)
		case <-ctx.Done():
			t.Fatal("missing PTY output", text)
		}
	}
	detach()
	select {
	case <-s.Done():
		t.Fatal("detach stopped shell")
	default:
	}
	history, _, detach, err := s.Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	if !strings.Contains(string(history), "terminal-unicode-中文") {
		t.Fatal("reconnect lost output")
	}
	if err = h.CloseSession(ctx, "alpha", info.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.List("alpha")) != 0 {
		t.Fatal("closed terminal retained")
	}
	if err = h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Create("alpha", t.TempDir(), Create{Cols: 80, Rows: 24}); err == nil {
		t.Fatal("shutdown admitted shell")
	}
}

func TestTerminalRejectsAgentCommandsAndInvalidSize(t *testing.T) {
	h := New()
	defer h.Close(context.Background())
	command := "command"
	for _, r := range []Create{{Cols: 0, Rows: 24}, {Cols: 80, Rows: 301}, {Cols: 80, Rows: 24, Agent: &command}, {Cols: 80, Rows: 24, Purpose: "agent"}} {
		if _, err := h.Create("alpha", t.TempDir(), r); err == nil {
			t.Fatal("invalid shell admitted", r)
		}
	}
}
