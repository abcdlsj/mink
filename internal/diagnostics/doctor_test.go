package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/internal/osservice"
)

type fakeServices struct {
	running bool
}

func (*fakeServices) Configure(string) {}
func (services *fakeServices) Running(context.Context, osservice.Component) bool {
	return services.running
}

func TestDoctorJSONHasOnlyStableCodeAndResultAndNoPrivatePath(t *testing.T) {
	homeDirectory := filepath.Join(t.TempDir(), "private-home")
	if err := os.Mkdir(homeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDirectory)
	doctor, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	doctor.Services = &fakeServices{}
	report := doctor.Run(context.Background())
	payload, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["code"] == "" || fields["result"] == "" {
		t.Fatalf("doctor JSON = %s", payload)
	}
	if strings.Contains(string(payload), homeDirectory) {
		t.Fatalf("doctor JSON leaked private path: %s", payload)
	}
}

func TestDoctorTreatsRestoreResidueAsErrorWithoutReadingBody(t *testing.T) {
	homeDirectory := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(homeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDirectory)
	doctor, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(doctor.Layout.RestoreRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	report := doctor.Run(context.Background())
	if report.Code != "RESTORE_PENDING" || report.Result != ResultError {
		t.Fatalf("report = %+v", report)
	}
}
