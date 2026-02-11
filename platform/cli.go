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
	prompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Bold(true)
	assist  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))
	tool    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Faint(true)
	command = lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")).Faint(true)
	success = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5"))
	fail    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
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
	c.bus.Subscribe(bus.TypeCommand, ch)
	c.bus.Subscribe(bus.TypeCommandOK, ch)
	c.bus.Subscribe(bus.TypeCommandError, ch)

	fmt.Println(dim.Render("mink. type 'exit' to quit"))

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

		fmt.Print(prompt.Render("› "))
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
					fmt.Printf("%s %s\n", fail.Render("✗"), fail.Render(err.Error()))
				} else {
					fmt.Printf("%s\n", dim.Render(out))
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
				fmt.Printf("\n%s\n", assist.Render(fmt.Sprintf("%v", m.Payload)))
				return
			case bus.TypeToolCall:
				fmt.Printf("%s %s\n", tool.Render("◉"), tool.Render(fmt.Sprintf("%v", m.Payload)))
			case bus.TypeToolResult:
				c.printResult(fmt.Sprintf("%v", m.Payload))
			case bus.TypeToolError:
				c.printError(fmt.Sprintf("%v", m.Payload))
			case bus.TypeCommand:
				fmt.Printf("%s %s\n", command.Render("$"), command.Render(fmt.Sprintf("%v", m.Payload)))
			case bus.TypeCommandOK:
				c.printResult(fmt.Sprintf("%v", m.Payload))
			case bus.TypeCommandError:
				c.printError(fmt.Sprintf("%v", m.Payload))
			}
		case <-c.stop:
			return
		}
	}
}

func (c *CLI) printResult(out string) {
	lines := strings.Split(out, "\n")
	total := len(lines)
	shown := min(total, 4)
	fmt.Printf("%s\n", success.Render("✓ done"))
	for i := range shown {
		line := lines[i]
		if len(line) > 80 {
			line = line[:80] + "…"
		}
		fmt.Printf("  %s\n", dim.Render(line))
	}
	if total > shown {
		fmt.Printf("  %s\n", dim.Render(fmt.Sprintf("… +%d lines", total-shown)))
	}
}

func (c *CLI) printError(msg string) {
	fmt.Printf("%s %s\n", fail.Render("✗ error"), dim.Render(msg))
}
