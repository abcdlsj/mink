package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/textutil"
)

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
	fmt.Fprintf(&b, "tool: %s\n", strings.TrimSpace(ev.Tool))
	if input := strings.TrimSpace(textutil.Valid(ev.Input)); input != "" {
		fmt.Fprintf(&b, "input:\n%s\n", input)
	}
	if output := strings.TrimSpace(textutil.Valid(ev.Output)); output != "" {
		fmt.Fprintf(&b, "\noutput:\n%s\n", output)
	}
	if failed && strings.TrimSpace(ev.Err) != "" {
		fmt.Fprintf(&b, "\nerror:\n%s\n", textutil.Valid(ev.Err))
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
		switch val := v.(type) {
		case string:
			out[k] = textutil.Valid(val)
		}
	}
	return out
}
