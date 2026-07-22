package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunContextForwardsArgumentsAndWriters(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runContext(context.Background(), []string{"auth"}, &stdout, &stderr)
	if err == nil || err.Error() != "auth requires --human-key-file and no positional arguments" {
		t.Fatalf("runContext() error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("runContext() output = %q / %q", stdout.String(), stderr.String())
	}
}
