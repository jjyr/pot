package protocol

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

type Frame struct {
	Event string
	Data  string
}
type SSEDecoder struct {
	line  []byte
	event string
	data  []string
	size  int
	Limit int
}

func (d *SSEDecoder) Push(chunk []byte) ([]Frame, error) {
	limit := d.Limit
	if limit == 0 {
		limit = 8 * 1024 * 1024
	}
	frames := []Frame{}
	for _, b := range chunk {
		d.size++
		if d.size > limit {
			return nil, fmt.Errorf("SSE frame exceeds limit")
		}
		if b != '\n' {
			d.line = append(d.line, b)
			continue
		}
		if !utf8.Valid(d.line) {
			return nil, fmt.Errorf("invalid SSE UTF-8")
		}
		line := string(bytes.TrimSuffix(d.line, []byte{'\r'}))
		d.line = d.line[:0]
		if line == "" {
			if len(d.data) > 0 {
				event := d.event
				if event == "" {
					event = "message"
				}
				frames = append(frames, Frame{event, strings.Join(d.data, "\n")})
			}
			d.event = ""
			d.data = nil
			d.size = 0
		} else if !strings.HasPrefix(line, ":") {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				d.event = value
			case "data":
				d.data = append(d.data, value)
			}
		}
	}
	return frames, nil
}
func (d *SSEDecoder) Finish() error {
	if len(d.line) != 0 || len(d.data) != 0 {
		return fmt.Errorf("incomplete SSE frame")
	}
	return nil
}
