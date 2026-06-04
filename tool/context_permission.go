package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/command"
)

func enforceRunContextPermission(ctx context.Context, t Tool, args json.RawMessage) error {
	profile := strings.TrimSpace(strings.ToLower(command.PermissionFrom(ctx)))
	if profile == "" || profile == "default" {
		return nil
	}
	switch profile {
	case "telegram":
		return enforceTelegramTool(t, args)
	case "cron":
		return enforceCronTool(t, args)
	default:
		return nil
	}
}

func enforceTelegramTool(t Tool, args json.RawMessage) error {
	switch RiskOf(t) {
	case RiskShell, RiskNetwork:
		name := t.Name()
		return permissionDenied(name, "telegram context cannot run shell or generic network tools; use notify_bark for notifications")
	default:
		return nil
	}
}

func enforceCronTool(t Tool, args json.RawMessage) error {
	switch RiskOf(t) {
	case RiskNetwork:
		return permissionDenied(t.Name(), "cron context cannot use generic network/webhook tools; use notify_bark for notifications")
	case RiskShell:
		name := t.Name()
		cmd, ok := shellCommandForTool(name, args)
		if !ok {
			return nil
		}
		if IsNetworkCommand(cmd) {
			return permissionDenied(name, "cron context cannot use shell network/webhook commands; use notify_bark for notifications")
		}
	}
	return nil
}

func shellCommandForTool(name string, args json.RawMessage) (string, bool) {
	switch name {
	case "bash", "background":
		var in struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal(args, &in) != nil {
			return "", false
		}
		cmd := strings.TrimSpace(in.Cmd)
		return cmd, cmd != ""
	default:
		return "", false
	}
}

func permissionDenied(toolName, reason string) error {
	return fmt.Errorf("permission denied: tool %s blocked: %s", toolName, reason)
}
