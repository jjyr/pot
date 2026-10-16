package usage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestWaitReleaseCASAndDurableOriginalRequestReceipt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "a", AtMS: 1, Required: 1}); err != nil {
		t.Fatal(err)
	}
	a, _ := l.Waiting(ctx, "main")
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "b", AtMS: 2, Required: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = l.AdmitWaitRelease(ctx, "main", "stale", *a); err == nil {
		t.Fatal("stale original wait released replacement")
	}
	b, _ := l.Waiting(ctx, "main")
	if b.Released {
		t.Fatal(b)
	}
	if _, err = l.AdmitWaitRelease(ctx, "main", "release-b", *b); err != nil {
		t.Fatal(err)
	}
	if err = l.CompleteWaitRelease(ctx, "main", "release-b"); err != nil {
		t.Fatal(err)
	}
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "c", AtMS: 3, Required: 1}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := l.AdmitWaitRelease(ctx, "main", "release-b", *b)
	if err != nil || !duplicate.Complete {
		t.Fatal(duplicate, err)
	}
	current, _ := l.Waiting(ctx, "main")
	if current.Released || current.Turn != "c" {
		t.Fatal(current)
	}
	if _, err = l.WaitRelease(ctx, "main", "release-b", "main", "c", 3); err == nil {
		t.Fatal("request identity rebound")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	duplicate, err = l.WaitRelease(ctx, "main", "release-b", "main", "b", 2)
	if err != nil || !duplicate.Complete {
		t.Fatal(duplicate, err)
	}
}
func TestWaitReleaseDoesNotReleaseReplacementBoundaryOfSameTurn(t *testing.T) {
	ctx := context.Background()
	l, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Wait(ctx, WaitRequest{Scope: "correspondence", Turn: "same-turn", AtMS: 10, Required: 1})
	original, _ := l.Waiting(ctx, "correspondence")
	if _, err = l.AdmitWaitRelease(ctx, "owner", "first-release", *original); err != nil {
		t.Fatal(err)
	}
	l.Wait(ctx, WaitRequest{Scope: "correspondence", Turn: "same-turn", AtMS: 10, Required: 1})
	if _, err = l.AdmitWaitRelease(ctx, "owner", "release", *original); err == nil {
		t.Fatal("stale wait instance released")
	}
}

func TestWaitTokenSurvivesDeletionAndSameMillisecondRewait(t *testing.T) {
	ctx := context.Background()
	l, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "turn", AtMS: 100, Required: 1}); err != nil {
		t.Fatal(err)
	}
	original, _ := l.Waiting(ctx, "main")
	if _, err = l.AdmitWaitRelease(ctx, "main", "old", *original); err != nil {
		t.Fatal(err)
	}
	if err = l.ClearWaitForTurn(ctx, "main", "turn"); err != nil {
		t.Fatal(err)
	}
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "turn", AtMS: 100, Required: 1}); err != nil {
		t.Fatal(err)
	}
	replacement, _ := l.Waiting(ctx, "main")
	if replacement.Since <= original.Since {
		t.Fatal("wait token reused after delete", original, replacement)
	}
	if _, err = l.AdmitWaitRelease(ctx, "main", "stale", *original); err == nil {
		t.Fatal("old snapshot released replacement boundary")
	}
}
