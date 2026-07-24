package contract

import (
	"bytes"
	"testing"
)

func TestUnsupportedCommandAndCanonicalError(t *testing.T) {
	var output bytes.Buffer
	if err := Run([]string{"unknown"}, &output); err == nil {
		t.Fatal("unknown command succeeded")
	}
	var errors bytes.Buffer
	FormatError(&errors, &Error{Message: "bad command", Code: "INVALID_ARGUMENT", NextAction: "choose a supported command"})
	if errors.String() != "Error: bad command\nCode: INVALID_ARGUMENT\nNext action: choose a supported command\n" {
		t.Fatalf("error output = %q", errors.String())
	}
}
