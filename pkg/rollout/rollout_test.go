package rollout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appendState(t *testing.T, path string, ref Ref, state string) Ref {
	t.Helper()
	r, e := Append(path, ref, json.RawMessage(state))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func assertState(t *testing.T, path string, ref Ref, want string) {
	t.Helper()
	got, e := Load(path, ref)
	if e != nil {
		t.Fatal(e)
	}
	canonicalWant, e := canonical(json.RawMessage(want))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, canonicalWant) {
		t.Fatalf("state\n got %s\nwant %s", got, canonicalWant)
	}
}
func TestDeltaCompactionAndResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	states := []string{`{"type":"context","version":2,"messages":[{"text":"a"}],"receipts":[],"opaque":{"n":9007199254740993}}`, `{"type":"context","version":2,"messages":[{"text":"a"},{"text":"b"}],"receipts":[{"id":"tool"}],"pending":[1],"opaque":{"n":9007199254740993}}`, `{"type":"context","version":2,"messages":[{"text":"summary"}],"receipts":[{"id":"tool"}],"started":null}`, `{"type":"context","version":2,"messages":[],"receipts":[],"pending":[]}`}
	var ref Ref
	for _, state := range states {
		ref = appendState(t, path, ref, state)
		assertState(t, path, ref, state)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if err := Compress(path, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plain remains", err)
	}
	assertState(t, path, ref, states[len(states)-1])
	ref = appendState(t, path, ref, `{"messages":["resumed"],"version":2}`)
	assertState(t, path, ref, `{"messages":["resumed"],"version":2}`)
	after, _ := os.ReadFile(path)
	if !bytes.HasPrefix(after, before) {
		t.Fatal("resume rewrote prior records")
	}
}
func TestCommittedPrefixAndTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	first := appendState(t, path, Ref{}, `{"messages":[1]}`)
	second := appendState(t, path, first, `{"messages":[1,2]}`)
	assertState(t, path, first, `{"messages":[1]}`)
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString(`{"torn`)
	f.Close()
	assertState(t, path, second, `{"messages":[1,2]}`)
	if err := Compress(path, first); err != nil {
		t.Fatal(err)
	}
	assertState(t, path, first, `{"messages":[1]}`)
	next := appendState(t, path, first, `{"messages":[1,3]}`)
	assertState(t, path, next, `{"messages":[1,3]}`)
	if _, err := Load(path, second); err == nil {
		t.Fatal("discarded tail reference accepted")
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("torn")) {
		t.Fatal("torn tail retained")
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, next); err == nil {
		t.Fatal("committed missing newline accepted")
	}
}
func TestMissingCorruptionAndReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"messages":["original"]}`)
	original, _ := os.ReadFile(path)
	stat, _ := os.Stat(path)
	changed := bytes.Replace(original, []byte("original"), []byte("modified"), 1)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	// Restoring mtime must not allow the hot cache to conceal changed content.
	if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, ref, json.RawMessage(`{"messages":[]}`)); err == nil {
		t.Fatal("cache concealed corruption")
	}
	if _, err := Load(path, ref); err == nil {
		t.Fatal("corruption accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, ref, json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing committed file reset")
	}
	for _, bad := range []Ref{{Sequence: 1}, {Hash: strings.Repeat("a", 64)}, {Sequence: -1, Hash: "x"}} {
		if _, err := Load(path, bad); err == nil {
			t.Fatal("invalid reference accepted", bad)
		}
	}
}
func TestImportAndGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var records []string
	var messages []string
	for i := range 100 {
		messages = append(messages, strings.Repeat(fmt.Sprintf("message-%03d ", i), 50))
		b, _ := json.Marshal(map[string]any{"messages": messages, "receipts": []string{}, "version": 2})
		records = append(records, string(b))
	}
	index := 0
	ref, err := AppendMany(path, Ref{}, func() (json.RawMessage, error) {
		if index == len(records) {
			return nil, io.EOF
		}
		v := records[index]
		index++
		return json.RawMessage(v), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertState(t, path, ref, records[len(records)-1])
	info, _ := os.Stat(path)
	if info.Size() > int64(len(records[len(records)-1])*3) {
		t.Fatalf("rollout duplicates full histories: %d", info.Size())
	}
	firstSize := info.Size()
	_, err = AppendMany(path, ref, func() (json.RawMessage, error) { return nil, errors.New("SQL cursor failed") })
	if err == nil {
		t.Fatal("import error lost")
	}
	assertState(t, path, ref, records[len(records)-1])
	info, _ = os.Stat(path)
	if info.Size() != firstSize {
		t.Fatal("failed empty import changed prefix")
	}
}
func TestCompressedCorruptionAndOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"messages":["first"]}`)
	old, _ := os.ReadFile(path)
	if err := Compress(path, ref); err != nil {
		t.Fatal(err)
	}
	compressed, _ := os.ReadFile(path + ".zst")
	// A crash may leave both representations. A valid plain file is authoritative.
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	ref = appendState(t, path, ref, `{"messages":["first","second"]}`)
	assertState(t, path, ref, `{"messages":["first","second"]}`)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ref); err == nil {
		t.Fatal("stale compressed state accepted")
	}
	compressed[len(compressed)-5] ^= 0xff
	if err := os.WriteFile(path+".zst", compressed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, Ref{Sequence: 1, Hash: ref.Hash}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("corrupt zstd restored")
	}
}
func TestEmptyObjectsAndNoMutationOfPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{}`)
	assertState(t, path, ref, `{}`)
	ref = appendState(t, path, ref, `{"null":null,"array":[],"object":{}}`)
	assertState(t, path, ref, `{"null":null,"array":[],"object":{}}`)
	ref = appendState(t, path, ref, `{}`)
	assertState(t, path, ref, `{}`)
}
func TestSymlinkRefusal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, Ref{}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("symlink followed")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "safe" {
		t.Fatal("symlink target modified")
	}
}
func TestCacheRollbackAndExternalReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	first := appendState(t, path, Ref{}, `{"messages":[1]}`)
	_ = appendState(t, path, first, `{"messages":[1,2]}`)
	next := appendState(t, path, first, `{"messages":[1,3]}`)
	assertState(t, path, next, `{"messages":[1,3]}`)
	info, _ := os.Stat(path)
	original, _ := os.ReadFile(path)
	replacement := path + ".new"
	_ = os.WriteFile(replacement, bytes.Replace(original, []byte(`[1]`), []byte(`[9]`), 1), 0600)
	_ = os.Chtimes(replacement, time.Now(), info.ModTime())
	_ = os.Rename(replacement, path)
	if _, err := Append(path, next, json.RawMessage(`{}`)); err == nil {
		t.Fatal("replacement hidden by cache")
	}
}

func TestImportPreservesTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	index := 0
	ref, err := AppendManyAt(path, Ref{}, func() (json.RawMessage, int64, error) {
		index++
		if index > 2 {
			return nil, 0, io.EOF
		}
		return json.RawMessage(fmt.Sprintf(`{"messages":[%d]}`, index)), int64(index * 1234), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i, line := range lines {
		var value record
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value.AtMS != int64((i+1)*1234) {
			t.Fatal("timestamp not retained", value.AtMS)
		}
	}
	changed := bytes.Replace(data, []byte(`"at_ms":1234`), []byte(`"at_ms":1235`), 1)
	_ = os.WriteFile(path, changed, 0600)
	if _, err := Load(path, ref); err == nil {
		t.Fatal("timestamp mutation escaped hash verification")
	}
}

func TestZstdChecksumOnLoadAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"messages":["payload"]}`)
	if err := Compress(path, ref); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path + ".zst")
	data[len(data)-1] ^= 1
	_ = os.WriteFile(path+".zst", data, 0600)
	if _, err := Load(path, ref); err == nil {
		t.Fatal("zstd checksum corruption accepted")
	}
	if _, err := Append(path, ref, json.RawMessage(`{"messages":[]}`)); err == nil {
		t.Fatal("zstd checksum corruption restored")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed restore published plain log")
	}
}

func TestCompressionIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"messages":["cold"]}`)
	if err := Compress(path, ref); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path + ".zst")
	if err != nil {
		t.Fatal(err)
	}
	if err := Compress(path, ref); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path + ".zst")
	if !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		t.Fatal("cold rollout rewritten")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cold rollout materialized")
	}
}

func TestFailedBatchCanRetryOriginalReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	committed := appendState(t, path, Ref{}, `{"messages":["committed"]}`)
	count := 0
	_, err := AppendMany(path, committed, func() (json.RawMessage, error) {
		count++
		if count == 1 {
			return json.RawMessage(`{"messages":["committed","orphan"]}`), nil
		}
		return nil, errors.New("interrupted migration")
	})
	if err == nil {
		t.Fatal("missing import failure")
	}
	assertState(t, path, committed, `{"messages":["committed"]}`)
	next := appendState(t, path, committed, `{"messages":["committed","retry"]}`)
	assertState(t, path, next, `{"messages":["committed","retry"]}`)
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("orphan")) {
		t.Fatal("uncommitted batch retained")
	}
}

func TestAppendHonorsExclusiveFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	committed := appendState(t, path, Ref{}, `{"messages":[1]}`)
	release, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := Append(path, committed, json.RawMessage(`{"messages":[1,2]}`))
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		release()
		t.Fatalf("append bypassed exclusive lock: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("append did not resume after unlock")
	}
}

func TestZstdTruncationPreservesArchive(t *testing.T) {
	for _, cut := range []int{1, 3, 8} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			ref := appendState(t, path, Ref{}, `{"messages":["payload"]}`)
			if err := Compress(path, ref); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path + ".zst")
			if err != nil {
				t.Fatal(err)
			}
			data = data[:len(data)-cut]
			if err = os.WriteFile(path+".zst", data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = Load(path, ref); err == nil {
				t.Fatal("truncated archive loaded")
			}
			if _, err = Append(path, ref, json.RawMessage(`{"messages":[]}`)); err == nil {
				t.Fatal("truncated archive restored")
			}
			if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed restore published plain log", err)
			}
			after, err := os.ReadFile(path + ".zst")
			if err != nil || !bytes.Equal(data, after) {
				t.Fatal("archive changed on failure", err)
			}
		})
	}
}

func TestZstdStreamingBeyondWindow(t *testing.T) {
	// The streaming decoder memory/window limits must not cap total session size.
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ref := appendState(t, path, Ref{}, `{"n":0}`)
	for n := 1; n < 20; n++ {
		ref = appendState(t, path, ref, `{"n":`+fmt.Sprint(n)+`}`)
	}
	large := `{"text":"` + strings.Repeat("abcdefgh", (archiveWindowSize+(1<<20))/8) + `","n":20}`
	ref = appendState(t, path, ref, large)
	// Exercise staged compression as well as streaming load and restoration.
	c, err := PrepareCompression(context.Background(), path, ref, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(loaded, []byte(`"n":20`)) || len(loaded) < archiveWindowSize {
		t.Fatal("large context not restored")
	}
	ref = appendState(t, path, ref, `{"n":21}`)
	assertState(t, path, ref, `{"n":21}`)
}
