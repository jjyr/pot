package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"strings"
	"time"
)

const fragmentChunk = 64 * 1024
const fragmentBudget = 32 * 1024 * 1024

type fragmentHeader struct {
	Size   int    `json:"size"`
	Digest string `json:"digest"`
}
type fragmentChunkBody struct {
	Offset int    `json:"offset"`
	Data   string `json:"data"`
}
type assembly struct {
	original wire
	header   fragmentHeader
	data     []byte
	deadline time.Time
}

func (p *Peer) sendFragments(ctx context.Context, w wire) error {
	if (w.Kind != "request" && w.Kind != "response") || len(w.Params) > MaxPayload {
		return fmt.Errorf("RPC fragment payload outside limits")
	}
	digest := sha256.Sum256(w.Params)
	header, _ := json.Marshal(fragmentHeader{len(w.Params), hex.EncodeToString(digest[:])})
	start := w
	start.Kind = w.Kind + ".start"
	start.Params = header
	if err := p.sendFrame(ctx, start); err != nil {
		return err
	}
	for offset := 0; offset < len(w.Params); offset += fragmentChunk {
		end := min(offset+fragmentChunk, len(w.Params))
		chunk, _ := json.Marshal(fragmentChunkBody{offset, base64.RawStdEncoding.EncodeToString(w.Params[offset:end])})
		frame := w
		frame.Kind = w.Kind + ".chunk"
		frame.Params = chunk
		frame.Error = ""
		frame.Fault = nil
		if err := p.sendFrame(ctx, frame); err != nil {
			return err
		}
	}
	end := w
	end.Kind = w.Kind + ".end"
	end.Params = json.RawMessage("null")
	end.Error = ""
	end.Fault = nil
	return p.sendFrame(ctx, end)
}

// receiveFragment runs solely in the session reader. Declared sizes reserve a
// shared bounded budget before allocation; fragments cannot change ID/method or
// bypass per-frame identity, audience and epoch verification.
func (p *Peer) receiveFragment(w wire) (*wire, error) {
	for _, item := range p.fragments {
		if time.Now().After(item.deadline) {
			return nil, fmt.Errorf("RPC fragment expired")
		}
	}
	base, suffix, isFragment := strings.Cut(w.Kind, ".")
	if !isFragment {
		return &w, nil
	}
	if base != "request" && base != "response" {
		return nil, fmt.Errorf("invalid fragment kind")
	}
	key := base + ":" + w.ID
	if suffix == "start" {
		if p.fragments[key] != nil || len(p.fragments) >= 8 {
			return nil, fmt.Errorf("fragment capacity/identity conflict")
		}
		var h fragmentHeader
		if err := core.DecodeJSON(w.Params, &h); err != nil {
			return nil, err
		}
		if h.Size <= 128*1024 || h.Size > MaxPayload || h.Size+p.fragmentBytes > fragmentBudget || len(h.Digest) != 64 {
			return nil, fmt.Errorf("fragment budget exceeded")
		}
		if _, err := hex.DecodeString(h.Digest); err != nil {
			return nil, err
		}
		original := w
		original.Kind = base
		p.fragments[key] = &assembly{original: original, header: h, data: make([]byte, 0, h.Size), deadline: time.Now().Add(30 * time.Second)}
		p.fragmentBytes += h.Size
		return nil, nil
	}
	item := p.fragments[key]
	if item == nil || item.original.Method != w.Method || w.Error != "" || w.Fault != nil {
		return nil, fmt.Errorf("fragment identity mismatch")
	}
	if suffix == "chunk" {
		var body fragmentChunkBody
		if err := core.DecodeJSON(w.Params, &body); err != nil {
			return nil, err
		}
		chunk, err := base64.RawStdEncoding.DecodeString(body.Data)
		if err != nil || len(chunk) < 1 || len(chunk) > fragmentChunk || body.Offset != len(item.data) || len(item.data)+len(chunk) > item.header.Size {
			return nil, fmt.Errorf("fragment offset/size mismatch")
		}
		item.data = append(item.data, chunk...)
		return nil, nil
	}
	if suffix != "end" || string(w.Params) != "null" || len(item.data) != item.header.Size {
		return nil, fmt.Errorf("incomplete fragment payload")
	}
	hash := sha256.Sum256(item.data)
	if hex.EncodeToString(hash[:]) != item.header.Digest || !json.Valid(item.data) {
		return nil, fmt.Errorf("fragment checksum/JSON mismatch")
	}
	result := item.original
	result.Params = item.data
	delete(p.fragments, key)
	p.fragmentBytes -= item.header.Size
	return &result, nil
}
