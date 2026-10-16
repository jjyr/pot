package eval

import (
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"io"
)

type observedStream struct {
	io.ReadCloser
	feedback *runFeedback
	request  core.ModelRequest
	decoder  modelprotocol.SSE
	observer modelprotocol.UsageObserver
	sequence uint64
}

func (s *observedStream) Read(data []byte) (int, error) {
	n, err := s.ReadCloser.Read(data)
	if n > 0 {
		frames, parseErr := s.decoder.Push(data[:n])
		if parseErr != nil {
			return 0, parseErr
		}
		for _, frame := range frames {
			s.sequence++
			usage, e := s.observer.Observe(modelprotocol.Protocol(s.request.Protocol), frame)
			if e != nil {
				return 0, e
			}
			if usage != nil {
				if e = s.feedback.record("usage", nativeReport(s.request.ID, fmt.Sprintf("native-%d", s.sequence), usage, frame, modelprotocol.Protocol(s.request.Protocol))); e != nil {
					return 0, e
				}
			}
		}
	}
	if err == io.EOF {
		if e := s.decoder.Finish(); e != nil {
			return n, e
		}
	}
	return n, err
}
