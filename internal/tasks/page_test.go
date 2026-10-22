package tasks

import (
	"context"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func TestCatalogPaginatesAcrossOwnersWithoutLeakingAgentPages(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	for i := 0; i < 120; i++ {
		owner := "alpha"
		if i%2 == 1 {
			owner = "beta"
		}
		text := fmt.Sprintf("Review item %03d", i)
		if i == 0 {
			text += "\nÜBER你好"
		}
		record, err := s.Create(ctx, owner, protocol.CreateTask{RequestID: fmt.Sprint(i), Text: text})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.db.Exec("UPDATE tasks SET body=json_set(body,'$.info.updated_at_ms',?) WHERE assistant_id=? AND id=?", i, owner, record.Info.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Catalog(ctx, protocol.TaskQuery{Scope: "active", Limit: 20})
	if err != nil || page.Total != 120 || page.Counts.Active != 120 || len(page.Items) != 20 || page.NextOffset == nil || *page.NextOffset != 20 {
		t.Fatalf("catalog: %+v %v", page, err)
	}
	for i, item := range page.Items {
		if item.UpdatedAtMS != int64(119-i) {
			t.Fatalf("wrong merge order: %+v", page.Items)
		}
	}
	page, err = s.Catalog(ctx, protocol.TaskQuery{Scope: "active", Offset: 100, Limit: 20})
	if err != nil || len(page.Items) != 20 || page.NextOffset != nil || page.Items[0].UpdatedAtMS != 19 {
		t.Fatalf("last page: %+v %v", page, err)
	}
	page, err = s.Page(ctx, "alpha", protocol.TaskQuery{Scope: "all", Limit: 50})
	if err != nil || page.Total != 60 {
		t.Fatalf("owner page: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.AssistantID != "alpha" {
			t.Fatal("agent page exposed another owner")
		}
	}
	if _, err = s.Page(ctx, "", protocol.TaskQuery{}); err == nil {
		t.Fatal("empty agent identity acquired catalogue access")
	}
	page, err = s.Catalog(ctx, protocol.TaskQuery{Q: "ITEM 119"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("search: %+v %v", page, err)
	}
	id := page.Items[0].ID
	page, err = s.Catalog(ctx, protocol.TaskQuery{Q: id, Offset: 999, Selected: id})
	if err != nil || page.Total != 1 || page.Offset != 0 || page.Selected == nil || page.Selected.ID != id {
		t.Fatalf("ID search and offset clamp: %+v %v", page, err)
	}
	if _, err = s.Catalog(ctx, protocol.TaskQuery{Limit: 51}); err == nil {
		t.Fatal("unbounded catalog page accepted")
	}
	page, err = s.Catalog(ctx, protocol.TaskQuery{Q: "über你好"})
	if err != nil || page.Total != 1 {
		t.Fatalf("Unicode case folding: %+v %v", page, err)
	}
}
