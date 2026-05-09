package collab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
)

func capabilityHint(v []string) string {
	return strings.Join(v, " ")
}

func parseError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: parse error: %w", name, err)
}

func taskType(ctx context.Context, err error) string {
	if err == nil {
		return bus.DelegateFinished
	}
	if ctx != nil && ctx.Err() != nil {
		return bus.DelegateCanceled
	}
	return bus.DelegateFailed
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newID() string {
	return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
}