package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

type Approval int

const (
	Denied Approval = iota
	AllowOnce
	AllowAlways
)

type Request struct {
	Tool    string
	Action  string
	Pattern string
}

type Approver interface {
	Approve(context.Context, Request) (Approval, error)
}

type PolicyGuard struct {
	workspace string
	perms     *Permissions
	approver  Approver
}

type permFile struct {
	Permissions struct {
		Allow []string `json:"allow"`
	} `json:"permissions"`
}

type Permissions struct {
	mu      sync.RWMutex
	path    string
	rules   []string
	session map[string]struct{}
}

func NewPolicyGuard(workspace, path string) *PolicyGuard {
	return &PolicyGuard{
		workspace: workspace,
		perms:     NewPermissions(path),
	}
}

func (g *PolicyGuard) SetApprover(a Approver) {
	g.approver = a
}

func (g *PolicyGuard) Allow(ctx context.Context, call Call) (bool, error) {
	if !needsApproval(g.workspace, call) {
		return true, nil
	}
	if g.perms != nil && g.perms.Check(call.Action) {
		return true, nil
	}
	if g.approver == nil {
		return false, nil
	}
	req := Request{
		Tool:    call.Tool,
		Action:  call.Action,
		Pattern: PatternFor(call.Action),
	}
	approval, err := g.approver.Approve(ctx, req)
	if err != nil {
		return false, err
	}
	switch approval {
	case AllowAlways:
		if g.perms != nil && req.Pattern != "" {
			if err := g.perms.AllowPersist(req.Pattern); err != nil {
				return false, err
			}
		}
		return true, nil
	case AllowOnce:
		if g.perms != nil {
			g.perms.AllowSession(call.Action)
		}
		return true, nil
	default:
		return false, nil
	}
}

func needsApproval(workspace string, call Call) bool {
	switch call.Tool {
	case "bash":
		cmd, ok := strings.CutPrefix(call.Action, "bash ")
		return ok && IsDangerous(cmd)
	case "read":
		path, ok := strings.CutPrefix(call.Action, "read ")
		return ok && isSensitiveReadPath(path)
	case "write", "edit":
		path, ok := strings.CutPrefix(call.Action, call.Tool+" ")
		return ok && shouldGuardWritePath(workspace, path)
	default:
		return false
	}
}

func NewPermissions(path string) *Permissions {
	p := &Permissions{
		path:    path,
		session: make(map[string]struct{}),
	}
	p.load()
	return p
}

func (p *Permissions) Check(cmd string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.session[cmd]; ok {
		return true
	}
	for _, rule := range p.rules {
		if matchRule(rule, cmd) {
			return true
		}
	}
	return false
}

func (p *Permissions) AllowSession(cmd string) {
	p.mu.Lock()
	p.session[cmd] = struct{}{}
	p.mu.Unlock()
}

func (p *Permissions) AllowPersist(pattern string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.rules, pattern) {
		return nil
	}
	p.rules = append(p.rules, pattern)
	return p.save()
}

func PatternFor(cmd string) string {
	if readPath, ok := strings.CutPrefix(cmd, "read "); ok {
		norm := strings.ToLower(filepath.ToSlash(readPath))
		for _, mark := range sensitiveReadMarks {
			if idx := strings.Index(norm, mark); idx >= 0 {
				return "read:" + readPath[:idx+len(mark)] + "*"
			}
		}
		return "read:" + readPath
	}
	parts := strings.Fields(cmd)
	if len(parts) > 0 {
		return "bash:" + parts[0]
	}
	return cmd
}

func matchRule(rule, cmd string) bool {
	if pattern, ok := strings.CutPrefix(rule, "read:"); ok {
		path, ok := strings.CutPrefix(cmd, "read ")
		if !ok {
			return false
		}
		if dir, ok := strings.CutSuffix(pattern, "*"); ok {
			return strings.HasPrefix(path, dir)
		}
		return path == pattern
	}
	if prefix, ok := strings.CutPrefix(rule, "bash:"); ok {
		return cmd == prefix || strings.HasPrefix(cmd, prefix+" ") || strings.HasPrefix(cmd, prefix+"\t")
	}
	return cmd == rule
}

func (p *Permissions) load() {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var f permFile
	if json.Unmarshal(data, &f) == nil {
		p.rules = f.Permissions.Allow
	}
}

func (p *Permissions) save() error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	f := permFile{}
	f.Permissions.Allow = p.rules
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, append(data, '\n'), 0o644)
}
