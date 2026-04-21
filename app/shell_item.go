package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/textutil"
)

const (
	itemUser = iota
	itemAssistant
	itemTool
	itemNotice
	itemError
)

type chatItem struct {
	Kind    int
	Content string
	Status  string
	Detail  string
	Time    time.Time
}

func (i chatItem) role() string {
	switch i.Kind {
	case itemUser:
		return "you"
	case itemAssistant:
		return "mink"
	case itemTool:
		return "tool"
	case itemNotice:
		return "note"
	case itemError:
		return "error"
	default:
		return "item"
	}
}

func (i chatItem) title() string {
	title := i.role()
	if i.Status != "" {
		title += " · " + i.Status
	}
	if !i.Time.IsZero() {
		title += " · " + i.Time.Format("15:04:05")
	}
	return title
}

func (i chatItem) detailText() string {
	var b strings.Builder
	b.WriteString(i.title())
	if text := strings.TrimSpace(textutil.Valid(i.Content)); text != "" {
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	if detail := strings.TrimSpace(textutil.Valid(i.Detail)); detail != "" {
		b.WriteString("\n\n")
		b.WriteString(detail)
	}
	return strings.TrimSpace(b.String())
}

func summarizeToolAction(name, raw string) string {
	name = strings.TrimSpace(name)
	args := parseToolArgs(raw)
	switch name {
	case "bash":
		if cmd := args["cmd"]; cmd != "" {
			return "Run bash " + textutil.Preview(cmd, 72)
		}
	case "read":
		if path := args["path"]; path != "" {
			return "Read file " + textutil.Preview(path, 72)
		}
	case "write":
		if path := args["path"]; path != "" {
			return "Write file " + textutil.Preview(path, 72)
		}
	case "edit":
		if path := args["path"]; path != "" {
			return "Edit file " + textutil.Preview(path, 72)
		}
	}
	if name == "" {
		return textutil.Preview(raw, 72)
	}
	if strings.TrimSpace(raw) == "" {
		return name
	}
	return name + " " + textutil.Preview(raw, 72)
}

func summarizeToolOutput(out string) string {
	return textutil.Preview(out, 88)
}

func renderToolDetail(ev bus.Event, failed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tool: %s\n", strings.TrimSpace(ev.Tool))
	if input := strings.TrimSpace(textutil.Valid(ev.Input)); input != "" {
		fmt.Fprintf(&b, "\nInput:\n%s\n", input)
	}
	if output := strings.TrimSpace(textutil.Valid(ev.Output)); output != "" {
		fmt.Fprintf(&b, "\nOutput:\n%s\n", output)
	}
	if failed && strings.TrimSpace(ev.Err) != "" {
		fmt.Fprintf(&b, "\nError:\n%s\n", textutil.Valid(ev.Err))
	}
	return strings.TrimSpace(b.String())
}

func parseToolArgs(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, ok := v.(string)
		if ok {
			out[k] = textutil.Valid(s)
		}
	}
	return out
}
