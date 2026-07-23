package sumi

import (
	"context"
	"flag"
	"fmt"
	"io"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	"github.com/abcdlsj/sumi/internal/diagnostics"
)

type doctorRunner interface {
	Run(context.Context) diagnostics.Report
}

var newDoctor = func(dataRoot string) (doctorRunner, error) {
	return diagnostics.New(dataRoot)
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataRoot := flags.String("data-root", "", "Sumi data root")
	jsonOutput := flags.Bool("json", false, "emit stable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return &clicontract.Error{Message: "doctor arguments are invalid", Code: "INVALID_ARGUMENT", NextAction: "remove positional arguments"}
	}
	doctor, err := newDoctor(*dataRoot)
	if err != nil {
		return &clicontract.Error{Message: "doctor environment is unsafe", Code: "DOCTOR_UNAVAILABLE", NextAction: "use a supported non-root current-user session"}
	}
	report := doctor.Run(ctx)
	if *jsonOutput {
		payload, err := report.JSON()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(stdout, string(payload)); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(stdout, "Doctor: %s (%s).\n", report.Result, report.Code); err != nil {
			return err
		}
	}
	if report.Result == diagnostics.ResultError {
		return &clicontract.Error{Message: "doctor found an error", Code: report.Code, NextAction: "resolve the reported stable code and rerun doctor"}
	}
	return nil
}
