package modelprotocol

import (
	"encoding/json"
	"github.com/jjyr/pot/internal/protocol"
)

// SSE frames native provider JSON without interpreting fields. A partial final
// frame is an error: truncation cannot be promoted into an executable tool call.
type SSE struct{ decoder protocol.SSEDecoder }

func (s *SSE) Push(chunk []byte) ([]json.RawMessage, error) {
	s.decoder.Limit = 16 * 1024 * 1024
	frames, err := s.decoder.Push(chunk)
	if err != nil {
		return nil, err
	}
	result := []json.RawMessage{}
	for _, frame := range frames {
		if frame.Data == "[DONE]" {
			continue
		}
		if _, err := object([]byte(frame.Data)); err != nil {
			return nil, err
		}
		result = append(result, json.RawMessage(frame.Data))
	}
	return result, nil
}
func (s *SSE) Finish() error { return s.decoder.Finish() }
