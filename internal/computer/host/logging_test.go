package host

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abcdlsj/sumi/internal/observability"
)

func TestPeriodicLoopLogsFailureAndRecoveryWithClassification(t *testing.T) {
	var output bytes.Buffer
	daemon := NewDaemon(DaemonConfig{
		Logger:      observability.New(observability.ComponentComputer, &output),
		BackoffMax:  time.Millisecond,
		RetryJitter: func(time.Duration) time.Duration { return 0 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Uint32
	third := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		daemon.periodicLoop(ctx, time.Millisecond, daemon.transportLogger, "computer.heartbeat", func(context.Context) error {
			switch calls.Add(1) {
			case 1:
				return errors.New("offline")
			case 3:
				close(third)
			}
			return nil
		})
	}()
	select {
	case <-third:
	case <-time.After(time.Second):
		t.Fatal("periodic loop did not recover")
	}
	cancel()
	<-done
	logged := output.String()
	for _, want := range []string{
		"component=computer", "category=transport", "event=computer.heartbeat.failed",
		"event=computer.heartbeat.recovered", "attempt=1", "failed_attempts=1", "err=offline",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log output %q does not contain %q", logged, want)
		}
	}
}
