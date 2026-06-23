package external

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/textutil"
)

type Driver struct {
	Name                 string
	Command              string
	StdinPrompt          bool
	BuildArgs            func(prompt, workDir, sessionID string, resume bool) []string
	BuildArgsWithProfile func(prompt, workDir, sessionID string, resume bool, profile Profile) []string
	ParseOutput          func(line string) *Message
	FormatHistory        func(messages []msg.Message) string
	RuntimeMeta          func(context.Context) map[string]string
}

type Profile struct {
	Isolated     bool
	Runtime      string
	Root         string
	Home         string
	CodexHome    string
	SettingsPath string
	PluginDirs   []string
	Env          []string
}

type MessageType int

const (
	MsgAssistantText MessageType = iota
	MsgStreamChunk
	MsgThinkingChunk
	MsgToolCall
	MsgToolResult
	MsgTurnDone
	MsgRuntimeMeta
	MsgError
)

type Message struct {
	Type     MessageType
	Text     string
	Snapshot bool
	ToolName string
	ToolArgs string
	ToolID   string
	Stderr   string
	ExitCode int
	IsError  bool
	Usage    *msg.TokenUsage
	Model    string
	CostUSD  float64
	Reason   string
	Meta     map[string]string
	Error    error
}

func NewRuntime(driver Driver) agent.RuntimeFactory {
	return func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		if strings.TrimSpace(driver.Command) == "" {
			return nil, fmt.Errorf("external runtime command is empty")
		}
		return &Runtime{
			driver:    driver,
			env:       env,
			workspace: env.Workspace,
		}, nil
	}
}

type Runtime struct {
	driver    Driver
	env       *agent.RuntimeEnv
	workspace string
}

func (r *Runtime) Run(ctx context.Context, turn *agent.Turn) error {
	profile, err := r.prepareProfile()
	if err != nil {
		return err
	}
	sessionID, resume := "", false
	if !profile.Isolated {
		sessionID, resume = r.externalSession(turn)
	}
	prompt := textutil.Valid(r.buildPrompt(turn, !resume || turn.IncludeHistory))
	fallbackPrompt := textutil.Valid(r.buildPrompt(turn, true))
	addUser(turn.Session, turn.Input)

	st := newRunState()
	runErr := r.runCommand(ctx, turn, st, prompt, sessionID, resume, profile)
	if runErr != nil && resume && !profile.Isolated && missingExternalSession(runErr) {
		sessionID = r.resetSessionID(turn.Session)
		st = newRunState()
		runErr = r.runCommand(ctx, turn, st, fallbackPrompt, sessionID, false, profile)
	}
	st.flush(turn.Session)
	return runErr
}

func (r *Runtime) externalSession(turn *agent.Turn) (string, bool) {
	if turn != nil && turn.DisableExternalResume {
		return "", false
	}
	if turn == nil {
		return "", false
	}
	return r.getOrCreateSessionID(turn.Session)
}

func newRunState() *runState {
	return &runState{calls: map[string]toolCallState{}}
}

func (r *Runtime) runCommand(ctx context.Context, turn *agent.Turn, st *runState, prompt, sessionID string, resume bool, profile Profile) error {
	if r.driver.RuntimeMeta != nil {
		if meta := r.driver.RuntimeMeta(ctx); len(meta) > 0 {
			st.onRuntimeMeta(turn, &Message{Type: MsgRuntimeMeta, Meta: meta})
		}
	}
	cmd := exec.CommandContext(ctx, r.driver.Command, r.buildArgs(prompt, sessionID, resume, profile)...)
	cmd.Env = profile.Env
	if r.workspace != "" {
		cmd.Dir = r.workspace
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if r.driver.StdinPrompt {
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		go func() {
			_, _ = io.WriteString(in, prompt)
			in.Close()
		}()
	}
	if err := cmd.Start(); err != nil {
		return r.startError(err)
	}

	errCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(stderr)
		errCh <- strings.TrimSpace(string(data))
	}()

	scanner := bufio.NewScanner(stdout)
	const maxLine = 10 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLine)

	var runErr error
	for scanner.Scan() {
		m := r.driver.ParseOutput(scanner.Text())
		if m == nil {
			continue
		}
		if err := handleMessage(r.driver.Name, turn, st, m); err != nil {
			runErr = err
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			break
		}
	}
	if err := scanner.Err(); err != nil && runErr == nil {
		runErr = err
	}
	waitErr := cmd.Wait()
	stderrText := <-errCh
	if ctxErr := ctx.Err(); ctxErr != nil {
		return r.contextError(ctxErr)
	}
	if runErr != nil && stderrText != "" {
		runErr = fmt.Errorf("%w: %s", runErr, stderrText)
	}
	if runErr == nil && waitErr != nil {
		if stderrText != "" {
			runErr = r.exitError(errors.New(stderrText))
		} else {
			runErr = r.exitError(waitErr)
		}
	}
	return runErr
}

func (r *Runtime) buildArgs(prompt, sessionID string, resume bool, profile Profile) []string {
	if r.driver.BuildArgsWithProfile != nil {
		return r.driver.BuildArgsWithProfile(prompt, r.workspace, sessionID, resume, profile)
	}
	if r.driver.BuildArgs != nil {
		return r.driver.BuildArgs(prompt, r.workspace, sessionID, resume)
	}
	return nil
}

func (r *Runtime) runtimeLabel() string {
	name := strings.TrimSpace(r.driver.Name)
	if name == "" {
		name = strings.TrimSpace(r.driver.Command)
	}
	if name == "" {
		return "external runtime"
	}
	return name
}

func (r *Runtime) startError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s unavailable: %w", r.runtimeLabel(), err)
}

func (r *Runtime) contextError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out: %w", r.runtimeLabel(), err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s canceled: %w", r.runtimeLabel(), err)
	}
	return fmt.Errorf("%s stopped: %w", r.runtimeLabel(), err)
}

func (r *Runtime) exitError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s exited: %w", r.runtimeLabel(), err)
}

func (r *Runtime) buildPrompt(turn *agent.Turn, includeHistory bool) string {
	var hist string
	if includeHistory && turn != nil && turn.Session != nil {
		if r.driver.FormatHistory != nil {
			hist = r.driver.FormatHistory(turn.Session.Messages)
		} else {
			hist = FormatHistory(turn.Session.Messages)
		}
	}
	return agent.BuildExternalPrompt(r.env, turn, hist)
}

func (r *Runtime) prepareProfile() (Profile, error) {
	name := strings.ToLower(strings.TrimSpace(r.driver.Name))
	profile := Profile{
		Runtime: name,
		Env:     cloneEnv(r.env),
	}
	switch name {
	case "claude", "codex":
	default:
		return profile, nil
	}
	root, err := r.profileRoot(name)
	if err != nil {
		return profile, err
	}
	profile.Isolated = true
	profile.Root = root
	profile.Home = filepath.Join(root, "home")
	profile.CodexHome = filepath.Join(root, "codex")
	profile.SettingsPath = filepath.Join(root, "claude", "settings.json")
	profile.PluginDirs = []string{filepath.Join(root, "claude", "plugins")}
	if err := ensureProfileDirs(profile); err != nil {
		return profile, err
	}
	if name == "claude" {
		if err := importHostClaudeProfile(profile); err != nil {
			return profile, err
		}
		if err := ensureClaudeSettings(profile); err != nil {
			return profile, err
		}
	}
	profile.Env = isolatedProfileEnv(profile.Env, profile)
	if err := validateIsolatedAuth(profile); err != nil {
		return profile, err
	}
	return profile, nil
}

func (r *Runtime) profileRoot(name string) (string, error) {
	dataRoot := ""
	if r.env != nil {
		dataRoot = strings.TrimSpace(r.env.DataRoot)
	}
	if dataRoot == "" {
		dataRoot = ".sumi"
	}
	profile := "default"
	if r.env != nil && r.env.Persona != nil && strings.TrimSpace(r.env.Persona.ID) != "" {
		profile = r.env.Persona.ID
	}
	root := filepath.Join(dataRoot, "external", name, sanitizeProfileName(profile))
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("%s isolated profile root is empty", name)
	}
	return root, nil
}

func ensureProfileDirs(profile Profile) error {
	dirs := []string{
		profile.Root,
		profile.Home,
		filepath.Join(profile.Root, "xdg-config"),
		filepath.Join(profile.Root, "xdg-cache"),
		filepath.Join(profile.Root, "xdg-data"),
		filepath.Dir(profile.SettingsPath),
		profile.CodexHome,
	}
	dirs = append(dirs, profile.PluginDirs...)
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func ensureClaudeSettings(profile Profile) error {
	if strings.TrimSpace(profile.SettingsPath) == "" {
		return nil
	}
	if _, err := os.Stat(profile.SettingsPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(profile.SettingsPath, []byte("{}\n"), 0o600)
}

func importHostClaudeProfile(profile Profile) error {
	hostHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(hostHome) == "" {
		return nil
	}
	hostClaude := filepath.Join(hostHome, ".claude")
	profileClaude := filepath.Join(profile.Home, ".claude")
	if samePath(hostClaude, profileClaude) {
		return nil
	}
	if _, err := os.Stat(hostClaude); err != nil {
		return nil
	}
	if err := os.MkdirAll(profileClaude, 0o700); err != nil {
		return err
	}
	files := []struct {
		src string
		dst string
	}{
		{filepath.Join(hostClaude, "settings.json"), profile.SettingsPath},
		{filepath.Join(hostClaude, "settings.local.json"), filepath.Join(profileClaude, "settings.local.json")},
		{filepath.Join(hostClaude, "config.json"), filepath.Join(profileClaude, "config.json")},
		{filepath.Join(hostClaude, ".credentials.json"), filepath.Join(profileClaude, ".credentials.json")},
	}
	for _, f := range files {
		if err := copyFileIfProfileMissing(f.src, f.dst); err != nil {
			return err
		}
	}
	if len(profile.PluginDirs) > 0 {
		if err := copyDirIfProfileEmpty(filepath.Join(hostClaude, "plugins"), profile.PluginDirs[0]); err != nil {
			return err
		}
	}
	for _, name := range []string{"commands", "skills"} {
		if err := copyDirIfProfileEmpty(filepath.Join(hostClaude, name), filepath.Join(profileClaude, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyFileIfProfileMissing(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return nil
	}
	info, err := os.Lstat(src)
	if err != nil || info.IsDir() {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if !profileFileNeedsImport(dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func profileFileNeedsImport(path string) bool {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(data))
	return trimmed == "" || trimmed == "{}"
}

func copyDirIfProfileEmpty(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return nil
	}
	info, err := os.Lstat(src)
	if err != nil || !info.IsDir() {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	empty, err := dirMissingOrEmpty(dst)
	if err != nil || !empty {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFileIfProfileMissing(path, target)
	})
}

func dirMissingOrEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return aa == bb
}

func isolatedProfileEnv(base []string, profile Profile) []string {
	env := envMap(base)
	delete(env, "CODEX_HOME")
	delete(env, "CLAUDE_CONFIG_DIR")
	delete(env, "CLAUDE_HOME")
	env["HOME"] = profile.Home
	env["XDG_CONFIG_HOME"] = filepath.Join(profile.Root, "xdg-config")
	env["XDG_CACHE_HOME"] = filepath.Join(profile.Root, "xdg-cache")
	env["XDG_DATA_HOME"] = filepath.Join(profile.Root, "xdg-data")
	if profile.Runtime == "codex" {
		env["CODEX_HOME"] = profile.CodexHome
	}
	return flattenEnv(env)
}

func validateIsolatedAuth(profile Profile) error {
	env := envMap(profile.Env)
	switch profile.Runtime {
	case "claude":
		if strings.TrimSpace(env["ANTHROPIC_API_KEY"]) != "" {
			return nil
		}
		if claudeProfileHasAuth(profile) {
			return nil
		}
		return fmt.Errorf("claude isolated profile requires ANTHROPIC_API_KEY, SUMI_CLAUDE_API_KEY, or imported auth under the Sumi Claude profile; refusing to fall back to host ~/.claude")
	case "codex":
		if strings.TrimSpace(env["OPENAI_API_KEY"]) != "" {
			return nil
		}
		if strings.TrimSpace(profile.CodexHome) != "" {
			if _, err := os.Stat(filepath.Join(profile.CodexHome, "auth.json")); err == nil {
				return nil
			}
		}
		return fmt.Errorf("codex isolated profile requires OPENAI_API_KEY, SUMI_CODEX_OPENAI_API_KEY, or auth.json under Sumi CODEX_HOME; refusing to fall back to host ~/.codex")
	default:
		return nil
	}
}

func claudeProfileHasAuth(profile Profile) bool {
	for _, path := range []string{
		filepath.Join(profile.Home, ".claude", "config.json"),
		filepath.Join(profile.Home, ".claude", ".credentials.json"),
		profile.SettingsPath,
	} {
		data, err := os.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		text := strings.ToLower(string(data))
		if strings.Contains(text, "apikeyhelper") ||
			strings.Contains(text, "api_key") ||
			strings.Contains(text, "access_token") ||
			strings.Contains(text, "accesstoken") ||
			strings.Contains(text, "refresh_token") ||
			strings.Contains(text, "refreshtoken") ||
			strings.Contains(text, "oauth") {
			return true
		}
	}
	return false
}

func cloneEnv(env *agent.RuntimeEnv) []string {
	if env == nil || len(env.ChildEnv) == 0 {
		return nil
	}
	return append([]string(nil), env.ChildEnv...)
}

func sanitizeProfileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == '_', r == '-', r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}

func envMap(src []string) map[string]string {
	out := make(map[string]string, len(src))
	for _, entry := range src {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.Contains(key, "=") {
			continue
		}
		out[key] = value
	}
	return out
}

func flattenEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func (r *Runtime) getOrCreateSessionID(s *session.Session) (string, bool) {
	if s == nil {
		return "", false
	}
	if s.ExternalSession == nil {
		s.ExternalSession = map[string]string{}
	}
	key := r.sessionKey()
	if sid := s.ExternalSession[key]; sid != "" {
		return sid, true
	}
	sid := uuid.New().String()
	s.ExternalSession[key] = sid
	return sid, false
}

func (r *Runtime) resetSessionID(s *session.Session) string {
	sid := uuid.New().String()
	if s == nil {
		return sid
	}
	if s.ExternalSession == nil {
		s.ExternalSession = map[string]string{}
	}
	s.ExternalSession[r.sessionKey()] = sid
	return sid
}

func (r *Runtime) sessionKey() string {
	name := strings.TrimSpace(r.driver.Name)
	if name == "" {
		name = strings.TrimSpace(r.driver.Command)
	}
	if name == "" {
		name = "external"
	}
	workspace := strings.TrimSpace(r.workspace)
	if workspace == "" || workspace == "." {
		return name
	}
	return name + ":" + filepath.Clean(workspace)
}

func missingExternalSession(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no conversation found with session id")
}

func handleMessage(name string, turn *agent.Turn, st *runState, m *Message) error {
	switch m.Type {
	case MsgStreamChunk:
		st.onStream(turn, m.Text)
	case MsgAssistantText:
		st.onAssistant(turn, m.Text, m.Snapshot)
	case MsgThinkingChunk:
		st.onThinking(turn, m.Text)
	case MsgToolCall:
		st.onToolCall(turn, m)
	case MsgToolResult:
		st.onToolResult(turn, m)
	case MsgTurnDone:
		st.onTurnDone(turn, m)
	case MsgRuntimeMeta:
		st.onRuntimeMeta(turn, m)
	case MsgError:
		return wrapMessageError(name, m)
	}
	return nil
}
