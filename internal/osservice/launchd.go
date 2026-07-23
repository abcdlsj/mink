package osservice

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"path/filepath"
)

func (manager *Manager) installLaunchd(ctx context.Context, config InstallConfig) error {
	for _, component := range []Component{Server, Computer} {
		payload, err := manager.launchdUnit(component, config)
		if err != nil {
			return err
		}
		if err := writeUnit(manager.unitPath(component), payload); err != nil {
			return err
		}
		_ = manager.runner.Run(ctx, "/bin/launchctl", "bootout", manager.domainTarget(component))
	}
	return nil
}

func (manager *Manager) startLaunchd(ctx context.Context, component Component) error {
	if err := manager.runner.Run(ctx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", manager.uid), manager.unitPath(component)); err != nil && !manager.Running(ctx, component) {
		return err
	}
	return manager.runner.Run(ctx, "/bin/launchctl", "kickstart", "-k", manager.domainTarget(component))
}

func (manager *Manager) stopLaunchd(ctx context.Context, component Component) error {
	if !manager.Running(ctx, component) {
		return nil
	}
	return manager.runner.Run(ctx, "/bin/launchctl", "bootout", manager.domainTarget(component))
}

func (manager *Manager) domainTarget(component Component) string {
	return fmt.Sprintf("gui/%d/%s", manager.uid, manager.labels[component])
}

func (manager *Manager) launchdUnit(component Component, config InstallConfig) ([]byte, error) {
	arguments := []string{config.Binary, string(component), "run", "--data-root", config.DataRoot}
	if component == Server {
		arguments = append(arguments, "--web-root", config.WebRoot)
	}
	var payload bytes.Buffer
	payload.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>`)
	writeXML(&payload, manager.labels[component])
	payload.WriteString(`</string><key>ProgramArguments</key><array>`)
	for _, argument := range arguments {
		payload.WriteString("<string>")
		writeXML(&payload, argument)
		payload.WriteString("</string>")
	}
	payload.WriteString(`</array><key>EnvironmentVariables</key><dict><key>HOME</key><string>`)
	writeXML(&payload, manager.home)
	payload.WriteString(`</string><key>PATH</key><string></string></dict><key>RunAtLoad</key><false/><key>KeepAlive</key><true/>`)
	for _, stream := range []struct {
		key  string
		name string
	}{{"StandardOutPath", string(component) + ".log"}, {"StandardErrorPath", string(component) + ".log"}} {
		payload.WriteString("<key>" + stream.key + "</key><string>")
		writeXML(&payload, filepath.Join(config.DataRoot, "logs", stream.name))
		payload.WriteString("</string>")
	}
	payload.WriteString(`</dict></plist>`)
	return payload.Bytes(), nil
}

func writeXML(buffer *bytes.Buffer, value string) {
	_ = xml.EscapeText(buffer, []byte(value))
}
