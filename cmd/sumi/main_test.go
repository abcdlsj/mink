package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestRunContextForwardsArgumentsAndWriters(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runContext(context.Background(), []string{"computer", "run", "--help"}, strings.NewReader(""), &stdout, &stderr); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("runContext() error = %v", err)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage of sumi-computer") {
		t.Fatalf("runContext() output = %q / %q", stdout.String(), stderr.String())
	}
}
