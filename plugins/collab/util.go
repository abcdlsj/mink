package collab

import (
	"fmt"
	"strings"
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
