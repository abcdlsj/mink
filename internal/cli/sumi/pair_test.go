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

func TestRunRoutesComputerPairCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Run(context.Background(), []string{"computer", "pair", "create", "--help"}, strings.NewReader(""), &stdout, &stderr)
	if !errors.Is(err, flag.ErrHelp) || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("pair help = %v, %q / %q", err, stdout.String(), stderr.String())
	}
	err = Run(context.Background(), []string{"computer", "pair", "unknown"}, strings.NewReader(""), &stdout, &stderr)
	var structured *clicontract.Error
	if !errors.As(err, &structured) || structured.Code != "INVALID_COMMAND" {
		t.Fatalf("unknown pair command = %#v", err)
	}
}
