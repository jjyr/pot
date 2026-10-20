package peer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/klauspost/compress/zstd"
)

const maxHeader = 32 * 1024
const compressionThreshold = 4 * 1024
const compressionWindow = 1024 * 1024
const encodingNone uint8 = 0
const encodingZstd uint8 = 1

// Metadata stays small. Bodies are independent byte strings, so the transport
// never re-serializes a business JSON document into another JSON envelope.
type frame struct {
	Kind     string       `cbor:"kind"`
	ID       string       `cbor:"id"`
	Method   string       `cbor:"method"`
	Encoding uint8        `cbor:"encoding,omitempty"`
	Size     uint32       `cbor:"size"`
	RawSize  uint32       `cbor:"raw_size"`
	Error    *RemoteError `cbor:"error,omitempty"`
}

var wireEncoding, wireDecoding = func() (cbor.EncMode, cbor.DecMode) {
	enc, err := cbor.CanonicalEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	dec, err := (cbor.DecOptions{
		DupMapKey:       cbor.DupMapKeyEnforcedAPF,
		IndefLength:     cbor.IndefLengthForbidden,
		TagsMd:          cbor.TagsForbidden,
		MaxNestedLevels: 8, MaxArrayElements: 16, MaxMapPairs: 16,
		ExtraReturnErrors: cbor.ExtraDecErrorUnknownField,
	}).DecMode()
	if err != nil {
		panic(err)
	}
	return enc, dec
}()

func decode(raw []byte, out any) error { return wireDecoding.Unmarshal(raw, out) }

type compressionCodec struct {
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

var compression = sync.OnceValues(func() (*compressionCodec, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithWindowSize(compressionWindow), zstd.WithEncoderConcurrency(2))
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(MaxPayload), zstd.WithDecoderMaxWindow(compressionWindow), zstd.WithDecodeAllCapLimit(true), zstd.WithDecoderConcurrency(2))
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &compressionCodec{enc, dec}, nil
})

func validFrame(h frame) bool {
	if h.Kind != "request" && h.Kind != "response" && h.Kind != "event" {
		return false
	}
	if !validRequestMetadata(h.ID, h.Method) || h.Size == 0 || h.Size > MaxPayload || h.RawSize == 0 || h.RawSize > MaxPayload {
		return false
	}
	if h.Encoding == encodingNone {
		if h.Size != h.RawSize {
			return false
		}
	} else if h.Encoding != encodingZstd || h.RawSize < compressionThreshold {
		return false
	}
	if h.Kind != "response" && h.Error != nil {
		return false
	}
	if h.Error != nil && (len(h.Error.Message) > 4096 || len(h.Error.Code) > 128 || len(h.Error.Data) > 16*1024 || len(h.Error.Data) > 0 && !json.Valid(h.Error.Data)) {
		return false
	}
	return h.Kind != "event" || len(h.Method) >= 6 && h.Method[:6] == "event."
}

func writeMessage(ctx context.Context, w io.Writer, h frame, body []byte, budget *memoryBudget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(body) == 0 || len(body) > MaxPayload {
		return fmt.Errorf("invalid peer body size")
	}
	h.Encoding, h.Size, h.RawSize = encodingNone, uint32(len(body)), uint32(len(body))
	wire := body
	if len(body) >= compressionThreshold {
		codec, err := compression()
		if err != nil {
			return err
		}
		capacity := codec.encoder.MaxEncodedSize(len(body))
		if budget.reserve(capacity) {
			defer budget.release(capacity)
			encoded := codec.encoder.EncodeAll(body, make([]byte, 0, capacity))
			// Compression is optional per message. Independent zstd frames do
			// not share dictionaries/history. Admission credentials never enter
			// the compressor. Require at least a ten-percent bandwidth saving.
			if len(encoded)*10 <= len(body)*9 {
				wire, h.Encoding, h.Size = encoded, encodingZstd, uint32(len(encoded))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validFrame(h) {
		return fmt.Errorf("invalid peer frame")
	}
	raw, err := wireEncoding.Marshal(h)
	if err != nil {
		return err
	}
	if len(raw) > maxHeader {
		return fmt.Errorf("peer metadata exceeds limit")
	}
	// Put small messages in one Yamux/TLS write. Separately writing the length,
	// header and body costs extra records and wakeups on every control RPC.
	combined := len(wire) <= 16*1024 && budget.reserve(len(wire))
	size := 4 + len(raw)
	if combined {
		size += len(wire)
		defer budget.release(len(wire))
	}
	packet := make([]byte, size)
	binary.BigEndian.PutUint32(packet[:4], uint32(len(raw)))
	copy(packet[4:], raw)
	if combined {
		copy(packet[4+len(raw):], wire)
		return writeAll(w, packet)
	}
	if err = writeAll(w, packet); err != nil {
		return err
	}
	return writeAll(w, wire)
}

// The caller validates routing before reading the declared body. release holds
// the reservation through dispatch, then transfers any returned result to its
// caller without retaining another transport copy.
func readMessage(r io.Reader, budget *memoryBudget, accept func(frame) bool) (frame, []byte, func(), error) {
	var h frame
	noop := func() {}
	raw, err := readPacket(r, maxHeader)
	if err != nil {
		return h, nil, noop, err
	}
	if decode(raw, &h) != nil || !validFrame(h) || !accept(h) {
		return h, nil, noop, fmt.Errorf("invalid peer frame")
	}
	size := int(h.Size)
	if h.Encoding == encodingZstd {
		size += int(h.RawSize)
	}
	if !budget.reserve(size) {
		return h, nil, noop, errMemoryCapacity
	}
	var once sync.Once
	release := func() { once.Do(func() { budget.release(size) }) }
	wire := make([]byte, int(h.Size))
	if _, err = io.ReadFull(r, wire); err != nil {
		release()
		return h, nil, noop, err
	}
	body := wire
	if h.Encoding == encodingZstd {
		codec, codecErr := compression()
		if codecErr != nil {
			release()
			return h, nil, noop, codecErr
		}
		body, err = codec.decoder.DecodeAll(wire, make([]byte, 0, int(h.RawSize)))
		if err != nil || len(body) != int(h.RawSize) {
			release()
			return h, nil, noop, fmt.Errorf("invalid peer compressed body")
		}
	}
	// One request/response per stream: require the sender's write-side FIN.
	// A truncated body or trailing bytes can never become an accepted RPC.
	var trailing [1]byte
	if n, err := r.Read(trailing[:]); n != 0 || err != io.EOF {
		release()
		return h, nil, noop, fmt.Errorf("invalid peer stream termination")
	}
	return h, body, release, nil
}
