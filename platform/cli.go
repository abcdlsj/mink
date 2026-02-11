package platform

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
)

var (
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

type CLI struct {
	bus    *bus.Bus
	router *cmd.Router
	hooks  *hook.Manager
	stop   chan struct{}
}

func NewCLI(b *bus.Bus, r *cmd.Router, h *hook.Manager) *CLI {
	return &CLI{
		bus:    b,
		router: r,
		hooks:  h,
		stop:   make(chan struct{}),
	}
}

func (c *CLI) ID() string { return "cli" }

func (c *CLI) Start(ctx context.Context) error {
	ch := make(chan bus.Msg, 64)
	c.bus.Subscribe(bus.TypeAssistant, ch)
	c.bus.Subscribe(bus.TypeToolCall, ch)
	c.bus.Subscribe(bus.TypeToolResult, ch)
	c.bus.Subscribe(bus.TypeToolError, ch)

	fmt.Println(gray.Render("mink. type 'exit' to quit"))

	go c.run(ctx, ch)
	return nil
}

func (c *CLI) Stop() error {
	close(c.stop)
	return nil
}

func (c *CLI) run(ctx context.Context, ch chan bus.Msg) {
	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-c.stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		fmt.Print(cyan.Render("> "))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		in := strings.TrimSpace(line)
		if in == "" {
			continue
		}
		if in == "exit" {
			return
		}

		c.hooks.Trigger(ctx, hook.BeforeInput, in)

		if cmd.IsCommand(in) {
			out, ok, err := c.router.Route(ctx, in)
			if ok {
				if err != nil {
					fmt.Printf("%s %s\n", red.Render("✗"), red.Render(err.Error()))
				} else {
					fmt.Printf("%s\n", gray.Render(out))
				}
				c.hooks.Trigger(ctx, hook.AfterInput, in)
				continue
			}
		}

		c.bus.Pub(bus.Msg{
			Type:    bus.TypeUserInput,
			From:    "cli",
			To:      "main",
			Payload: in,
		})

		c.waitResponse(ch)
		c.hooks.Trigger(ctx, hook.AfterInput, in)
	}
}

func (c *CLI) waitResponse(ch chan bus.Msg) {
	for {
		select {
		case m := <-ch:
			if m.To != "" && m.To != "cli" && m.To != "*" {
				continue
			}
			switch m.Type {
			case bus.TypeAssistant:
				fmt.Printf("%s %s\n", green.Render("🤖"), green.Render(fmt.Sprintf("%v", m.Payload)))
				return
			case bus.TypeToolCall:
				fmt.Printf("%s %s\n", yellow.Render("◉"), yellow.Render(fmt.Sprintf("%v", m.Payload)))
			case bus.TypeToolResult:
				c.printResult(m.Payload)
			case bus.TypeToolError:
				fmt.Printf("%s %s\n", red.Render("✗"), red.Render(fmt.Sprintf("%v", m.Payload)))
			}
		case <-c.stop:
			return
		}
	}
}

func (c *CLI) printResult(payload any) {
	out := fmt.Sprintf("%v", payload)
	lines := strings.Split(out, "\n")
	fmt.Printf("%s result:\n", green.Render("✓"))
	for i, line := range lines {
		if i >= 5 {
			fmt.Printf("  %s\n", gray.Render("... (truncated)"))
			break
		}
		if len(line) > 100 {
			fmt.Printf("  %s...\n", gray.Render(line[:100]))
		} else {
			fmt.Printf("  %s\n", gray.Render(line))
		}
	}
}
