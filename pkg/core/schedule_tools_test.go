package core_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestScheduleGoalToolContract(t *testing.T) {
	for _, raw := range core.ProductTools() {
		var tool struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]struct {
					Properties map[string]map[string]any `json:"properties"`
				} `json:"properties"`
			} `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Name != "manage_schedule" {
			continue
		}
		fields := tool.InputSchema.Properties["spec"].Properties
		goal := fields["goal"]
		if goal["type"] != "string" || goal["minLength"] != float64(1) || goal["maxLength"] != nil {
			t.Fatalf("goal must not misrepresent bytes as character maxLength: %v", goal)
		}
		if description, _ := goal["description"].(string); !strings.Contains(description, fmt.Sprintf("512 KiB = %d UTF-8 bytes", protocol.MaxScheduleGoalBytes)) || !strings.Contains(description, "not whitespace-only") {
			t.Fatalf("goal description diverges from validator: %q", description)
		}
		if fields["title"]["maxLength"] != float64(80) || !strings.Contains(fields["title"]["description"].(string), "Unicode characters") {
			t.Fatal("title must retain its character limit")
		}
		return
	}
	t.Fatal("manage_schedule contract missing")
}
