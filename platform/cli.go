package platform

import (
	"context"
	"fmt"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/tool"
)

const (
	maxOutputLines  = 4000
	agentLineLimit  = 8
	mouseScrollStep = 1
	minOutputLines  = 5
	maxInputHeight  = 8
)

var (
	stylePrompt       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Bold(true)
	stylePromptDanger = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).Bold(true)
	styleTool         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Faint(true)
	styleSuccess      = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5"))
	styleFail         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
	styleBar          = lipgloss.NewStyle().Foreground(lipgloss.Color("#9399B2"))
	styleSession      = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6ADC8"))
	styleAgent        = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
	styleCode         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5C2E7"))
	styleBold         = lipgloss.NewStyle().Bold(true)
	styleThinking     = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA"))

	styleConfirmBanner = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E1E2E")).Background(lipgloss.Color("#F38BA8")).Bold(true).Padding(0, 1)
	styleConfirmCmd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387")).Bold(true)
	styleConfirmHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Bold(true)
)

type StatusInfo struct {
	Model     string
	TokenIn   int
	TokenOut  int
	Workspace string
	Session   string
}

type CLI struct {
	bus      *bus.Bus
	router   *command.Router
	hooks    *hook.Manager
	statusFn func() StatusInfo
	stop     chan struct{}

	program *tea.Program
	model   *model
	events  chan bus.Msg

	confirmMu sync.Mutex
	confirmCh chan tool.Approval
	confirmOn bool
	confirmQ  string
}

func NewCLI(b *bus.Bus, r *command.Router, h *hook.Manager, sf func() StatusInfo) *CLI {
	return &CLI{
		bus:      b,
		router:   r,
		hooks:    h,
		statusFn: sf,
		stop:     make(chan struct{}),
	}
}

func (c *CLI) ID() string { return "cli" }

func (c *CLI) Start(ctx context.Context) error {
	c.subscribeMessages(ctx)
	return nil
}

func (c *CLI) Run() error {
	ta := textarea.New()
	ta.Placeholder = "Type message... (Enter: submit, Ctrl+J: newline)"
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.SetWidth(80)
	ta.SetHeight(2)
	ta.EndOfBufferCharacter = ' '
	ta.CharLimit = 0
	ta.Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleDim

	m := &model{
		cli:       c,
		input:     ta,
		output:    []string{styleDim.Render("mink. type 'exit' to quit")},
		agents:    make(map[string]*agentState),
		agentKeys: []string{},
		tools:     make(map[string]*toolState),
		spinner:   s,
	}
	c.model = m

	c.program = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	_, err := c.program.Run()
	return err
}

func (c *CLI) Stop() error {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	if c.events != nil {
		c.bus.Unobserve(c.events)
		c.events = nil
	}
	if c.program != nil {
		c.program.Quit()
	}
	return nil
}

func (c *CLI) subscribeMessages(ctx context.Context) {
	if c.events != nil {
		c.bus.Unobserve(c.events)
	}
	ch := make(chan bus.Msg, 64)
	c.events = ch
	c.bus.Observe(ch)

	go func() {
		for {
			select {
			case m := <-ch:
				if c.program != nil {
					c.program.Send(busMsg(m))
				}
			case <-c.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (c *CLI) Approve(ctx context.Context, raw string) (tool.Approval, error) {
	c.confirmMu.Lock()
	if c.confirmOn {
		c.confirmMu.Unlock()
		return tool.Denied, fmt.Errorf("another confirmation is in progress")
	}

	ch := make(chan tool.Approval, 1)
	c.confirmOn = true
	c.confirmCh = ch
	c.confirmQ = raw
	c.confirmMu.Unlock()

	if c.program != nil {
		c.program.Send(confirmRequestMsg(raw))
	}

	select {
	case a := <-ch:
		return a, nil
	case <-ctx.Done():
		c.confirmMu.Lock()
		if c.confirmOn && c.confirmCh == ch {
			c.confirmOn = false
			c.confirmCh = nil
			c.confirmQ = ""
		}
		c.confirmMu.Unlock()
		return tool.Denied, ctx.Err()
	}
}

type confirmRequestMsg string
