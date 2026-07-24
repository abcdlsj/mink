package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoggerCarriesStableClassification(t *testing.T) {
	var output bytes.Buffer
	logger := CategoryLogger(New(ComponentComputer, &output), ComponentComputer, CategoryRun)
	logger.Info("run claimed", "event", "run.claimed", "run_id", "run-1")
	logged := output.String()
	for _, want := range []string{"component=computer", "category=run", "event=run.claimed", "run_id=run-1"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q does not contain %q", logged, want)
		}
	}
}

func TestLoggerJSONFormatCarriesStableClassification(t *testing.T) {
	t.Setenv("SUMI_LOG_FORMAT", "json")
	var output bytes.Buffer
	CategoryLogger(New(ComponentServer, &output), ComponentServer, CategoryKnowledge).
		Info("projection rebuilt", "event", "knowledge.index.rebuild.completed")
	var logged map[string]any
	if err := json.Unmarshal(output.Bytes(), &logged); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	for key, want := range map[string]string{
		"component": "server",
		"category":  "knowledge",
		"event":     "knowledge.index.rebuild.completed",
	} {
		if got := logged[key]; got != want {
			t.Fatalf("log field %q = %#v, want %q", key, got, want)
		}
	}
}

func TestLoggerWarnsAndFallsBackForInvalidConfiguration(t *testing.T) {
	t.Setenv("SUMI_LOG_LEVEL", "error")
	t.Setenv("SUMI_LOG_FORMAT", "yaml")
	var output bytes.Buffer
	New(ComponentInstaller, &output)
	logged := output.String()
	for _, want := range []string{
		" WARN ", "component=installer", "category=lifecycle",
		"event=logging.configuration.invalid", "invalid_level=false", "invalid_format=true",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q does not contain %q", logged, want)
		}
	}
}
