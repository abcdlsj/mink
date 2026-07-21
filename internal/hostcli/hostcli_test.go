package hostcli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsHostContract(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"host", "contract"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"sumi.run.v1", "prompt", "steer", "spawn", "fork"} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("output = %q, missing %q", output.String(), value)
		}
	}
}

func TestDriverCapabilitiesAndCanonicalError(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"driver", "capabilities", "--kind", "claude"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Driver: claude") || !strings.Contains(output.String(), "Cancel: true") {
		t.Fatalf("output = %q", output.String())
	}
	if err := Run([]string{"driver", "capabilities", "--kind", "unknown"}, &output); err == nil {
		t.Fatal("unknown driver succeeded")
	}
	var errors bytes.Buffer
	FormatError(&errors, &Error{Message: "bad driver", Code: "INVALID_ARGUMENT", NextAction: "choose a supported driver"})
	if errors.String() != "Error: bad driver\nCode: INVALID_ARGUMENT\nNext action: choose a supported driver\n" {
		t.Fatalf("error output = %q", errors.String())
	}
}
