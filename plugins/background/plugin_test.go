package background

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/config"
)

func TestBackgroundUsesNoticeSource(t *testing.T) {
	dir := t.TempDir()
	a, err := app.New(config.Config{
		DataDir:   filepath.Join(dir, "data"),
		Workspace: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	events, cancel := a.Bus().Subscribe(16)
	defer cancel()

	r := &runner{app: a, workspace: dir, timeout: time.Second}
	ctx := command.WithSource(context.Background(), "cron:bazaar")
	ctx = command.WithNoticeSource(ctx, "tg:dm:42")
	args, _ := json.Marshal(map[string]string{"cmd": "printf hello"})
	if _, err := r.Run(ctx, args); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != bus.ServiceNotice {
				continue
			}
			if ev.Source != "tg:dm:42" {
				t.Fatalf("notice source = %q", ev.Source)
			}
			if !strings.Contains(ev.Text, "hello") {
				t.Fatalf("notice text = %q", ev.Text)
			}
			return
		case <-deadline:
			t.Fatal("missing service notice")
		}
	}
}
