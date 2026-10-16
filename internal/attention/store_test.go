package attention

import (
	"path/filepath"
	"testing"
)

func TestReadBaselinesAreDurableAndControlScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reads.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := Row{Receipt: Receipt{ControlID: "one", Owner: "main", ReplyID: "first", Count: 5}, Connected: true}
	status, err := s.Observe([]Row{first})
	if err != nil || status.Unread != 0 {
		t.Fatal(status, err)
	}
	next := first
	next.ReplyID = "next"
	next.Count = 7
	next.Running = true
	other := next
	other.ControlID = "two"
	status, err = s.Observe([]Row{next, other})
	if err != nil || status.Unread != 2 || !status.Running {
		t.Fatal(status, err)
	}
	if _, err = s.Read(first.Receipt); err == nil {
		t.Fatal("stale read cleared unseen replies")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err = s.Observe([]Row{next, other})
	if err != nil || status.Unread != 2 {
		t.Fatal("restart lost unread", status, err)
	}
	if _, err = s.Read(next.Receipt); err != nil {
		t.Fatal(err)
	}
	status, err = s.Observe([]Row{next, other})
	if err != nil || status.Unread != 0 {
		t.Fatal(status, err)
	}
	next.Count = 1
	next.ReplyID = "reset"
	status, err = s.Observe([]Row{next})
	if err != nil || status.Unread != 0 {
		t.Fatal("reset replayed arrivals", status, err)
	}
}
