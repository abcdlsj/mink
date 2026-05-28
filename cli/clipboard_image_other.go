//go:build !darwin

package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func pasteClipboardImage() tea.Cmd {
	return func() tea.Msg {
		return shellClipboardImageMsg{Err: fmt.Errorf("clipboard image paste is only supported on macOS")}
	}
}
