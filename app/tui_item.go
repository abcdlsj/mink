package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/textutil"
)

const (
	tuiUser = iota
	tuiAssistant
	tuiTool
	tuiNotice
	tuiError
)

type tuiItem struct {
	Kind    int
	Content string
	Status  string
	Detail  string
	Time    time.Time
}

func (i tuiItem) listText() (string, string) {
	label := i.label()
	main := label
	if text := textutil.Preview(i.Content, 96); text != "" {
		main += " " + text
	}
	secondary := i.Time.Format("15:04:05")
	if i.Status != "" {
		secondary += " | " + i.Status
	}
	return main, secondary
}

func (i tuiItem) inspectorText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i.label())
	fmt.Fprintf(&b, "time: %s\n", i.Time.Format(time.RFC3339))
	if i.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", i.Status)
	}
	if text := strings.TrimSpace(textutil.Valid(i.Content)); text != "" {
		fmt.Fprintf(&b, "\ncontent:\n%s\n", text)
	}
	if detail := strings.TrimSpace(textutil.Valid(i.Detail)); detail != "" {
		fmt.Fprintf(&b, "\n%s\n", detail)
	}
	return strings.TrimSpace(b.String())
}

func (i tuiItem) label() string {
	switch i.Kind {
	case tuiUser:
		return "you"
	case tuiAssistant:
		return "mink"
	case tuiTool:
		return "tool"
	case tuiNotice:
		return "note"
	case tuiError:
		return "error"
	default:
		return "item"
	}
}
