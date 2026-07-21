package hostcli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/abcdlsj/sumi/internal/driver"
)

type Error struct {
	Message    string
	Code       string
	NextAction string
}

func (e *Error) Error() string {
	return e.Message
}

func Run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return &Error{Message: "a command is required", Code: "MISSING_COMMAND", NextAction: "run 'sumi host contract' or 'sumi driver capabilities --kind <kind>'"}
	}
	switch args[0] {
	case "host":
		return runHost(args[1:], output)
	case "driver":
		return runDriver(args[1:], output)
	default:
		return &Error{Message: fmt.Sprintf("unsupported command %q", args[0]), Code: "INVALID_COMMAND", NextAction: "use 'host' or 'driver'"}
	}
}

func FormatError(output io.Writer, err error) {
	var structured *Error
	if !errors.As(err, &structured) {
		structured = &Error{Message: err.Error(), Code: "INTERNAL", NextAction: "retry the command; inspect the daemon log only if it persists"}
	}
	fmt.Fprintf(output, "Error: %s\nCode: %s\nNext action: %s\n", structured.Message, structured.Code, structured.NextAction)
}

func runHost(args []string, output io.Writer) error {
	if len(args) != 1 || args[0] != "contract" {
		return &Error{Message: "unsupported host command", Code: "INVALID_COMMAND", NextAction: "use 'sumi host contract'"}
	}
	fmt.Fprintf(output, "Contract: %s\nCommands: prompt steer spawn fork\nEvents: ordered optional stream\n", driver.ContractVersion)
	return nil
}

func runDriver(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "capabilities" {
		return &Error{Message: "unsupported driver command", Code: "INVALID_COMMAND", NextAction: "use 'sumi driver capabilities --kind <kind>'"}
	}
	flags := flag.NewFlagSet("sumi driver capabilities", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	kindValue := flags.String("kind", "", "driver kind")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return &Error{Message: "driver kind is required", Code: "INVALID_ARGUMENT", NextAction: "choose native, codex, or claude"}
	}
	kind := driver.Kind(strings.ToLower(*kindValue))
	capability, err := driver.Capabilities(kind)
	if err != nil {
		return &Error{Message: err.Error(), Code: "INVALID_ARGUMENT", NextAction: "choose native, codex, or claude"}
	}
	fmt.Fprintf(output, "Contract: %s\nDriver: %s\nStreaming: %t\nTools: %t\nResume: %t\nCancel: %t\nSteering: %t\n", driver.ContractVersion, kind, capability.Streaming, capability.Tools, capability.Resume, capability.Cancel, capability.Steering)
	return nil
}
