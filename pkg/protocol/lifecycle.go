package protocol

import "strings"

// LifecycleEvent is a committed business fact, never an invalidation hint or
// executable request. Only bounded presentation data crosses the event stream.
type LifecycleEvent struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Owner      string `json:"owner"`
	Task       string `json:"task,omitempty"`
	Name       string `json:"name,omitempty"`
	Preview    string `json:"preview,omitempty"`
	Source     string `json:"source,omitempty"`
	SourceBoot string `json:"source_boot,omitempty"`
	Suppressed bool   `json:"suppressed,omitempty"`
}

// PreviewText is the shared bounded catalogue/lifecycle presentation of a reply.
func (m Message) PreviewText() string {
	text := m.Text
	if m.Presentation != nil {
		text = m.Presentation.Caption
		if text == "" {
			text = m.Presentation.Resource.Name
		}
	}
	runes := []rune(strings.Join(strings.Fields(text), " "))
	return string(runes[:min(120, len(runes))])
}
