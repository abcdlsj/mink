package mink

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/platform"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

func (a *App) currentSection(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if section := a.activeSections[src]; section != "" {
		return section
	}
	return "main"
}

func (a *App) setActiveSection(src, section string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(section) == "" {
		delete(a.activeSections, src)
		return
	}
	a.activeSections[src] = section
}

func (a *App) currentMainSession(src string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeMainSessions[src]
}

func (a *App) setMainSession(src, sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		delete(a.activeMainSessions, src)
		return
	}
	a.activeMainSessions[src] = sessionID
}

func (a *App) webState(ctx context.Context, src string) (platform.WebState, error) {
	state := platform.WebState{
		Workspace: filepath.Base(currentWorkspace()),
		Section:   a.currentSection(src),
		Nav: []platform.WebNavItem{
			{ID: "inbox", Label: "Inbox"},
			{ID: "main", Label: "Main"},
			{ID: "teams", Label: "Teams"},
			{ID: "threads", Label: "Threads"},
		},
	}
	for i := range state.Nav {
		state.Nav[i].Active = state.Nav[i].ID == state.Section
	}

	switch state.Section {
	case "teams":
		return a.webTeamsState(ctx, src, state)
	case "threads":
		return a.webThreadsState(ctx, src, state)
	case "inbox":
		return a.webInboxState(ctx, src, state)
	default:
		return a.webMainState(ctx, src, state)
	}
}

func (a *App) webSelect(ctx context.Context, src, section, id string) error {
	section = strings.TrimSpace(section)
	if section == "" {
		section = "main"
	}
	a.setActiveSection(src, section)

	switch section {
	case "main":
		if id != "" {
			return a.webOpenSession(ctx, src, id)
		}
		if current := a.currentMainSession(src); current != "" {
			return a.webOpenSession(ctx, src, current)
		}
		return a.webNewSession(ctx, src)
	case "teams":
		if id != "" {
			return a.webOpenTeam(ctx, src, id)
		}
		if current := a.currentTeamID(src); current != "" {
			return a.webOpenTeam(ctx, src, current)
		}
		if a.rt == nil {
			return nil
		}
		teams, err := a.rt.ListTeams(ctx, "")
		if err != nil || len(teams) == 0 {
			return err
		}
		return a.webOpenTeam(ctx, src, teams[0].ID)
	case "threads":
		if id != "" {
			return a.webOpenThread(ctx, src, id)
		}
		threads, err := a.webRecentThreads(ctx)
		if err != nil || len(threads) == 0 {
			return err
		}
		return a.webOpenThread(ctx, src, threads[0].Thread.ID)
	case "inbox":
		return nil
	default:
		return fmt.Errorf("unknown section %q", section)
	}
}

func (a *App) webSendMessage(ctx context.Context, src, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "!") {
		return fmt.Errorf("web composer does not support command messages yet")
	}
	if a.currentSection(src) == "inbox" {
		return fmt.Errorf("select a conversation before sending a message")
	}
	if sess, err := a.sm.Current(src); err == nil && sess != nil && strings.TrimSpace(sess.Status()) == "closed" {
		return fmt.Errorf("active conversation is closed; start a new session or open an active thread")
	}
	if a.rt != nil {
		if threadID := a.currentThreadID(src); threadID != "" {
			thread, err := a.rt.GetThread(ctx, threadID)
			if err != nil {
				return err
			}
			if thread.ID != "" && strings.TrimSpace(thread.Status) == "closed" {
				return fmt.Errorf("thread %s is closed", thread.Title)
			}
		}
	}
	return a.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: text,
	})
}

func (a *App) webAction(ctx context.Context, src, name string) error {
	switch strings.TrimSpace(name) {
	case "new_session":
		return a.webNewSession(ctx, src)
	case "close_thread":
		threadID := a.currentThreadID(src)
		if threadID == "" {
			return fmt.Errorf("no active thread")
		}
		_, err := a.closeThread(ctx, src, threadID)
		return err
	default:
		return fmt.Errorf("unknown action %q", name)
	}
}

func (a *App) webNewSession(ctx context.Context, src string) error {
	sessionID, err := a.prepareFreshSource(ctx, src)
	if err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.setMainSession(src, sessionID)
	a.setActiveSection(src, "main")
	return nil
}

func (a *App) webOpenSession(ctx context.Context, src, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if err := a.sm.RestoreSource(src, sessionID); err != nil {
		return err
	}
	_ = a.sm.Update(sessionID, func(s *session.Session) {
		s.SetKind("main")
		s.SetStatus("active")
	})
	a.disp.InvalidateSource(src)
	a.disp.UnbindTeamSource(src)
	a.setActiveTeam(src, "")
	a.setActiveThread(src, "")
	a.setMainSession(src, sessionID)
	a.setActiveSection(src, "main")
	return nil
}

func (a *App) webOpenTeam(ctx context.Context, src, teamID string) error {
	a.setActiveTeam(src, teamID)
	a.setActiveSection(src, "teams")
	a.setActiveThread(src, "")
	a.disp.UnbindTeamSource(src)
	if a.rt == nil {
		return nil
	}
	threads, err := a.rt.ListThreads(ctx, teamID, "")
	if err != nil || len(threads) == 0 {
		return err
	}
	for _, thread := range threads {
		if thread.Status == "active" {
			return a.webOpenThread(ctx, src, thread.ID)
		}
	}
	return a.webOpenThread(ctx, src, threads[0].ID)
}

func (a *App) webOpenThread(ctx context.Context, src, threadID string) error {
	if a.rt == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	thread, err := a.rt.GetThread(ctx, threadID)
	if err != nil {
		return err
	}
	if thread.ID == "" {
		return fmt.Errorf("thread not found")
	}
	if err := a.sm.RestoreSource(src, thread.SessionID); err != nil {
		return err
	}
	status := strings.TrimSpace(thread.Status)
	if status == "" {
		status = "active"
	}
	_ = a.sm.Update(thread.SessionID, func(s *session.Session) {
		s.SetKind("team_thread")
		s.SetStatus(status)
		if strings.TrimSpace(s.Summary()) == "" {
			s.SetSummary(thread.Title)
		}
	})
	a.disp.InvalidateSource(src)
	a.setActiveTeam(src, thread.TeamID)
	a.setActiveThread(src, thread.ID)
	if status == "active" {
		a.disp.BindTeamSource(src, thread.TeamID, thread.ID)
	} else {
		a.disp.UnbindTeamSource(src)
	}
	return nil
}

func (a *App) webMainState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	state.IndexTitle = "Main Sessions"
	state.IndexAction = "new_session"
	state.IndexActionLabel = "New Session"

	currentID := a.currentMainSession(src)
	if currentID == "" {
		if id, ok := a.sm.CurrentID(src); ok {
			currentID = id
			a.setMainSession(src, id)
		}
	}

	items, err := a.webMainSessionItems(ctx, currentID)
	if err != nil {
		return state, err
	}
	state.IndexGroups = []platform.WebIndexGroup{{Title: "Sessions", Items: items}}
	state.HeaderTitle = "Main Conversation"
	state.HeaderSubtitle = currentID
	state.HeaderMeta = a.webUsageMeta(src)
	state.Messages = a.webMessagesForSource(src)
	state.ContextTitle = "Context"
	state.ContextBlocks = a.webSessionContextBlocks(src)
	state.ComposerLabel = "Message Main Session"
	state.ComposerPlaceholder = "Continue the active main session..."
	if sess, err := a.sm.Current(src); err == nil && sess != nil && strings.TrimSpace(sess.Status()) == "closed" {
		state.ComposerDisabled = true
		state.ComposerPlaceholder = "Session is closed. Start a new session to continue..."
	}
	state.EmptyHint = "Start a new session or pick a resumable conversation."
	return state, nil
}

func (a *App) webInboxState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	mainItems, err := a.webMainSessionItems(ctx, a.currentMainSession(src))
	if err != nil {
		return state, err
	}
	threads, err := a.webRecentThreads(ctx)
	if err != nil {
		return state, err
	}

	state.IndexTitle = "Inbox"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Main", Items: mainItems},
		{Title: "Threads", Items: a.webThreadIndexItems(threads, a.currentThreadID(src), "threads")},
	}
	state.HeaderTitle = "Inbox"
	state.HeaderSubtitle = "Recent activity across main sessions and team threads"
	state.Cards = a.webInboxCards(ctx, mainItems, threads)
	state.ContextTitle = "How To Use"
	state.ContextBlocks = []platform.WebContextBlock{
		{Title: "Main", Body: "Open or create a resumable main conversation."},
		{Title: "Teams", Body: "Open a team and work directly inside its active thread."},
	}
	state.ComposerLabel = "Select A Conversation"
	state.ComposerPlaceholder = "Inbox is read-only."
	state.ComposerDisabled = true
	state.EmptyHint = "No recent activity yet."
	return state, nil
}

func (a *App) webTeamsState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	if a.rt == nil {
		state.HeaderTitle = "Teams"
		state.HeaderSubtitle = "Runtime database unavailable"
		state.ComposerDisabled = true
		return state, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return state, err
	}
	currentTeam := a.currentTeamID(src)
	currentThread := a.currentThreadID(src)

	teamItems := make([]platform.WebIndexItem, 0, len(teams))
	threadItems := []platform.WebIndexItem{}
	headerTitle := "Teams"
	headerSubtitle := "Select a team"
	contextBlocks := []platform.WebContextBlock{}
	cards := []platform.WebCard{}
	composerPlaceholder := "Select a thread to continue the team conversation..."
	composerDisabled := currentThread == ""

	for _, team := range teams {
		teamItems = append(teamItems, platform.WebIndexItem{
			ID:      team.ID,
			Section: "teams",
			Label:   team.Name,
			Meta:    strings.ToUpper(team.Status),
			Active:  team.ID == currentTeam,
		})
	}

	if currentTeam != "" {
		team, err := a.rt.GetTeam(ctx, currentTeam)
		if err == nil && team.ID != "" {
			headerTitle = team.Name
			headerSubtitle = "Team thread workspace"
			members, _ := a.rt.ListTeamMembers(ctx, team.ID)
			contextBlocks = append(contextBlocks, platform.WebContextBlock{
				Title: "Members",
				Body:  a.webMemberSummary(ctx, members),
			})
			threads, _ := a.rt.ListThreads(ctx, team.ID, "")
			threadItems = a.webThreadIndexItems(a.wrapThreads(team, threads), currentThread, "threads")
			if currentThread == "" {
				for _, thread := range threads {
					info := a.threadInfoFromRecord(ctx, thread)
					cards = append(cards, platform.WebCard{
						Title:    info.Title,
						Subtitle: info.BestAnswer,
						Meta:     info.LatestSummaryAt,
					})
				}
			} else {
				thread, _ := a.rt.GetThread(ctx, currentThread)
				if thread.ID != "" {
					info := a.threadInfoFromRecord(ctx, thread)
					headerTitle = fmt.Sprintf("%s — %s", team.Name, thread.Title)
					headerSubtitle = "Active team thread"
					if strings.TrimSpace(thread.Status) == "closed" {
						headerSubtitle = "Closed team thread"
						composerDisabled = true
						composerPlaceholder = "Thread is closed. Open an active thread to continue..."
					} else {
						composerDisabled = false
						state.IndexAction = "close_thread"
						state.IndexActionLabel = "Close Thread"
					}
					contextBlocks = append(contextBlocks,
						platform.WebContextBlock{Title: "Best Answer", Body: info.BestAnswer},
						platform.WebContextBlock{Title: "Open Blockers", Body: info.OpenBlockers},
						platform.WebContextBlock{Title: "Latest Summary", Body: info.LatestSummary},
					)
					state.Messages = a.webMessagesForSource(src)
				}
			}
		}
	}

	state.IndexTitle = "Teams"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Teams", Items: teamItems},
		{Title: "Threads", Items: threadItems},
	}
	state.HeaderTitle = headerTitle
	state.HeaderSubtitle = headerSubtitle
	state.HeaderMeta = a.webUsageMeta(src)
	state.Cards = cards
	state.ContextTitle = "Context"
	state.ContextBlocks = contextBlocks
	state.ComposerLabel = "Message Team Thread"
	state.ComposerPlaceholder = composerPlaceholder
	state.ComposerDisabled = composerDisabled
	state.EmptyHint = "Create or open a team thread to start working."
	return state, nil
}

func (a *App) webThreadsState(ctx context.Context, src string, state platform.WebState) (platform.WebState, error) {
	threads, err := a.webRecentThreads(ctx)
	if err != nil {
		return state, err
	}
	currentThread := a.currentThreadID(src)
	currentTeam := a.currentTeamID(src)

	state.IndexTitle = "Recent Threads"
	state.IndexGroups = []platform.WebIndexGroup{
		{Title: "Threads", Items: a.webThreadIndexItems(threads, currentThread, "threads")},
	}
	state.HeaderTitle = "Threads"
	state.HeaderSubtitle = "Recent team workstreams"
	state.ComposerLabel = "Message Thread"
	state.ComposerPlaceholder = "Select a thread to continue..."
	state.ComposerDisabled = currentThread == ""
	state.EmptyHint = "No threads yet."

	if currentThread == "" || a.rt == nil {
		return state, nil
	}

	thread, err := a.rt.GetThread(ctx, currentThread)
	if err != nil || thread.ID == "" {
		return state, err
	}
	team, _ := a.rt.GetTeam(ctx, currentTeam)
	info := a.threadInfoFromRecord(ctx, thread)
	state.HeaderTitle = fmt.Sprintf("%s — %s", team.Name, thread.Title)
	state.HeaderSubtitle = "Active thread"
	if strings.TrimSpace(thread.Status) == "closed" {
		state.HeaderSubtitle = "Closed thread"
		state.ComposerDisabled = true
		state.ComposerPlaceholder = "Thread is closed. Open an active thread to continue..."
	} else {
		state.IndexAction = "close_thread"
		state.IndexActionLabel = "Close Thread"
	}
	state.HeaderMeta = a.webUsageMeta(src)
	state.Messages = a.webMessagesForSource(src)
	state.ContextTitle = "Context"
	state.ContextBlocks = []platform.WebContextBlock{
		{Title: "Best Answer", Body: info.BestAnswer},
		{Title: "Open Blockers", Body: info.OpenBlockers},
		{Title: "Latest Summary", Body: info.LatestSummary},
	}
	return state, nil
}

func (a *App) webMessagesForSource(src string) []platform.WebMessage {
	sess, err := a.sm.Current(src)
	if err != nil || sess == nil {
		return nil
	}
	msgs := sess.View().Messages
	out := make([]platform.WebMessage, 0, len(msgs))
	for _, message := range msgs {
		webMsg := platform.WebMessage{
			Role:        message.Role,
			Sender:      a.webSenderName(message),
			Descriptor:  a.webDescriptor(message),
			Time:        webTime(message.Timestamp),
			Content:     strings.TrimSpace(message.Content),
			Reasoning:   strings.TrimSpace(message.Reasoning),
			ToolCalls:   a.webToolCalls(message.ToolCalls),
			ToolResults: a.webToolResults(message.ToolResults),
		}
		if webMsg.Content == "" {
			webMsg.Content = a.webFallbackContent(message)
		}
		if !a.webMessageVisible(webMsg) {
			continue
		}
		out = append(out, webMsg)
	}
	return out
}

func (a *App) webMessageVisible(message platform.WebMessage) bool {
	return message.Content != "" ||
		message.Reasoning != "" ||
		len(message.ToolCalls) > 0 ||
		len(message.ToolResults) > 0
}

func (a *App) webToolCalls(calls []msg.ToolCall) []platform.WebToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]platform.WebToolCall, 0, len(calls))
	for _, call := range calls {
		args := strings.TrimSpace(string(call.Args))
		if args == "" || args == "null" {
			args = ""
		}
		out = append(out, platform.WebToolCall{
			Name: call.Name,
			Args: args,
		})
	}
	return out
}

func (a *App) webToolResults(results []msg.ToolResult) []platform.WebToolResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]platform.WebToolResult, 0, len(results))
	for _, result := range results {
		out = append(out, platform.WebToolResult{
			Content: strings.TrimSpace(result.Content),
			Error:   strings.TrimSpace(result.Error),
		})
	}
	return out
}

func (a *App) webFallbackContent(message msg.Message) string {
	switch message.Role {
	case "assistant":
		if len(message.ToolCalls) == 0 {
			return ""
		}
		lines := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			args := strings.TrimSpace(string(call.Args))
			if args == "" || args == "null" {
				lines = append(lines, fmt.Sprintf("tool: %s", call.Name))
				continue
			}
			lines = append(lines, fmt.Sprintf("tool: %s %s", call.Name, compactLine(args, 200)))
		}
		return strings.Join(lines, "\n")
	case "tool":
		if len(message.ToolResults) == 0 {
			return ""
		}
		lines := make([]string, 0, len(message.ToolResults))
		for _, result := range message.ToolResults {
			switch {
			case strings.TrimSpace(result.Error) != "":
				lines = append(lines, "tool error: "+compactLine(result.Error, 200))
			case strings.TrimSpace(result.Content) != "":
				lines = append(lines, compactLine(result.Content, 240))
			default:
				lines = append(lines, "tool result: (no output)")
			}
		}
		return strings.Join(lines, "\n")
	default:
		return ""
	}
}

func (a *App) webSenderName(message msg.Message) string {
	switch message.Role {
	case "user":
		return "You"
	case "system":
		return "System"
	case "tool":
		return "Tool"
	}
	if message.AgentID == "" || message.AgentID == bus.AddrAgentMain {
		return "Mink"
	}
	if a.rt != nil {
		if ident, err := a.rt.GetAgentIdentity(context.Background(), message.AgentID); err == nil && ident.DisplayName != "" {
			return ident.DisplayName
		}
	}
	for _, cfg := range a.cfg.Agents {
		if cfg.ID == message.AgentID && cfg.Name != "" {
			return cfg.Name
		}
	}
	return message.AgentID
}

func (a *App) webDescriptor(message msg.Message) string {
	switch message.Role {
	case "assistant":
		if message.AgentID != "" && message.AgentID != bus.AddrAgentMain {
			return "Agent"
		}
		return "Assistant"
	case "user":
		return "Owner"
	case "system":
		return "System"
	case "tool":
		return "Tool"
	default:
		return ""
	}
}

func (a *App) webUsageMeta(src string) []string {
	var meta []string
	if id, ok := a.sm.CurrentID(src); ok && id != "" {
		meta = append(meta, "session "+compactLine(id, 28))
	}
	if u, ok := a.disp.Usage(src); ok {
		meta = append(meta, fmt.Sprintf("tokens in:%d out:%d", u.Input, u.Output))
	}
	return meta
}

func (a *App) webSessionContextBlocks(src string) []platform.WebContextBlock {
	sess, err := a.sm.Current(src)
	if err != nil || sess == nil {
		return nil
	}
	var blocks []platform.WebContextBlock
	if anchor := sess.LatestAnchor(); anchor != nil {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Context Anchor",
			Body:  anchor.Summary,
		})
	}
	if prov := sess.Provenance(); prov != nil && prov.ParentSessionID != "" {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Forked From",
			Body:  prov.ParentSessionID,
		})
	}
	if activity := a.webRunlogSummary(sess.ID(), 18); activity != "" {
		blocks = append(blocks, platform.WebContextBlock{
			Title: "Recent Activity",
			Body:  activity,
		})
	}
	return blocks
}

func (a *App) webRunlogSummary(sessionID string, limit int) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	if limit <= 0 {
		limit = 20
	}
	if a.rt != nil {
		if events, err := a.rt.ReplayEventsForSession(context.Background(), sessionID, limit); err == nil && len(events) > 0 {
			out := make([]string, 0, len(events))
			for _, ev := range events {
				if rendered := webReplayLine(ev); rendered != "" {
					out = append(out, rendered)
				}
			}
			return strings.Join(out, "\n")
		}
	}
	return ""
}

func webReplayLine(ev rtsqlite.ReplayEvent) string {
	ts := ""
	if !ev.Timestamp.IsZero() {
		ts = ev.Timestamp.Format("15:04:05")
	}
	step := ""
	if ev.StepNum != nil {
		step = fmt.Sprintf(" step=%d", *ev.StepNum)
	}
	extra := webReplayExtra(ev.Type, ev.Data)
	if extra != "" {
		extra = " " + extra
	}
	switch ev.Type {
	case "user_input", "agent_output", "tool_call", "tool_end", "llm_error", "interrupt", "step_start", "step_end":
		return strings.TrimSpace(fmt.Sprintf("%s %s%s%s", ts, ev.Type, step, extra))
	default:
		return ""
	}
}

func webReplayExtra(kind string, data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	switch kind {
	case "user_input":
		if v, ok := data["input"].(string); ok {
			return compactLine(v, 120)
		}
	case "agent_output":
		if v, ok := data["content"].(string); ok {
			return compactLine(v, 120)
		}
	case "tool_call":
		name, _ := data["name"].(string)
		if name != "" {
			return name
		}
	case "tool_end":
		name, _ := data["name"].(string)
		if err, ok := data["error"].(string); ok && err != "" {
			if name != "" {
				return name + " error=" + compactLine(err, 120)
			}
			return compactLine(err, 120)
		}
		if name != "" {
			return name
		}
	case "llm_error":
		if err, ok := data["error"].(string); ok {
			return compactLine(err, 120)
		}
	case "interrupt":
		if reason, ok := data["reason"].(string); ok {
			return compactLine(reason, 120)
		}
	}
	return ""
}

func (a *App) webMainSessionItems(ctx context.Context, currentID string) ([]platform.WebIndexItem, error) {
	ids, err := a.sm.List()
	if err != nil {
		return nil, err
	}
	excluded, _ := a.webTeamThreadSessionSet(ctx)
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	items := make([]platform.WebIndexItem, 0, len(ids))
	for _, id := range ids {
		if _, skip := excluded[id]; skip {
			continue
		}
		sess, err := a.sm.Get(id)
		if err != nil || sess == nil {
			continue
		}
		if kind := strings.TrimSpace(sess.Kind()); kind != "" && kind != "main" {
			continue
		}
		title := id
		summary := ""
		for _, message := range sess.View().Messages {
			if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
				title = compactLine(message.Content, 28)
				break
			}
		}
		if s := strings.TrimSpace(sess.Summary()); s != "" {
			summary = compactLine(s, 28)
		}
		meta := compactLine(id, 28)
		if status := strings.TrimSpace(sess.Status()); status != "" {
			meta = strings.ToUpper(status)
			if summary != "" {
				meta += " · " + summary
			}
		} else if summary != "" {
			meta = summary
		}
		items = append(items, platform.WebIndexItem{
			ID:      id,
			Section: "main",
			Label:   title,
			Meta:    meta,
			Active:  id == currentID,
		})
	}
	return items, nil
}

func (a *App) webRecentThreads(ctx context.Context) ([]webThreadItem, error) {
	if a.rt == nil {
		return nil, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []webThreadItem
	for _, team := range teams {
		threads, err := a.rt.ListThreads(ctx, team.ID, "")
		if err != nil {
			return nil, err
		}
		for _, thread := range threads {
			out = append(out, webThreadItem{Team: team, Thread: thread})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Thread.UpdatedAt > out[j].Thread.UpdatedAt
	})
	return out, nil
}

func (a *App) webThreadIndexItems(threads []webThreadItem, currentThread, section string) []platform.WebIndexItem {
	items := make([]platform.WebIndexItem, 0, len(threads))
	for _, item := range threads {
		meta := item.Team.Name
		if status := strings.TrimSpace(item.Thread.Status); status != "" {
			meta += " · " + strings.ToUpper(status)
		}
		items = append(items, platform.WebIndexItem{
			ID:      item.Thread.ID,
			Section: section,
			Label:   item.Thread.Title,
			Meta:    meta,
			Active:  item.Thread.ID == currentThread,
		})
	}
	return items
}

func (a *App) webInboxCards(ctx context.Context, sessions []platform.WebIndexItem, threads []webThreadItem) []platform.WebCard {
	var cards []platform.WebCard
	for i, item := range sessions {
		if i >= 3 {
			break
		}
		cards = append(cards, platform.WebCard{
			Title:    item.Label,
			Subtitle: "Main conversation",
			Meta:     item.Meta,
		})
	}
	for i, thread := range threads {
		if i >= 3 {
			break
		}
		info := a.threadInfoFromRecord(ctx, thread.Thread)
		cards = append(cards, platform.WebCard{
			Title:    fmt.Sprintf("%s / %s", thread.Team.Name, thread.Thread.Title),
			Subtitle: info.BestAnswer,
			Meta:     info.LatestSummaryAt,
		})
	}
	return cards
}

func (a *App) webMemberSummary(ctx context.Context, members []rtsqlite.TeamMember) string {
	if len(members) == 0 {
		return "No members"
	}
	lines := make([]string, 0, len(members))
	for _, member := range members {
		name := member.AgentID
		if a.rt != nil {
			if ident, err := a.rt.GetAgentIdentity(ctx, member.AgentID); err == nil && ident.DisplayName != "" {
				name = ident.DisplayName
			}
		}
		lines = append(lines, fmt.Sprintf("%s — %s", name, member.RoleName))
	}
	return strings.Join(lines, "\n")
}

func (a *App) webTeamThreadSessionSet(ctx context.Context) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	if a.rt == nil {
		return set, nil
	}
	teams, err := a.rt.ListTeams(ctx, "")
	if err != nil {
		return set, err
	}
	for _, team := range teams {
		threads, err := a.rt.ListThreads(ctx, team.ID, "")
		if err != nil {
			return set, err
		}
		for _, thread := range threads {
			if thread.SessionID != "" {
				set[thread.SessionID] = struct{}{}
			}
		}
	}
	return set, nil
}

func (a *App) wrapThreads(team rtsqlite.Team, threads []rtsqlite.TeamThread) []webThreadItem {
	out := make([]webThreadItem, 0, len(threads))
	for _, thread := range threads {
		out = append(out, webThreadItem{Team: team, Thread: thread})
	}
	return out
}

type webThreadItem struct {
	Team   rtsqlite.Team
	Thread rtsqlite.TeamThread
}

func webTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format("15:04")
}

func currentWorkspace() string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return "workspace"
	}
	return wd
}

func findWebDist() string {
	// Look for web/dist relative to the binary, then relative to cwd.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "..", "web", "dist")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		dir := filepath.Join(wd, "web", "dist")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}
