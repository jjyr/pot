package eval

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/modelprotocol"
)

// Replay exact native bytes independently of the report/event usage projection.
func validateNativeCapture(directory string, captured []core.ModelRequest, reports []UsageReport) error {
	requests := map[string]core.ModelRequest{}
	for _, r := range captured {
		requests[r.ID] = r
	}
	type stream struct {
		decoder  modelprotocol.SSE
		observer modelprotocol.UsageObserver
		sequence uint64
		bytes    uint64
	}
	streams := map[string]*stream{}
	replayed := []UsageReport{}
	file, err := os.Open(filepath.Join(directory, "responses.jsonl"))
	if os.IsNotExist(err) {
		for _, r := range reports {
			if r.Source == "native" {
				return errors.New("native usage has no raw response capture")
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	total := uint64(0)
	for scanner.Scan() {
		var record struct {
			RequestID string `json:"request_id"`
			Bytes     []byte `json:"bytes"`
		}
		if err = core.DecodeJSON(scanner.Bytes(), &record); err != nil {
			return err
		}
		request, ok := requests[record.RequestID]
		if !ok {
			return errors.New("native response refers to unknown request")
		}
		s := streams[record.RequestID]
		if s == nil {
			s = &stream{}
			streams[record.RequestID] = s
		}
		s.bytes += uint64(len(record.Bytes))
		total += uint64(len(record.Bytes))
		if s.bytes > 24*1024*1024 || total > 64*1024*1024 {
			return errors.New("native response capture exceeds limits")
		}
		frames, err := s.decoder.Push(record.Bytes)
		if err != nil {
			return err
		}
		for _, frame := range frames {
			s.sequence++
			usage, err := s.observer.Observe(modelprotocol.Protocol(request.Protocol), frame)
			if err != nil {
				return err
			}
			if usage != nil {
				replayed = append(replayed, nativeReport(request.ID, fmt.Sprintf("native-%d", s.sequence), usage, frame, modelprotocol.Protocol(request.Protocol)))
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	// Failed/interrupted streams are retained; their coverage is determined by the
	// ordered request terminal, while fully framed observed usage remains usable.
	actual := []UsageReport{}
	for _, r := range reports {
		if r.Source == "native" {
			actual = append(actual, r)
		}
	}
	a, err := core.HashJSON(actual)
	if err != nil {
		return err
	}
	b, err := core.HashJSON(replayed)
	if err != nil {
		return err
	}
	if a != b {
		return errors.New("native usage events differ from exact captured response bytes")
	}
	return nil
}
