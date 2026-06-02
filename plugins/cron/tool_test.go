package cron

import (
	"strings"
	"testing"
)

func TestSchemaDescribesDeliverySource(t *testing.T) {
	props := (&toolImpl{}).Schema()["properties"].(map[string]any)
	source := props["source"].(map[string]any)
	desc := source["description"].(string)
	for _, want := range []string{
		"notice delivery source",
		"literal value `current`",
		"isolated cron session",
		"not in this delivery source",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("source description missing %q: %q", want, desc)
		}
	}
}

func TestCronSourceUsesTaskID(t *testing.T) {
	got := cronSource(Task{ID: "bazaar"})
	if got != "cron:bazaar" {
		t.Fatalf("cron source = %q", got)
	}
}

func TestCronSourceFallsBackForMissingTaskID(t *testing.T) {
	got := cronSource(Task{})
	if got != "cron" {
		t.Fatalf("cron source = %q", got)
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
