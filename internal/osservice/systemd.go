package osservice

import (
	"context"
	"fmt"
	"strings"
)

func (manager *Manager) installSystemd(ctx context.Context, config InstallConfig) error {
	for _, component := range []Component{Server, Computer} {
		if err := writeUnit(manager.unitPath(component), manager.systemdUnit(component, config)); err != nil {
			return err
		}
	}
	if err := manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	for _, component := range []Component{Server, Computer} {
		if err := manager.runner.Run(ctx, "/usr/bin/systemctl", "--user", "enable", manager.unitName(component)); err != nil {
			return err
		}
	}
	return nil
}

func (manager *Manager) systemdUnit(component Component, config InstallConfig) []byte {
	arguments := []string{config.Binary, string(component), "run", "--data-root", config.DataRoot}
	if component == Server {
		arguments = append(arguments, "--web-root", config.WebRoot)
	}
	escaped := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		escaped = append(escaped, systemdQuote(argument))
	}
	return []byte(fmt.Sprintf("[Unit]\nDescription=Sumi %s\n\n[Service]\nType=simple\nExecStart=%s\nRestart=on-failure\nEnvironment=PATH=\nEnvironment=HOME=%s\n\n[Install]\nWantedBy=default.target\n", component, strings.Join(escaped, " "), systemdQuote(manager.home)))
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "%", "%%")
	return "\"" + value + "\""
}
