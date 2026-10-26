package systemnotifications

import (
	"encoding/json"
	"strings"

	assets "github.com/jjyr/pot/web"
)

// Authority lets consumers discard events whose owner or source boot was retired.
type Authority interface{ Current(Event) bool }

// Event is the sole notification contract, independent of presentation channel.
// It contains no tool arguments, credentials or arbitrary navigation URL.
type Event struct {
	ID      string `json:"id"`
	Boot    string `json:"boot"`
	Space   string `json:"space"`
	Control string `json:"control"`
	Owner   string `json:"owner"`
	Task    string `json:"task,omitempty"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"`
	BodyKey string `json:"body_key"`
}
type View struct {
	Sequence uint64 `json:"sequence"`
	Space    string `json:"space"`
	Owner    string `json:"owner"`
	Task     string `json:"task"`
	Route    string `json:"route"`
	Focused  bool   `json:"focused"`
}

func (v View) Viewing(e Event) bool {
	return v.Focused && v.Space == e.Space && v.Owner == e.Owner && ((v.Task != "" && v.Task == e.Task) || (v.Task == "" && v.Route == "chat"))
}
func bounded(s string, limit int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return string(r)
}
func (e Event) Text(locale string) (string, string) {
	body := assets.Translate(locale, e.BodyKey)
	if e.Kind == "reply" && e.Preview != "" {
		body = bounded(e.Preview, 120)
	}
	return "Pot · " + bounded(e.Name, 60), body
}
func event(owner Owner, kind, id, task, name, preview string) Event {
	key, _ := json.Marshal([]string{owner.Control, owner.ID, kind, id})
	if name == "" {
		name = owner.Name
	}
	if name == "" {
		name = "Pot"
	}
	keys := map[string]string{"reply": "notifications.replyComplete", "task-complete": "notifications.taskComplete", "task-failed": "notifications.taskFailed", "approval": "notifications.approvalRequired"}
	return Event{Boot: owner.Boot, BodyKey: keys[kind], ID: string(key), Space: owner.Space, Control: owner.Control, Owner: owner.ID, Task: task, Kind: kind, Name: bounded(name, 60), Preview: bounded(preview, 120)}
}

// Target is the bounded navigation contract. Transports that already carry
// rendered text do not need preview, locale keys, boot data or arbitrary fields.
func (e Event) Target() map[string]string {
	return map[string]string{"id": e.ID, "space": e.Space, "control": e.Control, "owner": e.Owner, "task": e.Task, "kind": e.Kind, "name": e.Name}
}
