package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type ErrorType string

const (
	ErrTimeout    ErrorType = "TIMEOUT"
	ErrExec       ErrorType = "EXEC"
	ErrPermission ErrorType = "PERMISSION"
	ErrNotFound   ErrorType = "NOT_FOUND"
	ErrParse      ErrorType = "PARSE"
	ErrDuplicate  ErrorType = "DUPLICATE"
	ErrUnknown    ErrorType = "UNKNOWN"
)

type ToolError struct {
	Type       ErrorType
	Tool       string
	Message    string
	Details    string
	Suggestion string
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Type, e.Tool, e.Message)
}

func (e *ToolError) ForLLM() string {
	var b strings.Builder
	fmt.Fprintf(&b, "<error type=%q tool=%q>\n", e.Type, e.Tool)
	fmt.Fprintf(&b, "  <message>%s</message>\n", e.Message)
	if e.Details != "" {
		fmt.Fprintf(&b, "  <details>%s</details>\n", truncateStr(e.Details, 500))
	}
	if e.Suggestion != "" {
		fmt.Fprintf(&b, "  <suggestion>%s</suggestion>\n", e.Suggestion)
	}
	b.WriteString("</error>")
	return b.String()
}

func TimeoutError(tool, cmd string, timeout int) *ToolError {
	return &ToolError{
		Type:       ErrTimeout,
		Tool:       tool,
		Message:    fmt.Sprintf("command timed out after %ds", timeout),
		Details:    truncateStr(cmd, 100),
		Suggestion: "Consider using 'background' tool for long-running commands, or break the task into smaller steps.",
	}
}

func ExecError(tool, cmd string, exitCode int, output string) *ToolError {
	return &ToolError{
		Type:       ErrExec,
		Tool:       tool,
		Message:    fmt.Sprintf("command failed with exit code %d", exitCode),
		Details:    output,
		Suggestion: "Check the command syntax and ensure all required files/paths exist.",
	}
}

func NotFoundError(tool, path string) *ToolError {
	return &ToolError{
		Type:       ErrNotFound,
		Tool:       tool,
		Message:    fmt.Sprintf("file not found: %s", path),
		Suggestion: "Verify the path exists. Use 'bash' with 'ls' to list directory contents.",
	}
}

func PermissionError(tool, path string) *ToolError {
	return &ToolError{
		Type:       ErrPermission,
		Tool:       tool,
		Message:    fmt.Sprintf("permission denied: %s", path),
		Suggestion: "Check file permissions or try a different path.",
	}
}

func ParseError(tool, reason string, input ...string) *ToolError {
	var details string
	if len(input) > 0 {
		details = strings.TrimSpace(input[0])
	}
	return &ToolError{
		Type:       ErrParse,
		Tool:       tool,
		Message:    fmt.Sprintf("failed to parse arguments: %s", reason),
		Details:    details,
		Suggestion: "Check the tool schema and provide valid JSON arguments.",
	}
}

func DuplicateError(tool, details string) *ToolError {
	return &ToolError{
		Type:       ErrDuplicate,
		Tool:       tool,
		Message:    "duplicate tool call blocked in the same turn",
		Details:    details,
		Suggestion: "Reuse the previous result or choose a different tool/action instead of repeating the same call.",
	}
}

func WrapError(tool string, err error) *ToolError {
	if te, ok := err.(*ToolError); ok {
		return te
	}

	if err == context.DeadlineExceeded {
		return &ToolError{
			Type:       ErrTimeout,
			Tool:       tool,
			Message:    "operation timed out",
			Suggestion: "Consider using 'background' tool for long-running operations.",
		}
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		return &ToolError{
			Type:    ErrExec,
			Tool:    tool,
			Message: fmt.Sprintf("exit code %d", exitErr.ExitCode()),
		}
	}

	return &ToolError{
		Type:    ErrUnknown,
		Tool:    tool,
		Message: err.Error(),
	}
}

func FormatErrorForLLM(tool string, err error) string {
	te := WrapError(tool, err)
	return te.ForLLM()
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
