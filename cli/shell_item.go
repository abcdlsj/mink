package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/textutil"
)

const (
	itemUser = iota
	itemAssistant
	itemNotice
	itemError
)

const (
	segText = iota
	segTool
	segReasoning
)

type chatItem struct {
	Kind     int
	Status   string
	Time     time.Time
	Segments []chatSegment
}

type chatSegment struct {
	Kind   int
	Tool   string
	Text   string
	Status string
	Detail string
	Time   time.Time
}

func (i chatItem) role() string {
	switch i.Kind {
	case itemUser:
		return "you"
	case itemAssistant:
		return "sumi"
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
	for _, s := range i.Segments {
		switch s.Kind {
		case segText:
			if text := strings.TrimSpace(textutil.Valid(s.Text)); text != "" {
				b.WriteString("\n\n")
				b.WriteString(text)
			}
		case segTool:
			if text := strings.TrimSpace(textutil.Valid(s.Text)); text != "" {
				b.WriteString("\n\n")
				b.WriteString("Tool: ")
				b.WriteString(text)
			}
			if detail := strings.TrimSpace(textutil.Valid(s.Detail)); detail != "" {
				b.WriteString("\n")
				b.WriteString(detail)
			}
		case segReasoning:
			if text := strings.TrimSpace(textutil.Valid(s.Text)); text != "" {
				b.WriteString("\n\n")
				b.WriteString("Thinking: ")
				b.WriteString(text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func (i *chatItem) appendText(text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	for j := len(i.Segments) - 1; j >= 0; j-- {
		if i.Segments[j].Kind == segText {
			i.Segments[j].Text += text
			return
		}
	}
	i.Segments = append(i.Segments, chatSegment{
		Kind: segText,
		Text: text,
		Time: time.Now(),
	})
}

func (i *chatItem) appendReasoning(text string) {
	text = textutil.Valid(text)
	if text == "" {
		return
	}
	for j := len(i.Segments) - 1; j >= 0; j-- {
		if i.Segments[j].Kind == segReasoning {
			i.Segments[j].Text += text
			return
		}
	}
	i.Segments = append(i.Segments, chatSegment{
		Kind: segReasoning,
		Text: text,
		Time: time.Now(),
	})
}

func (i *chatItem) addTool(name, text, detail string, t time.Time) int {
	if t.IsZero() {
		t = time.Now()
	}
	i.Segments = append(i.Segments, chatSegment{
		Kind:   segTool,
		Tool:   textutil.Valid(strings.TrimSpace(name)),
		Text:   textutil.Valid(text),
		Status: "running",
		Detail: textutil.Valid(detail),
		Time:   t,
	})
	return len(i.Segments) - 1
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
