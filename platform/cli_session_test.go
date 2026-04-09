package platform

import (
	"strings"
	"testing"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
)

func TestHandleSessionNewReloadsTranscriptOnSwitch(t *testing.T) {
	m := &model{
		cli: &CLI{
			sessionFn: func() []msg.Message {
				return []msg.Message{
					{Role: "user", Content: "之前的问题"},
					{Role: "assistant", Content: "之前的回答"},
				}
			},
		},
		output:      []string{"stale"},
		agents:      map[string]*agentState{"agent:main": {id: "agent:main"}},
		tools:       map[string]*toolState{"tool-1": {id: "tool-1"}},
		pending:     1,
		lastSession: "old-session",
	}

	m.handleBusMsg(bus.Msg{Type: bus.TypeSessionNew, To: bus.AddrPlatformCLI, Payload: "new-session"})

	if m.pending != 0 {
		t.Fatalf("expected pending to reset, got %d", m.pending)
	}
	if got := strings.Join(stripANSISlice(m.output), "\n"); !strings.Contains(got, "之前的问题") || !strings.Contains(got, "之前的回答") {
		t.Fatalf("expected restored transcript in output, got %q", got)
	}
	if got := strings.Join(stripANSISlice(m.output), "\n"); !strings.Contains(got, "old-session → new-session") {
		t.Fatalf("expected session switch banner, got %q", got)
	}
}

func TestHandleSessionNewKeepsCurrentOutputForFreshEmptySession(t *testing.T) {
	m := &model{
		cli: &CLI{
			sessionFn: func() []msg.Message { return nil },
		},
		output: []string{"mink. type 'exit' to quit", "» hello"},
		agents: map[string]*agentState{},
		tools:  map[string]*toolState{},
	}

	m.handleBusMsg(bus.Msg{Type: bus.TypeSessionNew, To: bus.AddrPlatformCLI, Payload: "fresh-session"})

	if got := strings.Join(stripANSISlice(m.output), "\n"); !strings.Contains(got, "» hello") {
		t.Fatalf("expected existing output to remain for empty fresh session, got %q", got)
	}
	if m.lastSession != "fresh-session" {
		t.Fatalf("expected lastSession to update, got %q", m.lastSession)
	}
}

func stripANSISlice(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, stripANSI(line))
	}
	return out
}
