package main

import (
	"encoding/json"
	"io"
	"os"
)

type command struct {
	Kind   string          `json:"kind"`
	Prompt json.RawMessage `json:"prompt"`
}

func main() {
	if os.Getenv("SUMI_EXTERNAL_DRIVER") != "1" || os.Getenv("HOME") != "" {
		os.Exit(2)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(3)
	}
	var value command
	if err := json.Unmarshal(input, &value); err != nil || value.Kind != "prompt" || len(value.Prompt) == 0 {
		os.Exit(4)
	}
	_, _ = os.Stdout.WriteString(`{"type":"result","result":{"outcome":"succeeded","body":"external blackbox completion"}}` + "\n")
}
