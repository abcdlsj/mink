package main

import (
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type command struct {
	Kind   string `json:"kind"`
	Prompt struct {
		Sections []struct {
			Name string `json:"name"`
			Text string `json:"text"`
		} `json:"sections"`
	} `json:"prompt"`
}

func main() {
	if os.Getenv("SUMI_EXTERNAL_DRIVER") == "" || os.Getenv("HOME") == "" {
		os.Exit(2)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(3)
	}
	var value command
	if err := json.Unmarshal(input, &value); err != nil || value.Kind != "prompt" || len(value.Prompt.Sections) == 0 {
		os.Exit(4)
	}
	mode := "success"
	for _, section := range value.Prompt.Sections {
		if section.Name == "current_input" && strings.HasPrefix(section.Text, "external-mode:") {
			mode = strings.TrimPrefix(section.Text, "external-mode:")
		}
	}
	switch mode {
	case "success":
		_, _ = os.Stdout.WriteString(`{"type":"result","result":{"outcome":"succeeded","body":"external blackbox completion"}}` + "\n")
	case "duplicate":
		_, _ = os.Stdout.WriteString(`{"type":"result","result":{"outcome":"succeeded","body":"first"}}` + "\n")
		_, _ = os.Stdout.WriteString(`{"type":"result","result":{"outcome":"succeeded","body":"second"}}` + "\n")
	case "event-only":
		_, _ = os.Stdout.WriteString(`{"type":"event","kind":"started"}` + "\n")
	case "ordinary":
		_, _ = os.Stdout.WriteString("ordinary stdout\n")
	case "partial":
		_, _ = os.Stdout.WriteString(`{"type":"result"`)
	case "stdout-overflow":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 8192))
	case "stderr-overflow":
		_, _ = os.Stderr.WriteString(strings.Repeat("x", 8192))
	case "timeout":
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Hour)
		}
	case "result-overflow":
		_, _ = os.Stdout.WriteString(`{"type":"result","result":{"outcome":"succeeded","body":"` + strings.Repeat("x", 400_001) + `"}}` + "\n")
	default:
		os.Exit(5)
	}
}
