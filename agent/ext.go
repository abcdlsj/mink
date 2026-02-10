package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type ExtTool struct {
	Name   string
	Desc   string
	Schema map[string]any
	Run    func(ctx context.Context, args json.RawMessage) (string, error)
}

type ExtensionManager struct {
	tools    map[string]*ExtTool
	cmds     map[string]func([]string) (string, error)
	watchers []*fsnotify.Watcher
	dirs     []string
	mu       sync.RWMutex
	onReload func()
}

func NewExtManager() *ExtensionManager {
	return &ExtensionManager{
		tools: make(map[string]*ExtTool),
		cmds:  make(map[string]func([]string) (string, error)),
	}
}

func (m *ExtensionManager) LoadDir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, e := range ents {
		if e.IsDir() {
			if m.isSkill(filepath.Join(dir, e.Name())) {
				m.loadSkill(e.Name(), filepath.Join(dir, e.Name()))
			}
			continue
		}
		if m.isExec(e) {
			m.regExec(e.Name(), filepath.Join(dir, e.Name()))
		}
	}

	m.dirs = append(m.dirs, dir)
	return nil
}

func (m *ExtensionManager) Watch() error {
	for _, dir := range m.dirs {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}

		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return w.Add(path)
			}
			return nil
		})

		m.watchers = append(m.watchers, w)
		go m.watchLoop(w, dir)
	}
	return nil
}

func (m *ExtensionManager) watchLoop(w *fsnotify.Watcher, dir string) {
	for {
		select {
		case e, ok := <-w.Events:
			if !ok {
				return
			}
			if e.Op&fsnotify.Write == fsnotify.Write || e.Op&fsnotify.Create == fsnotify.Create {
				m.reload(dir)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
		}
	}
}

func (m *ExtensionManager) Stop() {
	for _, w := range m.watchers {
		w.Close()
	}
}

func (m *ExtensionManager) OnReload(fn func()) {
	m.onReload = fn
}

func (m *ExtensionManager) reload(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		delete(m.tools, e.Name())
	}

	for _, e := range ents {
		if e.IsDir() {
			if m.isSkill(filepath.Join(dir, e.Name())) {
				m.loadSkillUnsafe(e.Name(), filepath.Join(dir, e.Name()))
			}
		} else if m.isExec(e) {
			m.regExecUnsafe(e.Name(), filepath.Join(dir, e.Name()))
		}
	}

	if m.onReload != nil {
		m.onReload()
	}
}

func (m *ExtensionManager) isSkill(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "README.md"))
	return err == nil
}

func (m *ExtensionManager) loadSkill(name, dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadSkillUnsafe(name, dir)
}

func (m *ExtensionManager) loadSkillUnsafe(name, dir string) {
	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))

	ents, _ := os.ReadDir(dir)
	var binPath string
	for _, e := range ents {
		if !e.IsDir() && m.isExec(e) {
			binPath = filepath.Join(dir, e.Name())
			break
		}
	}
	if binPath == "" {
		return
	}

	m.tools[name] = &ExtTool{
		Name:   name,
		Desc:   string(readme),
		Schema: map[string]any{"type": "object", "properties": map[string]any{"input": map[string]string{"type": "string"}}},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct{ Input string }
			json.Unmarshal(args, &p)
			c := exec.CommandContext(ctx, binPath)
			if p.Input != "" {
				c.Stdin = strings.NewReader(p.Input)
			}
			out, err := c.CombinedOutput()
			return string(out), err
		},
	}
}

func (m *ExtensionManager) regExec(name, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.regExecUnsafe(name, path)
}

func (m *ExtensionManager) regExecUnsafe(name, path string) {
	m.tools[name] = &ExtTool{
		Name:   name,
		Desc:   fmt.Sprintf("tool: %s", name),
		Schema: map[string]any{"type": "object", "properties": map[string]any{"args": map[string]string{"type": "string"}}},
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct{ Args string }
			json.Unmarshal(args, &p)
			c := exec.CommandContext(ctx, path)
			if p.Args != "" {
				c.Args = append(c.Args, strings.Fields(p.Args)...)
			}
			out, err := c.CombinedOutput()
			return string(out), err
		},
	}
}

func (m *ExtensionManager) isExec(e os.DirEntry) bool {
	i, err := e.Info()
	if err != nil {
		return false
	}
	return i.Mode()&0111 != 0
}

func (m *ExtensionManager) RegCmd(name string, fn func([]string) (string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cmds[name] = fn
}

func (m *ExtensionManager) Cmd(name string, args []string) (string, error) {
	m.mu.RLock()
	fn, ok := m.cmds[name]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown: %s", name)
	}
	return fn(args)
}

func (m *ExtensionManager) Get(name string) *ExtTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tools[name]
}

func (m *ExtensionManager) Tools() []*ExtTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var r []*ExtTool
	for _, t := range m.tools {
		r = append(r, t)
	}
	return r
}
