package collab

import (
	"errors"
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

func taskType(err error) string {
	if err != nil {
		return bus.DelegateFailed
	}
	return bus.DelegateFinished
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

func wrapErr(text string, err error) error {
	if err == nil && strings.TrimSpace(text) != "" {
		return errors.New(text)
	}
	return err
}
