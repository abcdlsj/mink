package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunContextForwardsArgumentsAndWriters(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runContext(context.Background(), []string{"host", "contract"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Commands: prompt steer spawn fork") || stderr.Len() != 0 {
		t.Fatalf("runContext() output = %q / %q", stdout.String(), stderr.String())
	}
}
