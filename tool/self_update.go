package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/abcdlsj/mink/session"
)

type SessionMapper interface {
	ActiveSessions() map[string]string
}

type SelfUpdate struct {
	sm   *session.Manager
	disp SessionMapper
}

func NewSelfUpdate(sm *session.Manager, disp SessionMapper) *SelfUpdate {
	return &SelfUpdate{sm: sm, disp: disp}
}

func (s *SelfUpdate) Name() string { return "self_update" }
func (s *SelfUpdate) Desc() string {
	return "Rebuild and hot-upgrade the running daemon from local source. Compiles with go install, flushes sessions, and triggers zero-downtime upgrade. Only works in daemon mode."
}

func (s *SelfUpdate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_dir": map[string]any{
				"type":        "string",
				"description": "Source repository directory (optional, auto-detected via go list)",
			},
		},
	}
}

func (s *SelfUpdate) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		RepoDir string `json:"repo_dir"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	repoDir := params.RepoDir
	if repoDir == "" {
		dir, err := detectModuleRoot()
		if err != nil {
			return "", fmt.Errorf("cannot detect repo dir: %w (pass repo_dir explicitly)", err)
		}
		repoDir = dir
	}

	// 1. compile
	cmd := exec.CommandContext(ctx, "go", "install", "./cmd/mink/")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build failed:\n%s\n%w", strings.TrimSpace(string(output)), err)
	}

	// 2. flush all sessions
	if err := s.sm.FlushAll(); err != nil {
		return "", fmt.Errorf("flush sessions: %w", err)
	}

	// 3. collect session mappings
	sessions := s.disp.ActiveSessions()

	// 4. write upgrade state
	state := UpgradeState{
		Sessions:     sessions,
		ResumePrompt: "Self-update completed. Binary rebuilt and upgraded successfully.",
	}
	if err := writeUpgradeState(state); err != nil {
		return "", fmt.Errorf("write upgrade state: %w", err)
	}

	// 5. trigger upgrade (this will not return normally)
	go func() {
		time.Sleep(50 * time.Millisecond)
		syscall.Kill(os.Getpid(), syscall.SIGUSR2)
	}()

	return "upgrade signal sent, restarting...", nil
}

type UpgradeState struct {
	Sessions     map[string]string `json:"sessions"`
	ResumePrompt string           `json:"resume_prompt"`
}

func upgradeStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".mink", "upgrade_state.json")
}

func writeUpgradeState(s UpgradeState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(upgradeStatePath(), data, 0644)
}

func LoadUpgradeState() (*UpgradeState, error) {
	data, err := os.ReadFile(upgradeStatePath())
	if err != nil {
		return nil, err
	}
	var s UpgradeState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func RemoveUpgradeState() {
	os.Remove(upgradeStatePath())
}

func detectModuleRoot() (string, error) {
	// try cwd first (daemon usually starts from repo dir)
	if _, err := os.Stat("go.mod"); err == nil {
		abs, _ := filepath.Abs(".")
		return abs, nil
	}
	// fallback: go list in cwd
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("no go.mod in cwd and go list failed: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("empty module root")
	}
	return dir, nil
}
