package agent

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

func (r *ExternalRuntime) writeMCPConfig(bridgeW *io.PipeWriter, clientR *io.PipeReader) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "mink-mcp-*")
	if err != nil {
		return "", nil, err
	}

	sockPath := filepath.Join(tmpDir, "bridge.sock")

	listener, err := startBridgeRelay(sockPath, bridgeW, clientR)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	minkBin, err := os.Executable()
	if err != nil {
		minkBin = "mink"
	}

	configPath := filepath.Join(tmpDir, "mcp.json")
	config := map[string]any{
		"mcpServers": map[string]any{
			"mink": map[string]any{
				"command": minkBin,
				"args":    []string{"mcp-bridge", "--sock", sockPath},
			},
		},
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		listener.Close()
		os.RemoveAll(tmpDir)
		return "", nil, err
	}

	cleanup := func() {
		listener.Close()
		os.RemoveAll(tmpDir)
	}
	return configPath, cleanup, nil
}
