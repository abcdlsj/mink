package sumi

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
)

func TestRunPreservesHostAndDriverCommands(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"host", "contract"}, "Commands: prompt steer spawn fork"},
		{[]string{"driver", "capabilities", "--kind", "native"}, "Driver: native"},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		if err := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("Run(%v) error = %v", test.args, err)
		}
		if !strings.Contains(stdout.String(), test.want) {
			t.Fatalf("Run(%v) stdout = %q, want %q", test.args, stdout.String(), test.want)
		}
	}
}

func TestRunRoutesForegroundCommands(t *testing.T) {
	tests := []struct {
		args []string
	}{
		{[]string{"server", "run", "--help"}},
		{[]string{"computer", "run", "--help"}},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		err := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("Run(%v) error = %v, want flag.ErrHelp", test.args, err)
		}
		if stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("Run(%v) output = %q / %q", test.args, stdout.String(), stderr.String())
		}
	}
}

func TestRunRoutesAuthOpenWithoutLeakingPath(t *testing.T) {
	privatePath := "/private/example/human.key"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Run(context.Background(), []string{"auth", "open", "--human-key-file", privatePath}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || err.Error() != "human credential file is missing or unsafe" {
		t.Fatalf("Run(auth open) error = %v", err)
	}
	if strings.Contains(err.Error()+stdout.String()+stderr.String(), privatePath) {
		t.Fatalf("Run(auth open) leaked private path: %q / %q / %q", err, stdout.String(), stderr.String())
	}
}

func TestRunRejectsUnsupportedNewSubcommands(t *testing.T) {
	for _, args := range [][]string{{"server", "start"}, {"computer", "install"}, {"auth", "create"}} {
		err := Run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
		var structured *clicontract.Error
		if !errors.As(err, &structured) || structured.Code != "INVALID_COMMAND" {
			t.Fatalf("Run(%v) error = %#v", args, err)
		}
	}
}
