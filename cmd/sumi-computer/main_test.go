package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunContextForwardsArgumentsAndWriters(t *testing.T) {
	var stderr bytes.Buffer
	err := runContext(context.Background(), []string{"unexpected"}, bytes.NewReader(nil), &stderr)
	if err == nil || err.Error() != "unexpected positional arguments" {
		t.Fatalf("runContext() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runContext() stderr = %q", stderr.String())
	}
}
