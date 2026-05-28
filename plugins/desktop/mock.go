package desktop

import "time"

func mockState() WorkspaceState {
	return WorkspaceState{
		Workspace: "/Users/lisongjian/Workspace/gh/abcdlsj/sumi",
		Provider:  "anthropic",
		Model:     "claude-sonnet-4",
		Runtime:   "native",
		Ready:     true,
		DataDir:   "~/.sumi",
	}
}

func mockSessions() []SessionItem {
	now := time.Now()
	return []SessionItem{
		{
			ID:           "20260528-desktop-a1b2",
			Title:        "Fix provider fallback",
			PersonaID:    "dev",
			PersonaName:  "Dev Agent",
			Runtime:      "native",
			Model:        "claude-sonnet-4",
			UpdatedAt:    now.Add(-2 * time.Minute),
			MessageCount: 8,
			EventCount:   12,
			Running:      true,
			Pinned:       true,
		},
		{
			ID:           "20260528-desktop-c3d4",
			Title:        "Refactor tool registry",
			PersonaID:    "",
			PersonaName:  "Default",
			Runtime:      "native",
			Model:        "claude-sonnet-4",
			UpdatedAt:    now.Add(-1 * time.Hour),
			MessageCount: 4,
			EventCount:   4,
		},
		{
			ID:           "20260527-desktop-e5f6",
			Title:        "Inspect runlog format",
			PersonaName:  "Default",
			Runtime:      "codex",
			Model:        "gpt-4.1-mini",
			UpdatedAt:    now.Add(-26 * time.Hour),
			MessageCount: 14,
			EventCount:   18,
		},
	}
}

func mockSessionDetail(id string) SessionDetail {
	now := time.Now()
	item := mockSessions()[0]
	return SessionDetail{
		Item: item,
		Messages: []MessageView{
			{
				ID:      "m1",
				Role:    "user",
				Content: "Check this project structure.",
				Time:    now.Add(-3 * time.Minute),
			},
			{
				ID:        "m2",
				Role:      "assistant",
				Content:   "Reading the workspace to understand layout, then I will summarize the key directories.",
				Reasoning: "The user wants a structural overview. Best approach: list top-level dirs first, then drill into app/agent for entrypoints.",
				Time:      now.Add(-150 * time.Second),
				Events: []EventBlock{
					{
						Kind:       "tool_call",
						ToolName:   "list_files",
						Args:       `{"path":"/Users/lisongjian/Workspace/gh/abcdlsj/sumi"}`,
						Output:     "app/, agent/, bus/, cli/, cmd/, command/, config/, llm/, msg/, persona/, plugins/, session/, skill/, store/, textutil/, tool/",
						Status:     "done",
						DurationMs: 340,
						Time:       now.Add(-140 * time.Second),
					},
					{
						Kind:   "service_notice",
						Output: "Model changed to claude-sonnet-4",
						Status: "done",
						Time:   now.Add(-135 * time.Second),
					},
					{
						Kind:       "tool_call",
						ToolName:   "shell",
						Args:       `{"cmd":"go test ./..."}`,
						Output:     "FAIL\tgithub.com/abcdlsj/sumi/agent\t0.812s\nFAIL\tgithub.com/abcdlsj/sumi/llm\t0.541s",
						Status:     "error",
						Err:        "exit status 1",
						DurationMs: 81,
						Time:       now.Add(-90 * time.Second),
					},
				},
			},
			{
				ID:      "m3",
				Role:    "user",
				Content: "Why did the tests fail?",
				Time:    now.Add(-60 * time.Second),
			},
			{
				ID:      "m4",
				Role:    "assistant",
				Content: "Investigating now.",
				Time:    now.Add(-30 * time.Second),
				Events: []EventBlock{
					{
						Kind:     "tool_call",
						ToolName: "list_files",
						Args:     `{"path":"agent"}`,
						Status:   "running",
						Time:     now.Add(-10 * time.Second),
					},
				},
			},
		},
	}
}

func mockPersonas() []PersonaItem {
	return []PersonaItem{
		{ID: "dev", Display: "Dev Agent", Runtime: "native", Description: "Code-focused engineering agent.", Tools: []string{"shell", "search", "memory"}},
		{ID: "writer", Display: "Writer", Runtime: "native", Description: "Drafts and edits prose.", Tools: []string{"search"}},
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
