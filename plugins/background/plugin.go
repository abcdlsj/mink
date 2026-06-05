package background

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/command"
	taskpkg "github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

type runner struct {
	app       *app.App
	workspace string
	childEnv  []string
	timeout   time.Duration
}

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterTool(&runner{
			app:       a,
			workspace: a.Workspace(),
			childEnv:  a.ChildEnv(),
			timeout:   30 * time.Minute,
		})
		return nil
	}
}

func (r *runner) Name() string            { return "background" }
func (r *runner) Risk() tool.RiskCategory { return tool.RiskShell }

func (r *runner) Desc() string {
	return "Run a shell command in the background and send the result back later"
}

func (r *runner) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd": map[string]any{"type": "string", "description": "Shell command"},
			"cwd": map[string]any{"type": "string", "description": "Working directory"},
		},
		"required": []string{"cmd"},
	}
}

func (r *runner) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Cmd string `json:"cmd"`
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("background: parse error: %w", err)
	}
	if strings.TrimSpace(in.Cmd) == "" {
		return "", fmt.Errorf("cmd is required")
	}
	id := taskID()
	policy := command.EntrypointPolicy("background:" + id)
	src := command.SourceFrom(ctx)
	if policy.Delivery == command.DeliveryNotice {
		src = command.NoticeSourceFrom(ctx)
	}
	taskID := r.createTask(id, src, strings.TrimSpace(in.Cwd), in.Cmd)
	go r.run(id, taskID, src, strings.TrimSpace(in.Cwd), in.Cmd)
	return fmt.Sprintf("background task started: %s", id), nil
}

func (r *runner) createTask(id, source, cwd, commandText string) string {
	if r.app == nil || r.app.Tasks() == nil {
		return ""
	}
	state := taskpkg.TaskState{
		Goal:       "Run background command",
		Todo:       []string{"execute command", "publish result notice"},
		Checkpoint: "queued",
		RelatedIDs: []string{id, source},
	}
	if cwd != "" {
		state.Artifacts = []string{cwd}
	}
	tk, err := r.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          "background",
		TriggerMessageID: id,
		InitiatorID:      "system",
		WorkerID:         "background",
		Title:            taskTitle(commandText),
		Source:           source,
		State:            state,
	})
	if err != nil {
		return ""
	}
	return tk.ID
}

func (r *runner) run(id, taskID, source, cwd, commandText string) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	runID := r.startTask(taskID, source)

	cmd := exec.CommandContext(ctx, "bash", "-lc", commandText)
	if len(r.childEnv) > 0 {
		cmd.Env = r.childEnv
	}
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	} else if r.workspace != "" {
		cmd.Dir = r.workspace
	}
	out, err := cmd.CombinedOutput()
	var sb strings.Builder
	fmt.Fprintf(&sb, "[background %s]\n$ %s\n", id, commandText)
	text := strings.TrimSpace(string(out))
	if text != "" {
		sb.WriteString(text)
		sb.WriteByte('\n')
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		fmt.Fprintf(&sb, "error: timeout after %s", r.timeout)
		r.finishTask(taskID, runID, taskpkg.StatusFailed, "timeout", nil, []string{ctx.Err().Error()})
	case err != nil:
		fmt.Fprintf(&sb, "error: %s", err)
		r.finishTask(taskID, runID, taskpkg.StatusFailed, "failed", nil, []string{err.Error()})
	default:
		sb.WriteString("done")
		r.finishTask(taskID, runID, taskpkg.StatusFinished, "done", []string{source}, nil)
	}
	r.app.PublishNotice(source, strings.TrimSpace(sb.String()))
}

func (r *runner) startTask(taskID, source string) string {
	if r.app == nil || r.app.Tasks() == nil || taskID == "" {
		return ""
	}
	state := taskpkg.TaskState{
		Goal:       "Run background command",
		Todo:       []string{"publish result notice"},
		Checkpoint: "running",
		RelatedIDs: []string{source},
	}
	run, err := r.app.Tasks().StartRun(taskID, state)
	if err != nil {
		return ""
	}
	_, _ = r.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning, State: &state})
	return run.ID
}

func (r *runner) finishTask(taskID, runID string, status taskpkg.Status, checkpoint string, artifacts, blockers []string) {
	if r.app == nil || r.app.Tasks() == nil || taskID == "" {
		return
	}
	state := taskpkg.TaskState{
		Goal:       "Run background command",
		Checkpoint: checkpoint,
		Artifacts:  artifacts,
		Blockers:   blockers,
	}
	_, _ = r.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: status, Outcome: checkpoint, State: &state})
	if runID != "" {
		_, _ = r.app.Tasks().FinishRun(runID, taskpkg.FinishRunInput{Status: status, State: &state})
	}
}

func taskID() string {
	return fmt.Sprintf("task-%08x", rand.Uint32())
}

func taskTitle(s string) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) > taskpkg.MaxTitleLen {
		return string(rs[:taskpkg.MaxTitleLen])
	}
	return s
}
