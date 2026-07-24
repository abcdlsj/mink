package contract

import (
	"errors"
	"fmt"
	"io"
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
		return &Error{Message: "a command is required", Code: "MISSING_COMMAND", NextAction: "use 'sumi server', 'sumi computer', 'sumi doctor', or an install command"}
	}
	return &Error{Message: fmt.Sprintf("unsupported command %q", args[0]), Code: "INVALID_COMMAND", NextAction: "use 'sumi server', 'sumi computer', 'sumi doctor', or an install command"}
}

func FormatError(output io.Writer, err error) {
	var structured *Error
	if !errors.As(err, &structured) {
		structured = &Error{Message: err.Error(), Code: "INTERNAL", NextAction: "retry the command; inspect the daemon log only if it persists"}
	}
	fmt.Fprintf(output, "Error: %s\nCode: %s\nNext action: %s\n", structured.Message, structured.Code, structured.NextAction)
}
