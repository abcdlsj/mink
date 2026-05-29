package collab

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
	"github.com/google/uuid"
)

var (
	ErrLegacyDelegateNoSpace = errors.New("delegate: no space anchor; routed through legacy in-memory path")
	ErrUnknownWorker         = errors.New("delegate: worker persona not registered")
)

type spaceDelegateInput struct {
	ParentSpaceID    string
	TriggerMessageID string
	InitiatorID      string
	WorkerID         string
	Title            string
	Input            string
	Runtime          string
	Source           string
	ShareContext     bool
}

func (m *manager) resolveSpaceAnchor(source string) (string, string, bool) {
	if m.app == nil || m.app.Spaces() == nil {
		return "", "", false
	}
	target := space.MapSource(source)
	if target.Kind == "" {
		return "", "", false
	}
	sp, err := m.app.Spaces().EnsureForSource(source, space.PersonaInfo{ID: target.Seed})
	if err != nil || sp == nil {
		return "", "", false
	}
	if len(sp.Messages) == 0 {
		return sp.ID, "", true
	}
	return sp.ID, sp.Messages[len(sp.Messages)-1].ID, true
}

func (m *manager) tryDelegateInSpace(ctx context.Context, source, workerID, runtime, input string) (string, bool, error) {
	spaceID, triggerID, ok := m.resolveSpaceAnchor(source)
	if !ok || triggerID == "" {
		return "", false, nil
	}
	if strings.TrimSpace(workerID) == "" {
		return "", true, ErrUnknownWorker
	}
	worker := m.app.Personas().Get(workerID)
	if worker == nil {
		return "", true, ErrUnknownWorker
	}
	id, err := m.delegateAsync(ctx, spaceDelegateInput{
		ParentSpaceID:    spaceID,
		TriggerMessageID: triggerID,
		InitiatorID:      command.PersonaFrom(ctx),
		WorkerID:         worker.ID,
		Title:            input,
		Input:            input,
		Runtime:          runtime,
		Source:           source,
	})
	return id, true, err
}

func (m *manager) tryDelegateInSpaceForAlias(ctx context.Context, source, alias, input string) (string, bool, error) {
	spaceID, triggerID, ok := m.resolveSpaceAnchor(source)
	if !ok || triggerID == "" {
		return "", false, nil
	}
	worker, err := m.resolveCollabWorkerPersona(source, alias)
	if err != nil {
		return "", true, err
	}
	rt := strings.TrimSpace(worker.Runtime)
	if rt == "" {
		rt = m.app.Config().Runtime
	}
	id, err := m.runWorkerAsTask(ctx, workerRunInput{
		Source:           source,
		ParentSpaceID:    spaceID,
		TriggerMessageID: triggerID,
		InitiatorID:      initiatorOrUser(ctx),
		WorkerID:         worker.ID,
		Runtime:          rt,
		Title:            input,
		Input:            input,
		ShareContext:     true,
	})
	return id, true, err
}

func (m *manager) tryMentionInSpace(ctx context.Context, source string, in mentionArgs) (string, bool, error) {
	spaceID, triggerID, ok := m.resolveSpaceAnchor(source)
	if !ok || triggerID == "" {
		return "", false, nil
	}
	worker, err := m.resolveCollabWorkerPersona(source, in.AgentID)
	if err != nil {
		return "", true, err
	}
	rt := strings.TrimSpace(worker.Runtime)
	if rt == "" {
		rt = m.app.Config().Runtime
	}
	resultID, err := m.runWorkerAsMention(ctx, workerRunInput{
		Source:           source,
		ParentSpaceID:    spaceID,
		TriggerMessageID: triggerID,
		InitiatorID:      initiatorOrUser(ctx),
		WorkerID:         worker.ID,
		Runtime:          rt,
		Input:            in.Question,
	})
	if err != nil {
		return "", true, err
	}
	return fmt.Sprintf("mentioned %s, message=%s", worker.Display, shortMessageRef(resultID)), true, nil
}

func (m *manager) trySpawnInSpace(ctx context.Context, source string, in spawnArgs, runtime string) (string, bool, error) {
	spaceID, triggerID, ok := m.resolveSpaceAnchor(source)
	if !ok || triggerID == "" {
		return "", false, nil
	}
	worker, err := m.resolveCollabWorkerPersona(source, in.Runtime)
	if err != nil {
		return "", true, err
	}
	rt := strings.TrimSpace(worker.Runtime)
	if rt == "" {
		rt = runtime
	}
	timeout := time.Duration(m.app.Config().Collab.PollTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	outcome, runErr := m.runWorkerSync(ctx, workerRunInput{
		Source:           source,
		ParentSpaceID:    spaceID,
		TriggerMessageID: triggerID,
		InitiatorID:      initiatorOrUser(ctx),
		WorkerID:         worker.ID,
		Runtime:          rt,
		Title:            in.Task,
		Input:            in.Task,
		ShareContext:     in.ShareContext,
	}, timeout)
	if runErr != nil {
		return "", true, runErr
	}
	return formatSpawnReturn(outcome), true, nil
}

func initiatorOrUser(ctx context.Context) string {
	if id := strings.TrimSpace(command.PersonaFrom(ctx)); id != "" {
		return id
	}
	return "user"
}

func formatSpawnReturn(out workerRunOutcome) string {
	parts := []string{"spawn " + string(out.Status)}
	if out.ResultMessageID != "" {
		parts = append(parts, "message="+shortMessageRef(out.ResultMessageID))
	}
	if out.Outcome != "" {
		parts = append(parts, "outcome="+out.Outcome)
	}
	return strings.Join(parts, ", ")
}

func shortMessageRef(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (m *manager) delegateAsync(ctx context.Context, in spaceDelegateInput) (string, error) {
	if strings.TrimSpace(in.WorkerID) == "" {
		return "", ErrUnknownWorker
	}
	worker := m.app.Personas().Get(in.WorkerID)
	if worker == nil {
		return "", ErrUnknownWorker
	}
	tk, err := m.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          in.ParentSpaceID,
		TriggerMessageID: in.TriggerMessageID,
		InitiatorID:      in.InitiatorID,
		WorkerID:         worker.ID,
		Title:            shortTitle(in.Title, in.Input),
		Source:           in.Source,
	})
	if err != nil {
		return "", err
	}
	go func() {
		bgCtx := context.Background()
		_, _ = m.runSpaceDelegate(bgCtx, tk, in, worker)
	}()
	return tk.ID, nil
}

func (m *manager) runSpaceDelegate(ctx context.Context, tk *taskpkg.Task, in spaceDelegateInput, worker *persona.Persona) (*spaceDelegateOutcome, error) {
	r, err := m.app.Tasks().StartRun(tk.ID)
	if err != nil {
		return nil, err
	}
	if _, err := m.app.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusRunning}); err != nil {
		return nil, err
	}
	scratch := session.New("subtask:" + uuid.NewString()[:8])
	if in.ShareContext {
		if parent, err := m.app.CurrentSession(in.Source); err == nil && parent != nil {
			for _, pm := range cloneMessages(parent.Messages) {
				scratch.Add(pm)
			}
		}
	}
	scratch.Add(msg.Message{Role: "user", Content: in.Input})
	baseline := len(scratch.Messages)
	rt, err := m.app.NewRuntimeFor(in.Runtime, worker)
	if err != nil {
		_ = m.failTask(tk.ID, r.ID, err)
		return nil, err
	}
	streamID := "stream-" + uuid.NewString()[:8]
	turn := &agent.Turn{
		Source:          scratch.Source,
		Input:           in.Input,
		Session:         scratch,
		Bus:             m.app.Bus(),
		SpaceID:         in.ParentSpaceID,
		ParentMessageID: in.TriggerMessageID,
		AgentID:         worker.ID,
		StreamID:        streamID,
	}
	m.app.Bus().Publish(bus.Event{
		Type:            bus.TurnStarted,
		Source:          scratch.Source,
		SessionID:       scratch.ID,
		TaskID:          tk.ID,
		SpaceID:         turn.SpaceID,
		ParentMessageID: turn.ParentMessageID,
		AgentID:         turn.AgentID,
		StreamID:        turn.StreamID,
	})
	runErr := rt.Run(ctx, turn)
	if runErr != nil {
		m.app.Bus().Publish(bus.Event{
			Type:            bus.TurnError,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			TaskID:          tk.ID,
			Err:             runErr.Error(),
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
	} else {
		m.app.Bus().Publish(bus.Event{
			Type:            bus.TurnFinished,
			Source:          scratch.Source,
			SessionID:       scratch.ID,
			TaskID:          tk.ID,
			SpaceID:         turn.SpaceID,
			ParentMessageID: turn.ParentMessageID,
			AgentID:         turn.AgentID,
			StreamID:        turn.StreamID,
		})
	}
	added := scratch.Messages[baseline:]
	steps := summarizeAddedSteps(added, runErr)
	if ctx.Err() != nil && runErr != nil {
		_ = m.cancelTask(tk.ID, r.ID, steps, ctx.Err())
		return &spaceDelegateOutcome{Task: tk, Run: r, Status: taskpkg.StatusCanceled, Err: ctx.Err()}, nil
	}
	if runErr != nil {
		_ = m.finishRunFinal(tk.ID, r.ID, taskpkg.StatusFailed, steps, runErr.Error())
		return &spaceDelegateOutcome{Task: tk, Run: r, Status: taskpkg.StatusFailed, Err: runErr}, nil
	}
	content, reasoning := assembleAddedAssistantOutput(added)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		_ = m.finishRunFinal(tk.ID, r.ID, taskpkg.StatusEmptyOutput, steps, "no output")
		return &spaceDelegateOutcome{Task: tk, Run: r, Status: taskpkg.StatusEmptyOutput}, nil
	}
	resolved := space.ParseMentions(content, nil, 0)
	written, err := m.app.Spaces().AppendAgentMessage(
		in.ParentSpaceID,
		space.PersonaInfo{ID: worker.ID, Display: worker.Display, Role: worker.Description},
		content,
		reasoning,
		resolved,
		in.TriggerMessageID,
	)
	if err != nil {
		_ = m.finishRunFinal(tk.ID, r.ID, taskpkg.StatusFailed, steps, err.Error())
		return &spaceDelegateOutcome{Task: tk, Run: r, Status: taskpkg.StatusFailed, Err: err}, nil
	}
	outcome := outcomeFromContent(content)
	if _, err := m.app.Tasks().Update(tk.ID, taskpkg.UpdateTaskInput{
		Status:          taskpkg.StatusFinished,
		ResultMessageID: written.ID,
		Outcome:         outcome,
	}); err != nil {
		return nil, err
	}
	if _, err := m.app.Tasks().FinishRun(r.ID, taskpkg.FinishRunInput{
		Status:   taskpkg.StatusFinished,
		KeySteps: steps,
	}); err != nil {
		return nil, err
	}
	m.app.Bus().Publish(bus.Event{
		Type:   bus.DelegateFinished,
		Source: in.Source,
		TaskID: tk.ID,
		Output: outcome,
	})
	return &spaceDelegateOutcome{
		Task:            tk,
		Run:             r,
		Status:          taskpkg.StatusFinished,
		ResultMessageID: written.ID,
	}, nil
}

func (m *manager) delegateInSpace(ctx context.Context, in spaceDelegateInput) (*spaceDelegateOutcome, error) {
	if strings.TrimSpace(in.ParentSpaceID) == "" || strings.TrimSpace(in.TriggerMessageID) == "" {
		return nil, ErrLegacyDelegateNoSpace
	}
	if strings.TrimSpace(in.WorkerID) == "" {
		return nil, ErrUnknownWorker
	}
	worker := m.app.Personas().Get(in.WorkerID)
	if worker == nil {
		return nil, ErrUnknownWorker
	}
	tk, err := m.app.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          in.ParentSpaceID,
		TriggerMessageID: in.TriggerMessageID,
		InitiatorID:      in.InitiatorID,
		WorkerID:         worker.ID,
		Title:            shortTitle(in.Title, in.Input),
		Source:           in.Source,
	})
	if err != nil {
		return nil, err
	}
	return m.runSpaceDelegate(ctx, tk, in, worker)
}

type spaceDelegateOutcome struct {
	Task            *taskpkg.Task
	Run             *taskpkg.Run
	Status          taskpkg.Status
	ResultMessageID string
	Err             error
}

func (m *manager) failTask(taskID, runID string, runErr error) error {
	steps := []taskpkg.KeyStep{{Kind: taskpkg.KindError, Title: shortError(runErr.Error()), At: time.Now()}}
	return m.finishRunFinal(taskID, runID, taskpkg.StatusFailed, steps, runErr.Error())
}

func (m *manager) cancelTask(taskID, runID string, steps []taskpkg.KeyStep, err error) error {
	if _, terr := m.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: taskpkg.StatusCanceled, Outcome: shortError(err.Error())}); terr != nil {
		return terr
	}
	if _, ferr := m.app.Tasks().FinishRun(runID, taskpkg.FinishRunInput{Status: taskpkg.StatusCanceled, KeySteps: steps}); ferr != nil {
		return ferr
	}
	return nil
}

func (m *manager) finishRunFinal(taskID, runID string, status taskpkg.Status, steps []taskpkg.KeyStep, outcome string) error {
	if _, err := m.app.Tasks().Update(taskID, taskpkg.UpdateTaskInput{Status: status, Outcome: shortOutcome(outcome)}); err != nil {
		return err
	}
	if _, err := m.app.Tasks().FinishRun(runID, taskpkg.FinishRunInput{Status: status, KeySteps: steps}); err != nil {
		return err
	}
	return nil
}

func shortTitle(explicit, input string) string {
	t := strings.TrimSpace(explicit)
	if t == "" {
		t = strings.TrimSpace(input)
	}
	t = collapseWhitespace(t)
	if rl := []rune(t); len(rl) > taskpkg.MaxTitleLen {
		return string(rl[:taskpkg.MaxTitleLen])
	}
	return t
}

func shortOutcome(s string) string {
	s = collapseWhitespace(s)
	if rl := []rune(s); len(rl) > taskpkg.MaxOutcomeLen {
		return string(rl[:taskpkg.MaxOutcomeLen])
	}
	return s
}

func shortError(s string) string {
	s = collapseWhitespace(s)
	if rl := []rune(s); len(rl) > taskpkg.MaxTitleLen-len("Failed: ") {
		s = string(rl[:taskpkg.MaxTitleLen-len("Failed: ")])
	}
	return "Failed: " + s
}

func outcomeFromContent(content string) string {
	c := collapseWhitespace(content)
	if rl := []rune(c); len(rl) > taskpkg.MaxOutcomeLen {
		return string(rl[:taskpkg.MaxOutcomeLen])
	}
	return c
}

func collapseWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func assembleAddedAssistantOutput(added []msg.Message) (string, string) {
	var contentParts, reasoningParts []string
	for _, m := range added {
		if m.Role != "assistant" {
			continue
		}
		if c := strings.TrimSpace(m.Content); c != "" {
			contentParts = append(contentParts, c)
		}
		if r := strings.TrimSpace(m.Reasoning); r != "" {
			reasoningParts = append(reasoningParts, r)
		}
	}
	return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
}

func summarizeAddedSteps(added []msg.Message, runErr error) []taskpkg.KeyStep {
	steps := make([]taskpkg.KeyStep, 0)
	now := time.Now()
	tools := 0
	for _, m := range added {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			tools++
			step, ok := stepFromToolCall(tc, now)
			if !ok {
				continue
			}
			steps = append(steps, step)
			if len(steps) >= taskpkg.MaxKeySteps-1 {
				break
			}
		}
		if len(steps) >= taskpkg.MaxKeySteps-1 {
			break
		}
	}
	if runErr != nil {
		steps = append(steps, taskpkg.KeyStep{Kind: taskpkg.KindError, Title: shortError(runErr.Error()), At: time.Now(), OK: false})
		return capSteps(steps)
	}
	if len(steps) == 0 && tools == 0 {
		steps = append(steps, taskpkg.KeyStep{Kind: taskpkg.KindSummary, Title: "Summary: completed without tool use", At: time.Now(), OK: true})
	}
	return capSteps(steps)
}

func capSteps(steps []taskpkg.KeyStep) []taskpkg.KeyStep {
	if len(steps) > taskpkg.MaxKeySteps {
		return steps[:taskpkg.MaxKeySteps]
	}
	return steps
}

func stepFromToolCall(tc msg.ToolCall, at time.Time) (taskpkg.KeyStep, bool) {
	name := strings.TrimSpace(tc.Name)
	switch name {
	case "read":
		return taskpkg.KeyStep{Kind: taskpkg.KindRead, Title: "Read " + shortLabel(extractField(tc.Args, "path"), 40), At: at, OK: true}, true
	case "write":
		return taskpkg.KeyStep{Kind: taskpkg.KindWrite, Title: "Wrote " + shortLabel(extractField(tc.Args, "path"), 40), At: at, OK: true}, true
	case "bash":
		return taskpkg.KeyStep{Kind: taskpkg.KindRun, Title: "Ran bash " + shortLabel(extractField(tc.Args, "cmd"), 40), At: at, OK: true}, true
	case "delegate", "spawn", "mention", "spawn_specialist":
		return taskpkg.KeyStep{Kind: taskpkg.KindSubtask, Title: "Delegated to " + shortLabel(extractField(tc.Args, "target"), 40), At: at, OK: true}, true
	default:
		return taskpkg.KeyStep{Kind: taskpkg.KindRun, Title: "Ran " + shortLabel(name, 40), At: at, OK: true}, true
	}
}

func shortLabel(raw string, n int) string {
	s := collapseWhitespace(raw)
	rl := []rune(s)
	if len(rl) > n {
		return string(rl[:n])
	}
	if s == "" {
		return "(unknown)"
	}
	return s
}

func extractField(args []byte, key string) string {
	if len(args) == 0 || key == "" {
		return ""
	}
	needle := fmt.Sprintf("%q:", key)
	idx := strings.Index(string(args), needle)
	if idx < 0 {
		return ""
	}
	rest := string(args[idx+len(needle):])
	rest = strings.TrimLeft(rest, " ")
	if !strings.HasPrefix(rest, "\"") {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
