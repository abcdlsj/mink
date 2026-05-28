package desktop

import "testing"

func TestBackendNilAppFallsBackToMock(t *testing.T) {
	b := newBackend(nil)
	if got := b.WorkspaceInfo().Workspace; got == "" {
		t.Errorf("WorkspaceInfo empty: %#v", b.WorkspaceInfo())
	}
	if got := b.ListChannels(); len(got) == 0 {
		t.Error("ListChannels empty in mock mode")
	}
	if got := b.ListThreads(); len(got) == 0 {
		t.Error("ListThreads empty in mock mode")
	}
	if got := b.ListAgents(); len(got) == 0 {
		t.Error("ListAgents empty in mock mode")
	}
	if got := b.ListPersonas(); len(got) == 0 {
		t.Error("ListPersonas empty in mock mode")
	}
	if got := b.ListModels(); len(got) == 0 {
		t.Error("ListModels empty in mock mode")
	}
	if got := b.ListTools(); len(got) == 0 {
		t.Error("ListTools empty in mock mode")
	}
	if got := b.ListCommands(); len(got) == 0 {
		t.Error("ListCommands empty in mock mode")
	}
}

func TestBackendStopWithoutSendIsSafe(t *testing.T) {
	b := newBackend(nil)
	if err := b.StopTurn("missing"); err != nil {
		t.Errorf("StopTurn returned: %v", err)
	}
}

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in       string
		provider string
		model    string
	}{
		{"anthropic / claude-sonnet-4", "anthropic", "claude-sonnet-4"},
		{"openai / gpt-4.1-mini", "openai", "gpt-4.1-mini"},
		{"(unconfigured)", "(unconfigured)", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		p, m := splitModel(c.in)
		if p != c.provider || m != c.model {
			t.Errorf("splitModel(%q) = %q,%q want %q,%q", c.in, p, m, c.provider, c.model)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := fallback("", "default"); got != "default" {
		t.Errorf("fallback empty: %q", got)
	}
	if got := fallback("  ", "default"); got != "default" {
		t.Errorf("fallback whitespace: %q", got)
	}
	if got := fallback("real", "default"); got != "real" {
		t.Errorf("fallback non-empty: %q", got)
	}
}
