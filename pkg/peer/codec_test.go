package peer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func testBudget() *memoryBudget { return &memoryBudget{limit: maxSessionMemory} }
func acceptAny(frame) bool      { return true }

func TestMessageBodyBytesAndAdaptiveCompression(t *testing.T) {
	random := make([]byte, 128*1024)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		body     []byte
		encoding uint8
	}{
		{"small", []byte{0, 1, 0xff, 0xfe}, encodingNone},
		{"incompressible", random, encodingNone},
		{"repetitive", bytes.Repeat([]byte("large document content\n"), 10000), encodingZstd},
		{"maximum", bytes.Repeat([]byte("a"), MaxPayload), encodingZstd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := testBudget()
			var wire bytes.Buffer
			if err := writeMessage(context.Background(), &wire, frame{Kind: "request", ID: "id", Method: "echo"}, tc.body, budget); err != nil {
				t.Fatal(err)
			}
			if budget.used != 0 {
				t.Fatal("encoder reservation leaked")
			}
			h, body, release, err := readMessage(&wire, budget, acceptAny)
			if err != nil {
				t.Fatal(err)
			}
			if h.Encoding != tc.encoding || !bytes.Equal(body, tc.body) {
				t.Fatalf("roundtrip encoding=%d body=%d", h.Encoding, len(body))
			}
			if budget.used == 0 {
				t.Fatal("body memory not reserved")
			}
			release()
			release()
			if budget.used != 0 {
				t.Fatal("decoder reservation leaked")
			}
		})
	}
}

func TestStrictCBORMetadata(t *testing.T) {
	valid, _ := wireEncoding.Marshal(frame{Kind: "request", ID: "id", Method: "echo", Size: 4, RawSize: 4})
	unknown, _ := wireEncoding.Marshal(map[string]any{"unknown": true})
	nested := append(bytes.Repeat([]byte{0x81}, 9), 0xf6)
	for _, raw := range [][]byte{
		{0xa2, 0x62, 'i', 'd', 0x61, 'a', 0x62, 'i', 'd', 0x61, 'b'}, // duplicate key
		unknown, nested,
		{0xbf, 0xff},      // indefinite map
		{0xc0, 0x61, 'x'}, // tagged item
		append(append([]byte(nil), valid...), 0xf6), // trailing CBOR item
	} {
		var h frame
		if decode(raw, &h) == nil {
			t.Fatalf("accepted malformed CBOR: %x", raw)
		}
	}
}

func messageBytes(t *testing.T, h frame, body []byte) []byte {
	t.Helper()
	raw, err := wireEncoding.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writePacket(&buf, raw, maxHeader); err != nil {
		t.Fatal(err)
	}
	buf.Write(body)
	return buf.Bytes()
}

func TestMessageRejectsBoundsTruncationAndTrailingData(t *testing.T) {
	h := frame{Kind: "request", ID: "id", Method: "echo", Size: 4, RawSize: 4}
	oversize := h
	oversize.Size, oversize.RawSize = MaxPayload+1, MaxPayload+1
	wrongSize := h
	wrongSize.RawSize++
	unknownEncoding := h
	unknownEncoding.Encoding = 99
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], maxHeader+1)
	for _, raw := range [][]byte{
		length[:], messageBytes(t, oversize, nil), messageBytes(t, wrongSize, nil), messageBytes(t, unknownEncoding, nil),
		messageBytes(t, h, []byte("nul")), messageBytes(t, h, []byte("nullx")),
	} {
		budget := testBudget()
		_, _, release, err := readMessage(bytes.NewReader(raw), budget, acceptAny)
		release()
		if err == nil || budget.used != 0 {
			t.Fatalf("accepted bad frame or leaked: err=%v memory=%d", err, budget.used)
		}
	}
}

func TestCompressedBodyCannotExceedDeclaredSizeOrWindow(t *testing.T) {
	codec, err := compression()
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("a"), 1024*1024)
	encoded := codec.encoder.EncodeAll(body, nil)
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(4*compressionWindow), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	largeBody := bytes.Repeat([]byte("abcdefgh"), compressionWindow/2)
	largeWindow := enc.EncodeAll(largeBody, nil)
	for _, tc := range []struct {
		wire []byte
		size int
	}{
		{encoded, compressionThreshold}, // decompression bomb relative to declaration
		{encoded, len(body) + 1},        // short output
		{[]byte("invalid zstd"), len(body)},
		{largeWindow, len(largeBody)},
	} {
		h := frame{Kind: "request", ID: "id", Method: "echo", Encoding: encodingZstd, Size: uint32(len(tc.wire)), RawSize: uint32(tc.size)}
		budget := testBudget()
		_, _, release, err := readMessage(bytes.NewReader(messageBytes(t, h, tc.wire)), budget, acceptAny)
		release()
		if err == nil || budget.used != 0 {
			t.Fatalf("accepted invalid compression/leaked: %v memory=%d", err, budget.used)
		}
	}
}

type countingReader struct {
	io.Reader
	read int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestRejectRoutingAndMemoryBeforeReadingBody(t *testing.T) {
	h := frame{Kind: "request", ID: "id", Method: "echo", Size: 4, RawSize: 4}
	raw := messageBytes(t, h, []byte("null"))
	for _, tc := range []struct {
		limit  int
		accept func(frame) bool
	}{{0, acceptAny}, {100, func(frame) bool { return false }}} {
		r := &countingReader{Reader: bytes.NewReader(raw)}
		budget := &memoryBudget{limit: tc.limit}
		_, _, release, err := readMessage(r, budget, tc.accept)
		release()
		if err == nil || r.read != len(raw)-4 || budget.used != 0 {
			t.Fatalf("read rejected body: %d/%d err=%v memory=%d", r.read, len(raw), err, budget.used)
		}
	}
	// Under memory pressure compression may be skipped without allocating its
	// scratch output. The caller already owns the uncompressed body.
	budget := &memoryBudget{}
	var buf bytes.Buffer
	body := bytes.Repeat([]byte("a"), 8192)
	if err := writeMessage(context.Background(), &buf, h, body, budget); err != nil {
		t.Fatal(err)
	}
	rawHeader, err := readPacket(&buf, maxHeader)
	if err != nil {
		t.Fatal(err)
	}
	var header frame
	if err = decode(rawHeader, &header); err != nil || header.Encoding != encodingNone {
		t.Fatal("compression did not fall back", err)
	}
}

func TestHierarchicalMemoryAndSpanCleanup(t *testing.T) {
	parent := &memoryBudget{limit: 10}
	a := &memoryBudget{limit: 8, parent: parent}
	b := &memoryBudget{limit: 8, parent: parent}
	span := &streamMemory{budget: a}
	if err := span.ReserveMemory(8, 0); err != nil {
		t.Fatal(err)
	}
	if b.reserve(3) || a.reserve(1) {
		t.Fatal("memory limit bypassed")
	}
	span.ReleaseMemory(2)
	if !b.reserve(4) {
		t.Fatal("released memory unavailable")
	}
	span.Done()
	span.Done()
	span.ReleaseMemory(8)
	if span.ReserveMemory(1, 0) == nil {
		t.Fatal("closed span reused")
	}
	b.release(4)
	if a.used != 0 || b.used != 0 || parent.used != 0 {
		t.Fatal("memory reservation leaked")
	}
}

func BenchmarkMessageCodec(b *testing.B) {
	for _, size := range []int{256, 4096, 1024 * 1024} {
		payload, _ := json.Marshal(bytes.Repeat([]byte("document content "), size/17))
		b.Run(stringSize(size), func(b *testing.B) {
			budget := testBudget()
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				var buf bytes.Buffer
				if err := writeMessage(context.Background(), &buf, frame{Kind: "request", ID: "id", Method: "echo"}, payload, budget); err != nil {
					b.Fatal(err)
				}
				_, _, release, err := readMessage(&buf, budget, acceptAny)
				release()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func stringSize(n int) string {
	if n == 256 {
		return "256B"
	}
	if n == 4096 {
		return "4KiB"
	}
	return "1MiB"
}
