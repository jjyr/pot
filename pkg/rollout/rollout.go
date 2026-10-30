// Package rollout stores recoverable context as hash-chained JSONL deltas.
// The caller owns the committed Ref (usually in SQLite) and must serialize the
// append and its reference commit. Bytes beyond that reference are uncommitted.
package rollout

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const maxRecordSize = 256 << 20

var (
	ErrCorrupt = errors.New("corrupt rollout")
)

// Ref identifies the last durably committed record, not necessarily the file's
// last record. The zero value denotes an empty history.
type Ref struct {
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

func (r Ref) IsZero() bool { return r.Sequence == 0 && r.Hash == "" }
func (r Ref) validate() error {
	if r.IsZero() {
		return nil
	}
	b, err := hex.DecodeString(r.Hash)
	if r.Sequence <= 0 || err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != r.Hash {
		return fmt.Errorf("%w: invalid reference", ErrCorrupt)
	}
	return nil
}

type object map[string]json.RawMessage

type arrayEdit struct {
	Keep   int               `json:"keep"`
	Append []json.RawMessage `json:"append"`
}
type delta struct {
	Set    object               `json:"set,omitempty"`
	Remove []string             `json:"remove,omitempty"`
	Arrays map[string]arrayEdit `json:"arrays,omitempty"`
}
type payload struct {
	AtMS       int64   `json:"at_ms"`
	Version    int     `json:"version"`
	Sequence   int64   `json:"sequence"`
	Previous   string  `json:"previous"`
	Checkpoint *object `json:"checkpoint,omitempty"`
	Delta      *delta  `json:"delta,omitempty"`
}
type record struct {
	payload
	Hash string `json:"hash"`
}

func digest(p payload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func canonical(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	return json.Marshal(value)
}
func decodeState(raw json.RawMessage) (object, error) {
	b, err := canonical(raw)
	if err != nil {
		return nil, err
	}
	var state object
	if err = json.Unmarshal(b, &state); err != nil || state == nil {
		return nil, errors.New("rollout state must be a JSON object")
	}
	return state, nil
}
func difference(previous, next object) delta {
	d := delta{Set: object{}, Arrays: map[string]arrayEdit{}}
	for key, value := range next {
		old, ok := previous[key]
		if ok && bytes.Equal(old, value) {
			continue
		}
		if ok && len(old) > 0 && old[0] == '[' && len(value) > 0 && value[0] == '[' {
			var a, b []json.RawMessage
			_ = json.Unmarshal(old, &a)
			_ = json.Unmarshal(value, &b)
			common := 0
			for common < len(a) && common < len(b) && bytes.Equal(a[common], b[common]) {
				common++
			}
			tail := append([]json.RawMessage{}, b[common:]...)
			d.Arrays[key] = arrayEdit{Keep: common, Append: tail}
		} else {
			d.Set[key] = value
		}
	}
	for key := range previous {
		if _, ok := next[key]; !ok {
			d.Remove = append(d.Remove, key)
		}
	}
	sort.Strings(d.Remove)
	return d
}

// Array fields remain decoded while replaying: growing histories are not
// marshaled and parsed again at every delta, only once at the recovery boundary.
func applyBuffered(state object, arrays map[string][]json.RawMessage, d delta) error {
	seen := map[string]bool{}
	for _, key := range d.Remove {
		if seen[key] {
			return errors.New("duplicate delta key")
		}
		seen[key] = true
		delete(state, key)
		delete(arrays, key)
	}
	for key, value := range d.Set {
		if seen[key] {
			return errors.New("conflicting delta key")
		}
		seen[key] = true
		state[key] = value
		delete(arrays, key)
	}
	for key, edit := range d.Arrays {
		if seen[key] {
			return errors.New("conflicting delta key")
		}
		items, ok := arrays[key]
		if !ok {
			old := state[key]
			if len(old) == 0 || old[0] != '[' {
				return errors.New("array edit on non-array")
			}
			if err := json.Unmarshal(old, &items); err != nil {
				return err
			}
		}
		if edit.Keep < 0 || edit.Keep > len(items) {
			return errors.New("array prefix out of range")
		}
		// Drop references to truncated suffixes so compaction releases old payloads.
		clear(items[edit.Keep:])
		items = append(items[:edit.Keep], edit.Append...)
		if items == nil {
			items = []json.RawMessage{}
		}
		arrays[key] = items
	}
	return nil
}

// Append writes and syncs one context transition before returning its reference.
func Append(path string, committed Ref, state json.RawMessage) (Ref, error) {
	done := false
	return AppendMany(path, committed, func() (json.RawMessage, error) {
		if done {
			return nil, io.EOF
		}
		done = true
		return state, nil
	})
}

// AppendMany streams an import under one lock, replaying the existing prefix only
// once. An error leaves only uncommitted bytes; retry with the original Ref.
func AppendMany(path string, committed Ref, next func() (json.RawMessage, error)) (Ref, error) {
	return AppendManyAt(path, committed, func() (json.RawMessage, int64, error) {
		raw, err := next()
		return raw, time.Now().UnixMilli(), err
	})
}

// AppendManyAt preserves original timestamps while streaming legacy history.
// Zero timestamps represent unknown legacy time; negative times are rejected.
func AppendManyAt(path string, committed Ref, next func() (json.RawMessage, int64, error)) (Ref, error) {
	if err := committed.validate(); err != nil {
		return Ref{}, err
	}
	unlock, err := lock(path)
	if err != nil {
		return Ref{}, err
	}
	defer unlock()
	if err = restore(path, committed); err != nil {
		return Ref{}, err
	}
	var state object
	var offset int64
	f, err := openRegular(path, os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) && committed.IsZero() {
		f, err = openRegular(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err != nil {
		return Ref{}, err
	}
	defer f.Close()
	state, offset, err = replayForAppend(path, f, committed)
	if err != nil {
		return Ref{}, err
	}
	if err = f.Truncate(offset); err != nil {
		return Ref{}, err
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return Ref{}, err
	}
	current := committed
	for {
		raw, at, e := next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return Ref{}, e
		}
		if at < 0 {
			return Ref{}, errors.New("negative rollout timestamp")
		}
		updated, e := decodeState(raw)
		if e != nil {
			return Ref{}, e
		}
		if current.Sequence == int64(^uint64(0)>>1) {
			return Ref{}, errors.New("rollout sequence exhausted")
		}
		p := payload{AtMS: at, Version: 1, Sequence: current.Sequence + 1, Previous: current.Hash}
		if current.IsZero() {
			p.Checkpoint = &updated
		} else {
			d := difference(state, updated)
			p.Delta = &d
		}
		h, e := digest(p)
		if e != nil {
			return Ref{}, e
		}
		line, e := json.Marshal(record{payload: p, Hash: h})
		if e != nil {
			return Ref{}, e
		}
		if len(line)+1 > maxRecordSize {
			return Ref{}, errors.New("rollout record exceeds size limit")
		}
		if _, e = f.Write(append(line, '\n')); e != nil {
			return Ref{}, e
		}
		current = Ref{Sequence: p.Sequence, Hash: h}
		state = updated
	}
	if err = f.Sync(); err != nil {
		return Ref{}, err
	}
	// A crash during compression can leave both forms. The synced plain log
	// is authoritative; remove its now-stale compressed representation.
	if err = os.Remove(path + ".zst"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Ref{}, err
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return Ref{}, err
	}
	remember(path, f, current, state, offsetForFile(f))
	return current, nil
}

// Load verifies the committed hash chain and reconstructs its exact context.
// Uncommitted plain-file tail bytes are ignored, including a torn final line.
func Load(path string, committed Ref) (json.RawMessage, error) {
	if err := committed.validate(); err != nil {
		return nil, err
	}
	if committed.IsZero() {
		return nil, nil
	}
	unlock, err := lock(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	f, err := openRegular(path, os.O_RDONLY, 0)
	if err == nil {
		defer f.Close()
		state, _, e := replay(f, committed)
		if e != nil {
			return nil, e
		}
		return json.Marshal(state)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err = openRegular(path+".zst", os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := newArchiveReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	state, _, err := replay(z, committed)
	if err != nil {
		return nil, err
	}
	// Consume through EOF to validate zstd checksum/trailer, including buffered data.
	if _, err = io.Copy(io.Discard, z); err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func replay(input io.Reader, committed Ref) (object, int64, error) {
	if committed.IsZero() {
		return nil, 0, nil
	}
	reader := bufio.NewReaderSize(input, 64<<10)
	state := object{}
	arrays := map[string][]json.RawMessage{}
	previous := ""
	var offset int64
	for sequence := int64(1); sequence <= committed.Sequence; sequence++ {
		line, err := readLine(reader)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: record %d: %v", ErrCorrupt, sequence, err)
		}
		offset += int64(len(line))
		var entry record
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&entry); err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return nil, 0, fmt.Errorf("%w: trailing record data", ErrCorrupt)
		}
		hash, e := digest(entry.payload)
		if e != nil {
			return nil, 0, e
		}
		if entry.AtMS < 0 || entry.Version != 1 || entry.Sequence != sequence || entry.Previous != previous || entry.Hash != hash {
			return nil, 0, fmt.Errorf("%w: record chain mismatch at %d", ErrCorrupt, sequence)
		}
		if sequence == 1 {
			if entry.Checkpoint == nil || entry.Delta != nil {
				return nil, 0, fmt.Errorf("%w: missing initial checkpoint", ErrCorrupt)
			}
			state = *entry.Checkpoint
		} else {
			if entry.Checkpoint != nil || entry.Delta == nil {
				return nil, 0, fmt.Errorf("%w: invalid delta record", ErrCorrupt)
			}
			if err = applyBuffered(state, arrays, *entry.Delta); err != nil {
				return nil, 0, fmt.Errorf("%w: %v", ErrCorrupt, err)
			}
		}
		previous = hash
	}
	if previous != committed.Hash {
		return nil, 0, fmt.Errorf("%w: committed hash mismatch", ErrCorrupt)
	}
	for key, items := range arrays {
		raw, err := json.Marshal(items)
		if err != nil {
			return nil, 0, err
		}
		state[key] = raw
	}
	return state, offset, nil
}
func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > maxRecordSize {
			return nil, errors.New("record size limit")
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

// Compress replaces an exact committed JSONL file with a verified zstd copy.
// Uncommitted tail bytes are discarded. Callers must serialize reference commits.
func Compress(path string, committed Ref) error {
	forget(path)
	if err := committed.validate(); err != nil {
		return err
	}
	if committed.IsZero() {
		return errors.New("cannot compress empty rollout")
	}
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	// Cold already-compressed sessions are verified without rewriting their
	// representation or modification time on repeated checks.
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		exact, verifyErr := compressedExact(path+".zst", committed)
		if verifyErr != nil {
			return verifyErr
		}
		if exact {
			return nil
		}
	} else if statErr != nil {
		return statErr
	}
	if err = restore(path, committed); err != nil {
		return err
	}
	f, err := openRegular(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, offset, err := replay(f, committed)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != offset {
		if err = f.Truncate(offset); err != nil {
			return err
		}
		if err = f.Sync(); err != nil {
			return err
		}
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".rollout-compress-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	z, err := newArchiveWriter(temp)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(z, hash), f)
	if err != nil {
		_ = z.Close()
		return err
	}
	if err = z.Close(); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	check, err := openRegular(temp.Name(), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	zr, err := newArchiveReader(check)
	if err != nil {
		check.Close()
		return err
	}
	actual := sha256.New()
	size, copyErr := io.Copy(actual, zr)
	zr.Close()
	check.Close()
	if copyErr != nil {
		return copyErr
	}
	if size != n || !bytes.Equal(actual.Sum(nil), hash.Sum(nil)) {
		return fmt.Errorf("%w: compressed verification mismatch", ErrCorrupt)
	}
	if err = os.Rename(temp.Name(), path+".zst"); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// restore materializes a compressed rollout before append. Plain files always
// win after an interrupted representation change; stale zstd files are removed.
func restore(path string, committed Ref) error {
	f, err := openRegular(path, os.O_RDONLY, 0)
	if err == nil {
		f.Close()
		// Do not remove the old representation before the plain prefix is validated.
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err = openRegular(path+".zst", os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) && committed.IsZero() {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	z, err := newArchiveReader(f)
	if err != nil {
		return err
	}
	defer z.Close()
	temp, err := os.CreateTemp(filepath.Dir(path), ".rollout-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = io.Copy(temp, z); err != nil {
		return err
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, _, err = replay(temp, committed); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	if err = syncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	if err = os.Remove(path + ".zst"); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func openRegular(path string, flags int, mode os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("rollout path is not a regular file")
	}
	return f, nil
}
func lock(path string) (func(), error) {
	if err := durableDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := openRegular(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Hot append replay is bounded and keyed by both the committed reference and
// the file identity/metadata. Load always verifies disk rather than using cache.
type replayEntry struct {
	ref    Ref
	state  object
	offset int64
	info   os.FileInfo
	bytes  int
}

var replayCache = struct {
	sync.Mutex
	entries map[string]replayEntry
	bytes   int
}{entries: map[string]replayEntry{}}

const cacheByteLimit = 32 << 20

func offsetForFile(f *os.File) int64 { n, _ := f.Seek(0, io.SeekCurrent); return n }
func forget(path string) {
	replayCache.Lock()
	defer replayCache.Unlock()
	if e, ok := replayCache.entries[path]; ok {
		replayCache.bytes -= e.bytes
		delete(replayCache.entries, path)
	}
}
func remember(path string, f *os.File, ref Ref, state object, offset int64) {
	info, err := f.Stat()
	if err != nil {
		return
	}
	size := 0
	for k, v := range state {
		size += len(k) + len(v)
	}
	replayCache.Lock()
	defer replayCache.Unlock()
	if e, ok := replayCache.entries[path]; ok {
		replayCache.bytes -= e.bytes
		delete(replayCache.entries, path)
	}
	if size > cacheByteLimit {
		return
	}
	if len(replayCache.entries) >= 16 || replayCache.bytes+size > cacheByteLimit {
		clear(replayCache.entries)
		replayCache.bytes = 0
	}
	replayCache.entries[path] = replayEntry{ref, state, offset, info, size}
	replayCache.bytes += size
}
func replayForAppend(path string, f *os.File, ref Ref) (object, int64, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	replayCache.Lock()
	entry, ok := replayCache.entries[path]
	replayCache.Unlock()
	if ok && entry.ref == ref && os.SameFile(info, entry.info) && info.Size() == entry.info.Size() && info.ModTime() == entry.info.ModTime() && reflect.DeepEqual(info.Sys(), entry.info.Sys()) {
		return entry.state, entry.offset, nil
	}
	forget(path)
	return replay(f, ref)
}

// Sync each newly created directory entry before publishing references to files
// beneath it. Existing caller-owned directories keep their permissions.
func durableDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return errors.New("rollout parent is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return err
	}
	if err = durableDirectory(parent); err != nil {
		return err
	}
	if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDirectory(parent)
}

type countingReader struct {
	input io.Reader
	bytes int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.input.Read(p)
	r.bytes += int64(n)
	return n, err
}
func compressedExact(path string, ref Ref) (bool, error) {
	f, err := openRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	z, err := newArchiveReader(f)
	if err != nil {
		return false, err
	}
	defer z.Close()
	counter := &countingReader{input: z}
	_, offset, err := replay(counter, ref)
	if err != nil {
		return false, err
	}
	if _, err = io.Copy(io.Discard, counter); err != nil {
		return false, err
	}
	return counter.bytes == offset, nil
}
