package main

import (
	"strings"
	"testing"
)

func TestExperimentOptions(t *testing.T) {
	overrides, err := parseExperimentOptions([]string{"ask", "hello", "--model", "main-model", "--effort=high", "--task-model", "worker-model", "--task-effort", "low", "--provider", "main-provider", "--task-provider", "worker-provider"}, nil)
	if err != nil || overrides.Model != "main-model" || overrides.Effort != "high" || overrides.TaskModel != "worker-model" || overrides.TaskEffort != "low" || overrides.Provider != "main-provider" || overrides.TaskProvider != "worker-provider" {
		t.Fatal(overrides, err)
	}
	for _, flag := range []string{"--model", "--effort", "--task-model", "--task-effort", "--provider", "--task-provider"} {
		if _, err := parseExperimentOptions([]string{"usage", flag, "value"}, nil); err == nil {
			t.Fatalf("usage accepted %s", flag)
		}
		if _, err := parseExperimentOptions([]string{"ask", "hello", flag, "  "}, nil); err == nil {
			t.Fatalf("blank %s accepted", flag)
		}
	}
	for _, args := range [][]string{
		{"ask"}, {"ask", "--task", "task", "hello"}, {"ask", "--details", "hello"}, {"ask", "--wat", "hello"},
		{"usage", "--no-wait"}, {"usage", "--assistant", "a", "--task", "t"}, {"usage", "--turn", "t"},
		{"usage", "--from", "2026-10-01"}, {"usage", "--from", "2026-10-02", "--to", "2026-10-01"},
		{"usage", "--from", "2026-02-30", "--to", "2026-03-01"}, {"usage", "--timezone", "bogus"},
		{"usage", "--offset", "20"}, {"usage", "--details", "--offset", "-1"}, {"usage", "text"},
	} {
		if _, err := parseExperimentOptions(args, strings.NewReader("")); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parseExperimentOptions([]string{"ask", "--assistant=a", "--", "--literal"}, nil)
	if err != nil || o.Prompt != "--literal" || o.Assistant != "a" {
		t.Fatal(o, err)
	}
	o, err = parseExperimentOptions([]string{"ask", "-"}, strings.NewReader("中文\nexperiment"))
	if err != nil || o.Prompt != "中文\nexperiment" {
		t.Fatal(o, err)
	}
	for _, input := range []string{"  ", strings.Repeat("x", 65537)} {
		if _, err = parseExperimentOptions([]string{"ask", "-"}, strings.NewReader(input)); err == nil {
			t.Fatal("accepted invalid stdin")
		}
	}
}
