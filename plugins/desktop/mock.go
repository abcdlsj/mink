package desktop

import (
	"strings"
	"time"
)

type ChannelItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Topic       string    `json:"topic,omitempty"`
	Agents      []string  `json:"agents"`
	UpdatedAt   time.Time `json:"updated_at"`
	UnreadCount int       `json:"unread_count"`
	HasRunning  bool      `json:"has_running"`
}

type ThreadItem struct {
	ID         string    `json:"id"`
	ChannelID  string    `json:"channel_id"`
	Title      string    `json:"title"`
	UpdatedAt  time.Time `json:"updated_at"`
	EventCount int       `json:"event_count"`
	HasRunning bool      `json:"has_running"`
}

// DirectChatItem represents an entry in the left rail's
// "Direct Chats" group. Each direct chat is its own KindDirectChat
// Space; the id is the Space id, the title is the Space title (the
// router will polish the title from the first user message in P3
// follow-up).
type DirectChatItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Agents      []string  `json:"agents"`
	UpdatedAt   time.Time `json:"updated_at"`
	UnreadCount int       `json:"unread_count"`
	HasRunning  bool      `json:"has_running"`
}

// RecentItem is one row in the left rail's "Recent" aggregator.
// Recent is NOT a Space — it's a derived view across every kind.
// Each item carries its kind so the frontend can dispatch the
// click correctly: kind="channel" navigates via openChannel,
// "direct_chat" via the direct-chat detail, "agent_dm" via
// openAgent.
type RecentItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // "channel" | "direct_chat" | "agent_dm"
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"` // optional last-message preview
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentItem struct {
	ID      string `json:"id"`
	Display string `json:"display"`
	Role    string `json:"role,omitempty"`
	Status  string `json:"status"`
}

type ParticipantsView struct {
	Agents       []AgentItem `json:"agents"`
	RunningAgent string      `json:"running_agent,omitempty"`
	ActiveRuns   []AgentRun  `json:"active_runs,omitempty"`
	RecentRuns   []AgentRun  `json:"recent_runs,omitempty"`
}

type AgentRun struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	ThreadID   string    `json:"thread_id,omitempty"`
	Time       time.Time `json:"time"`
	DurationMs int64     `json:"duration_ms,omitempty"`
}

func mockState() WorkspaceState {
	return WorkspaceState{
		Workspace: "/Users/lisongjian/Workspace/gh/abcdlsj/sumi",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4",
		Runtime:   "local",
		Ready:     true,
		DataDir:   "~/.sumi",
	}
}

func mockChannels() []ChannelItem {
	now := time.Now()
	return []ChannelItem{
		{ID: "ch-research", Name: "research", Topic: "Investigations and prior art", Agents: []string{"researcher", "writer"}, UpdatedAt: now.Add(-3 * time.Minute), UnreadCount: 2},
		{ID: "ch-coding", Name: "coding", Topic: "Implementation work", Agents: []string{"coder", "reviewer"}, UpdatedAt: now.Add(-25 * time.Minute), UnreadCount: 0},
		{ID: "ch-general", Name: "general", Topic: "Default workspace channel", Agents: []string{"coder", "writer"}, UpdatedAt: now.Add(-2 * time.Hour), UnreadCount: 0},
	}
}

func mockThreads() []ThreadItem {
	now := time.Now()
	return []ThreadItem{
		{ID: "th-fallback", ChannelID: "ch-coding", Title: "Provider fallback path", UpdatedAt: now.Add(-2 * time.Minute), EventCount: 12},
		{ID: "th-design", ChannelID: "ch-research", Title: "Design review for runlog", UpdatedAt: now.Add(-1 * time.Hour), EventCount: 6},
		{ID: "th-tools", ChannelID: "ch-coding", Title: "Tool registry refactor", UpdatedAt: now.Add(-3 * time.Hour), EventCount: 4},
	}
}

func mockAgents() []AgentItem {
	return []AgentItem{
		{ID: "coder", Display: "Coder", Role: "Implementation", Status: "idle"},
		{ID: "reviewer", Display: "Reviewer", Role: "Code review", Status: "idle"},
		{ID: "researcher", Display: "Researcher", Role: "Investigation", Status: "idle"},
		{ID: "writer", Display: "Writer", Role: "Drafting", Status: "idle"},
	}
}

func mockChannelDetail(id string) SessionDetail {
	now := time.Now()
	channel := mockChannels()[0]
	for _, c := range mockChannels() {
		if c.ID == id {
			channel = c
			break
		}
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           channel.ID,
			Title:        "#" + channel.Name,
			Runtime:      "local",
			Model:        "claude-sonnet-4",
			UpdatedAt:    channel.UpdatedAt,
			MessageCount: 14,
			EventCount:   18,
			Running:      channel.HasRunning,
		},
		Summary: channel.Topic,
		Messages: []MessageView{
			{ID: "c1", Role: "user", Content: "Anyone found a clean way to do provider fallback?", Time: now.Add(-15 * time.Minute)},
			{ID: "c2", Role: "agent", AuthorID: "researcher", AuthorName: "Researcher", Content: "Looked at Anthropic and OpenAI SDK retry patterns. They both use header-based fallback hints.", Time: now.Add(-13 * time.Minute)},
			{ID: "c3", Role: "agent", AuthorID: "coder", AuthorName: "Coder", Content: "I'll prototype it. Going into a thread to keep this channel clean.",
				Time:           now.Add(-5 * time.Minute),
				ThreadID:       "th-fallback",
				ThreadSummary:  "Provider fallback path · 12 events · running"},
			{ID: "c4", Role: "user", Content: "@reviewer can you scan recent commits while they work?", Time: now.Add(-3 * time.Minute)},
			{ID: "c5", Role: "agent", AuthorID: "reviewer", AuthorName: "Reviewer", Content: "On it.", Time: now.Add(-2 * time.Minute)},
		},
	}
}

func mockThreadDetail(id string) SessionDetail {
	now := time.Now()
	return SessionDetail{
		Item: SessionItem{
			ID:           id,
			Title:        "Provider fallback path",
			Runtime:      "local",
			Model:        "claude-sonnet-4",
			UpdatedAt:    now.Add(-2 * time.Minute),
			MessageCount: 4,
			EventCount:   12,
			Running:      false,
		},
		Summary: "Thread in #coding · started by Coder",
		Messages: []MessageView{
			{ID: "t1", Role: "agent", AuthorID: "coder", AuthorName: "Coder", Content: "Reading the workspace to map the fallback surface.", Time: now.Add(-4 * time.Minute),
				Events: []EventBlock{
					{Kind: "tool_call", ToolName: "list_files", Args: `{"path":"llm"}`, Output: "anthropic.go, openai.go, openrouter.go, retry_transport.go", Status: "done", DurationMs: 240, Time: now.Add(-4 * time.Minute)},
					{Kind: "service_notice", Output: "Model changed to claude-sonnet-4", Status: "done", Time: now.Add(-3 * time.Minute)},
				},
			},
			{ID: "t2", Role: "user", Content: "Why did the tests fail earlier?", Time: now.Add(-90 * time.Second)},
			{ID: "t3", Role: "agent", AuthorID: "coder", AuthorName: "Coder", Reasoning: "Need to inspect the retry path and the test expectations to find the divergence.", Content: "Investigating now.", Time: now.Add(-60 * time.Second),
				Events: []EventBlock{
					{Kind: "tool_call", ToolName: "shell", Args: `{"cmd":"go test ./llm/..."}`, Output: "FAIL\tgithub.com/abcdlsj/sumi/llm\t0.541s", Err: "exit status 1", Status: "error", DurationMs: 81, Time: now.Add(-50 * time.Second)},
				},
			},
			{ID: "t4", Role: "agent", AuthorID: "reviewer", AuthorName: "Reviewer", Content: "Delegated to me: check the recent commits touching llm/.", Time: now.Add(-30 * time.Second),
				Events: []EventBlock{
					{Kind: "tool_call", ToolName: "list_files", Args: `{"path":"llm"}`, Output: "anthropic.go, openai.go, openrouter.go, retry_transport.go", Status: "done", DurationMs: 320, Time: now.Add(-25 * time.Second)},
				},
			},
		},
	}
}

func mockParticipants(channelID, threadID string) ParticipantsView {
	now := time.Now()
	if threadID != "" {
		return ParticipantsView{
			Agents: []AgentItem{
				{ID: "coder", Display: "Coder", Role: "Implementation", Status: "idle"},
				{ID: "reviewer", Display: "Reviewer", Role: "Code review", Status: "idle"},
			},
			RecentRuns: []AgentRun{
				{ID: "r0", AgentID: "coder", Title: "list_files llm/", Status: "done", ThreadID: threadID, Time: now.Add(-4 * time.Minute)},
			},
		}
	}
	return ParticipantsView{
		Agents: []AgentItem{
			{ID: "researcher", Display: "Researcher", Role: "Investigation", Status: "idle"},
			{ID: "writer", Display: "Writer", Role: "Drafting", Status: "idle"},
		},
	}
}

func mockPersonas() []PersonaItem {
	return []PersonaItem{
		{ID: "coder", Display: "Coder", Runtime: "local", Description: "Implementation-focused agent.", Tools: []string{"shell", "search", "memory"}},
		{ID: "reviewer", Display: "Reviewer", Runtime: "local", Description: "Reviews code and prior changes.", Tools: []string{"search"}},
		{ID: "researcher", Display: "Researcher", Runtime: "local", Description: "Looks up references and prior art.", Tools: []string{"search", "memory"}},
		{ID: "writer", Display: "Writer", Runtime: "local", Description: "Drafts and edits prose.", Tools: []string{"search"}},
	}
}

func mockModels() []ModelItem {
	return []ModelItem{
		{Name: "main", Provider: "anthropic", Model: "claude-sonnet-4", MaxTokens: 8192, ContextWindow: 200000, Ready: true},
		{Name: "fast", Provider: "openai", Model: "gpt-4.1-mini", MaxTokens: 8192, ContextWindow: 128000, Ready: true},
	}
}

func mockTools() []ToolItem {
	return []ToolItem{
		{Name: "shell", Description: "Run a shell command in the workspace.", Enabled: true},
		{Name: "search", Description: "Search files and content.", Enabled: true},
		{Name: "memory", Description: "Read and write workspace memory.", Enabled: true},
	}
}

func mockCommands() []CommandItem {
	return []CommandItem{
		{Name: "/model", Summary: "Switch active model", Usage: "/model <name>"},
		{Name: "/session", Summary: "Manage current session", Usage: "/session [list|new|switch <id>]"},
		{Name: "/compact", Summary: "Compact conversation context"},
		{Name: "/replay", Summary: "Replay session events"},
		{Name: "/tokens", Summary: "Show token usage"},
	}
}

var mockReplyChunks = []string{
	"Looking into ", "your request",
	" — let me check the workspace",
	" first.",
}

var mockReasoning = "Looking at the workspace to understand the structure before answering."

func runMockStream(f *fanout, req SendRequest) {
	sid := req.SessionID
	if sid == "" {
		sid = "mock"
	}
	emit := func(ev BusEvent) {
		ev.SessionID = sid
		if ev.Time.IsZero() {
			ev.Time = time.Now()
		}
		f.publish(ev)
	}

	low := strings.ToLower(req.Input)

	switch {
	case strings.Contains(low, "fail delegate"):
		runMockFailDelegate(emit)
		return
	case strings.Contains(low, "delegate"):
		runMockDelegate(emit)
		return
	case strings.Contains(low, "mention"):
		runMockMention(emit)
		return
	case strings.Contains(low, "parallel"):
		runMockParallel(emit)
		return
	case strings.Contains(low, "error") || strings.Contains(low, "fail"):
		runMockToolFail(emit)
		return
	}

	emit(BusEvent{Type: "turn.started"})
	for _, r := range strings.Split(mockReasoning, " ") {
		time.Sleep(60 * time.Millisecond)
		emit(BusEvent{Type: "turn.reasoning", Text: r + " "})
	}

	time.Sleep(200 * time.Millisecond)
	emit(BusEvent{
		Type: "tool.call.started",
		ToolCallID: "tc-1", Tool: "list_files",
		Input: `{"path":"."}`,
	})
	time.Sleep(300 * time.Millisecond)
	emit(BusEvent{
		Type: "tool.call.finished",
		ToolCallID: "tc-1", Tool: "list_files",
		Output: "agent/, app/, bus/, cli/, cmd/, plugins/",
	})

	time.Sleep(180 * time.Millisecond)
	for _, c := range mockReplyChunks {
		emit(BusEvent{Type: "turn.chunk", Text: c})
		time.Sleep(110 * time.Millisecond)
	}
	emit(BusEvent{Type: "turn.finished"})
}

func runMockToolFail(emit func(BusEvent)) {
	emit(BusEvent{Type: "turn.started"})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "tool.call.started", ToolCallID: "tc-1", Tool: "list_files", Input: `{"path":"."}`})
	time.Sleep(380 * time.Millisecond)
	emit(BusEvent{Type: "tool.call.failed", ToolCallID: "tc-1", Tool: "list_files", Output: "stat .: permission denied", Err: "exit status 1"})
	time.Sleep(160 * time.Millisecond)
	emit(BusEvent{Type: "service.notice", Text: "Halted on tool error"})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.error", Err: "list_files failed: permission denied"})
}

func runMockMention(emit func(BusEvent)) {
	emit(BusEvent{Type: "turn.started"})
	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "Let me ask "})
	time.Sleep(80 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "Coder to take a look."})
	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.mention", ToolCallID: "mn-1",
		Tool: "coder", Input: "@Coder",
	})
	time.Sleep(700 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.mention.reply", ToolCallID: "mn-1",
		Tool: "coder", Output: "Looked at the file. The fallback only triggers on 5xx, missing 429. I'd add a retry on 429 with backoff.",
	})
	time.Sleep(180 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "\n\nThanks Coder. Will follow up."})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.finished"})
}

func runMockDelegate(emit func(BusEvent)) {
	emit(BusEvent{Type: "turn.started"})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "I'll hand this off to Reviewer for a deeper look."})
	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.delegate.started", ToolCallID: "dg-1",
		Tool: "reviewer", Input: "Review the recent commits in llm/ for retry behavior",
	})
	time.Sleep(900 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.delegate.progress", ToolCallID: "dg-1",
		Tool: "reviewer", Text: "running",
	})
	time.Sleep(900 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.delegate.finished", ToolCallID: "dg-1",
		Tool: "reviewer",
		Output: "Reviewed 3 commits. Suggest splitting retry policy into its own type and unit-testing the 429 path.",
	})
	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "\n\nReviewer is back with notes."})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.finished"})
}

func runMockFailDelegate(emit func(BusEvent)) {
	emit(BusEvent{Type: "turn.started"})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "Trying to delegate to Reviewer..."})
	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.delegate.started", ToolCallID: "dg-2",
		Tool: "reviewer", Input: "Review llm/ retry policy",
	})
	time.Sleep(700 * time.Millisecond)
	emit(BusEvent{
		Type: "agent.delegate.failed", ToolCallID: "dg-2",
		Tool: "reviewer",
		Err: "reviewer is offline (last heartbeat 4m ago)",
	})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "\n\nCouldn't reach Reviewer. I'll continue here."})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.finished"})
}

func runMockParallel(emit func(BusEvent)) {
	emit(BusEvent{Type: "turn.started"})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "Let me coordinate."})
	time.Sleep(150 * time.Millisecond)

	emit(BusEvent{Type: "tool.call.started", ToolCallID: "tc-p1", Tool: "list_files", Input: `{"path":"llm"}`})
	emit(BusEvent{Type: "agent.mention", ToolCallID: "mn-p1", Tool: "coder", Input: "@Coder"})
	emit(BusEvent{Type: "agent.delegate.started", ToolCallID: "dg-p1", Tool: "reviewer", Input: "Audit retry policy"})

	time.Sleep(600 * time.Millisecond)
	emit(BusEvent{Type: "tool.call.finished", ToolCallID: "tc-p1", Tool: "list_files", Output: "anthropic.go, openai.go, retry_transport.go"})
	time.Sleep(300 * time.Millisecond)
	emit(BusEvent{Type: "agent.mention.reply", ToolCallID: "mn-p1", Tool: "coder", Output: "Looks scoped. Suggest a dedicated retry struct."})
	time.Sleep(400 * time.Millisecond)
	emit(BusEvent{Type: "agent.delegate.finished", ToolCallID: "dg-p1", Tool: "reviewer", Output: "Done. Recommend extracting retry policy."})

	time.Sleep(150 * time.Millisecond)
	emit(BusEvent{Type: "turn.chunk", Text: "\n\nAll three agree on extracting the retry policy."})
	time.Sleep(120 * time.Millisecond)
	emit(BusEvent{Type: "turn.finished"})
}
