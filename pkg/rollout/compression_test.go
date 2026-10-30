package rollout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparedCompressionRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"messages":["before"]}`)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("orphan torn tail")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := PrepareCompression(context.Background(), path, ref, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plain remains", err)
	}
	exact, err := compressedExact(path+".zst", ref)
	if err != nil || !exact {
		t.Fatal("archive not exact", exact, err)
	}
	assertState(t, path, ref, `{"messages":["before"]}`)
	ref = appendState(t, path, ref, `{"messages":["before","after"]}`)
	assertState(t, path, ref, `{"messages":["before","after"]}`)
}

func TestPreparedCompressionRejectsChangedAndBusy(t *testing.T) {
	for _, mode := range []string{"append", "replace", "busy"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			ref := appendState(t, path, Ref{}, `{"n":1}`)
			c, err := PrepareCompression(context.Background(), path, ref, 0)
			if err != nil {
				t.Fatal(err)
			}
			temp := c.temp
			switch mode {
			case "append":
				ref = appendState(t, path, ref, `{"n":2}`)
			case "replace":
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path, b, 0600); e != nil {
					t.Fatal(e)
				}
			case "busy":
				unlock, e := lock(path)
				if e != nil {
					t.Fatal(e)
				}
				defer unlock()
			}
			start := time.Now()
			if err = c.Commit(context.Background()); !errors.Is(err, ErrChanged) {
				t.Fatalf("got %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("commit blocked")
			}
			if _, err = os.Stat(temp); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("temporary remains", err)
			}
			if _, err = os.Stat(path); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(path + ".zst"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("archive published", err)
			}
		})
	}
}

func TestPreparationDoesNotLockAndCancels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"value":"`+strings.Repeat("x", 100000)+`"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := PrepareCompression(ctx, path, ref, 1024)
		if c != nil {
			_ = c.Abort()
		}
		done <- err
	}()
	// Even an existing append lock cannot hold up preparation; a slow preparation
	// must likewise not acquire that lock and delay a subsequent append.
	time.Sleep(30 * time.Millisecond)
	appended := make(chan error, 1)
	go func() { _, err := Append(path, ref, []byte(`{"value":"new"}`)); appended <- err }()
	select {
	case err := <-appended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("append waited for compression")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("preparation ignored cancellation")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".rollout-compress-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary remains", files, err)
	}
}

func TestPreparationPacesAndPreservesCorruptSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"n":1}`)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c, err := PrepareCompression(context.Background(), path, ref, info.Size()*20)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Abort()
	// Three full uncompressed passes each take at least 50ms. Verification also
	// accounts for compressed input bytes.
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("reads not paced")
	}
	if err = os.WriteFile(path, []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareCompression(context.Background(), path, ref, 0); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "corrupt\n" {
		t.Fatal("source modified", err)
	}
}

func TestPreparationCancellationRemovesTemporary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"n":1}`)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, e := PrepareCompression(ctx, path, ref, info.Size()*5)
		if c != nil {
			_ = c.Abort()
		}
		done <- e
	}()
	deadline := time.After(2 * time.Second)
	for {
		files, e := filepath.Glob(filepath.Join(filepath.Dir(path), ".rollout-compress-*"))
		if e != nil {
			t.Fatal(e)
		}
		if len(files) > 0 {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("finished before cancellation: %v", e)
		case <-deadline:
			t.Fatal("temporary never created")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".rollout-compress-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary remains", files, err)
	}
	assertState(t, path, ref, `{"n":1}`)
}
