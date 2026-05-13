package cron

import "testing"

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
