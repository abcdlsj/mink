package cron

import (
	"strings"
	"testing"
)

func TestSchemaDescribesCurrentSource(t *testing.T) {
	props := (&toolImpl{}).Schema()["properties"].(map[string]any)
	source := props["source"].(map[string]any)
	desc := source["description"].(string)
	if !strings.Contains(desc, "literal value `current`") {
		t.Fatalf("source description = %q", desc)
	}
}

func TestDefaultSourceTreatsCurrentAsContextSource(t *testing.T) {
	got := defaultSource("telegram:42", "current")
	if got != "telegram:42" {
		t.Fatalf("source = %q", got)
	}
}

func TestUpdateTaskTreatsCurrentSourceAsContextSource(t *testing.T) {
	task := Task{Source: "telegram:1"}
	updateTask(&task, "telegram:42:7", params{Source: "current"})
	if task.Source != "telegram:42:7" {
		t.Fatalf("source = %q", task.Source)
	}
}
