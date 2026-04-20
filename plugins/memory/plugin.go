package memory

import (
	"os"

	"github.com/abcdlsj/mink/app"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		s, err := open(a.Config().MemoryDir(), a.Workspace())
		if err != nil {
			return err
		}
		a.RegisterTool(&readTool{s: s})
		a.RegisterTool(&searchTool{s: s})
		a.RegisterTool(&writeTool{s: s})
		a.RegisterCommand(&cmd{s: s})
		return nil
	}
}

func open(root, workspace string) (*store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &store{root: root, workspace: workspace}, nil
}
