package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/abcdlsj/mink/bus"
)

func runCLI(ctx context.Context, a *App, args []string) error {
	events, cancel := a.bus.Subscribe(256)
	defer cancel()

	go func() {
		var open bool
		for ev := range events {
			if ev.Source != "cli" {
				continue
			}
			switch ev.Type {
			case bus.TurnChunk:
				open = true
				fmt.Print(ev.Text)
			case bus.ToolCallStarted:
				if open {
					fmt.Println()
					open = false
				}
				input := strings.TrimSpace(ev.Input)
				if input == "" {
					fmt.Printf("[tool] %s\n", ev.Tool)
				} else {
					fmt.Printf("[tool] %s %s\n", ev.Tool, input)
				}
			case bus.ToolCallFinished:
				if strings.TrimSpace(ev.Output) != "" {
					fmt.Printf("[tool:%s] %s\n", ev.Tool, strings.TrimSpace(ev.Output))
				}
			case bus.ToolCallFailed:
				fmt.Printf("[tool:%s] error: %s\n", ev.Tool, ev.Err)
			case bus.TurnFinished:
				if open {
					fmt.Println()
					open = false
				}
			case bus.TurnError:
				if open {
					fmt.Println()
					open = false
				}
				fmt.Printf("error: %s\n", ev.Err)
			case bus.ServiceNotice:
				if open {
					fmt.Println()
					open = false
				}
				fmt.Println(ev.Text)
			}
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("mink v3")
	fmt.Println("type 'exit' to quit")
	for {
		fmt.Print("mink> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		out, err := a.HandleInput(ctx, "cli", line)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		if strings.HasPrefix(line, "!") && strings.TrimSpace(out) != "" {
			fmt.Println(out)
		}
	}
}
