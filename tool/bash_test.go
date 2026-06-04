package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBashReceivesChildEnv(t *testing.T) {
	b := &Bash{
		workspace: t.TempDir(),
		childEnv:  []string{"SUMI_TEST_CHILD_ENV=ok"},
	}
	args, _ := json.Marshal(map[string]string{"cmd": "printf \"$SUMI_TEST_CHILD_ENV\""})

	out, err := b.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("output = %q", out)
	}
}
