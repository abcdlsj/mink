package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Bash struct {
	workspace string
	childEnv  []string
}

func (t *Bash) Name() string       { return "bash" }
func (t *Bash) Desc() string       { return "Run a shell command in the workspace" }
func (t *Bash) Risk() RiskCategory { return RiskShell }
func (t *Bash) Schema() map[string]any {
	return objectSchema(
		prop("cmd", "string", "Shell command to run"),
		required("cmd"),
	)
}

func (t *Bash) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decodeArgs[struct {
		Cmd string `json:"cmd"`
	}]("bash", args)
	if err != nil {
		return "", err
	}
	if err := guardCommand(in.Cmd); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", in.Cmd)
	if len(t.childEnv) > 0 {
		cmd.Env = t.childEnv
	}
	if t.workspace != "" {
		cmd.Dir = t.workspace
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), ExecError("bash", in.Cmd, exitErr.ExitCode(), string(out))
		}
		return string(out), err
	}
	return string(out), nil
}

func guardCommand(cmd string) error {
	raw := strings.ToLower(strings.TrimSpace(cmd))
	for _, token := range []string{
		"sudo ",
		"rm -rf /",
		"rm -rf ~",
		"git reset --hard",
		"mkfs",
		"shutdown",
		"reboot",
		"dd if=",
	} {
		if strings.Contains(raw, token) {
			return fmt.Errorf("command blocked: %s", token)
		}
	}
	return nil
}

func guardedCall(workspace, name string, args json.RawMessage) (Call, bool) {
	switch name {
	case "bash":
		in, err := decodeArgs[struct {
			Cmd string `json:"cmd"`
		}]("bash", args)
		if err != nil || strings.TrimSpace(in.Cmd) == "" {
			return Call{}, false
		}
		return Call{Tool: name, Action: "bash " + strings.TrimSpace(in.Cmd), Args: args}, true
	case "read", "write", "edit":
		in, err := decodeArgs[struct {
			Path string `json:"path"`
		}]("path", args)
		if err != nil {
			return Call{}, false
		}
		return guardPathCall(workspace, name, in.Path, args)
	default:
		return Call{}, false
	}
}

func guardPathCall(workspace, name, path string, args json.RawMessage) (Call, bool) {
	path = resolvePath(workspace, path)
	if strings.TrimSpace(path) == "" {
		return Call{}, false
	}
	return Call{Tool: name, Action: name + " " + path, Args: args}, true
}
