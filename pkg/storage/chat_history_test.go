package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestUIWindowStableSequenceAndLocate(t *testing.T) {
	ctx := context.Background()
	s, _ := testStore(t)
	page, meta, err := s.UIWindow(ctx, 0, 0, "", 10)
	if err != nil || len(page) != 0 || meta.HasMore {
		t.Fatal(page, meta, err)
	}
	for i := 0; i < 241; i++ {
		m := message(fmt.Sprintf("m-%03d", i), protocol.Complete)
		m.CreatedAtMS = 1
		if _, err = s.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	page, meta, err = s.UIWindow(ctx, 0, 0, "", 80)
	if err != nil || len(page) != 80 || page[0].ID != "m-161" || !meta.HasMore || meta.HasNewer {
		t.Fatal(meta, err)
	}
	before := meta.Before
	// Concurrent append and an edit leave every old insertion sequence intact.
	m := message("m-050", protocol.Complete)
	m.Text = "edited"
	s.Save(ctx, m)
	s.Save(ctx, message("new", protocol.Complete))
	older, olderMeta, err := s.UIWindow(ctx, 162, 0, "", 80)
	if err != nil || older[0].ID != "m-081" || olderMeta.Before != "82" || before != "162" {
		t.Fatal(olderMeta, err)
	}
	seen := map[string]bool{}
	cursor := int64(0)
	for {
		rows, h, e := s.UIWindow(ctx, cursor, 0, "", 80)
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range rows {
			if seen[m.ID] {
				t.Fatal("duplicate", m.ID)
			}
			seen[m.ID] = true
		}
		if !h.HasMore {
			break
		}
		fmt.Sscan(h.Before, &cursor)
	}
	if len(seen) != 242 {
		t.Fatal("pagination lost messages", len(seen))
	}
	located, h, err := s.UIWindow(ctx, 0, 0, "m-050", 20)
	if err != nil || len(located) != 20 || !h.HasMore || !h.HasNewer {
		t.Fatal(h, err)
	}
	found := false
	for _, m := range located {
		if m.ID == "m-050" {
			found = m.Text == "edited" && m.Sequence == "51"
		}
	}
	if !found {
		t.Fatal("locate lost edited identity")
	}
	if _, _, err = s.UIWindow(ctx, 0, 0, "foreign", 20); !IsMissingUIMessage(err) {
		t.Fatal("missing ID", err)
	}
	newer, h, err := s.UIWindow(ctx, 0, 240, "", 20)
	if err != nil || len(newer) != 2 || h.HasNewer {
		t.Fatal(h, err)
	}
}

func TestUIChangesBoundedCoalescing(t *testing.T) {
	ctx := context.Background()
	s, _ := testStore(t)
	m := message("active", protocol.Streaming)
	for i := 0; i < 200; i++ {
		m.Text = fmt.Sprintf("token %d", i)
		if _, err := s.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := s.UIChanges(ctx, 0, 200)
	if err != nil || len(changes) != 1 || changes[0].Text != "token 199" {
		t.Fatal(changes, err)
	}
	if _, err = s.UIChanges(ctx, 0, 257); err == nil {
		t.Fatal("unbounded journal allowed")
	}
}
