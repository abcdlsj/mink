package sumi

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/abcdlsj/sumi/internal/diagnostics"
)

type fakeDoctor struct {
	report diagnostics.Report
}

func (doctor fakeDoctor) Run(context.Context) diagnostics.Report { return doctor.report }

func TestDoctorJSONIsStableAndErrorsUseReportCode(t *testing.T) {
	original := newDoctor
	newDoctor = func(string) (doctorRunner, error) {
		return fakeDoctor{report: diagnostics.Report{Code: "RESTORE_PENDING", Result: diagnostics.ResultError}}, nil
	}
	t.Cleanup(func() { newDoctor = original })
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"doctor", "--json"}, bytes.NewReader(nil), &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("doctor error result returned success")
	}
	var payload map[string]any
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &payload) != nil || len(payload) != 2 || payload["code"] != "RESTORE_PENDING" {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}
