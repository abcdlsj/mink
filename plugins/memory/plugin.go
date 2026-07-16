package memory

import (
	"os"

	"github.com/abcdlsj/sumi/app"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		s, err := open(a.Config().MemoryDir(), a.Workspace())
		if err != nil {
			return err
		}
		a.RegisterTool(&readTool{s: s})
		a.RegisterTool(&searchTool{s: s})
		a.RegisterTool(&proposeTool{s: s})
		a.RegisterTool(&rememberTool{s: s})
		a.RegisterTool(&writeTool{s: s})
		a.RegisterTool(&updateTool{s: s})
		a.RegisterTool(&deleteTool{s: s})
		return a.RegisterCommand(&cmd{s: s})
	}
}

func open(root, workspace string) (*store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &store{root: root, workspace: workspace}, nil
}
