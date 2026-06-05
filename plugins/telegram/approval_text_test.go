package telegram

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/tool"
)

func TestApprovalTextUsesProposal(t *testing.T) {
	text := approvalText(tool.Request{
		Action: "bash git push origin main",
		Proposal: tool.ActionProposal{
			Intent:   "Run shell command",
			Target:   "git",
			Risk:     "shell",
			Preview:  "bash git push origin main",
			Rollback: "Manual recovery may be required",
		},
	})
	for _, want := range []string{
		"intent: Run shell command",
		"target: git",
		"risk: shell",
		"preview: bash git push origin main",
		"rollback: Manual recovery may be required",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval text missing %q:\n%s", want, text)
		}
	}
}
