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
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/tool"
)

const (
	maxOutputLines   = 4000
	agentLineLimit   = 8
	mouseScrollStep  = 1
	minOutputLines   = 5
	maxInputHeight   = 8
	sidebarMinWidth  = 40
	sidebarMaxWidth  = 48
	sidebarGap       = 1
	minWideWidth     = 116
	minMainWidth     = 56
	sidebarAgents    = 4
	sidebarTools     = 6
	sidebarDelegates = 5
	composerLines    = 0
	transcriptLines  = 0
)

var (
	stylePrompt       = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8790")).Bold(true)
	stylePromptDanger = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).Bold(true)
	styleTool         = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C"))
	styleSuccess      = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	styleFail         = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75"))
	styleDim          = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	styleBar          = lipgloss.NewStyle().Foreground(lipgloss.Color("#30343B"))
	styleSession      = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8C0CC"))
	styleAgent        = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7")).Bold(true)
	styleCode         = lipgloss.NewStyle().Foreground(lipgloss.Color("#C8A36A"))
	styleBold         = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Bold(true)
	styleThinking     = lipgloss.NewStyle().Foreground(lipgloss.Color("#56B6C2")).Faint(true)
	styleThinkingTag  = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C")).Faint(true)
	stylePlaceholder  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	styleConfirmBanner = lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#E06C75")).Bold(true).Padding(0, 1)
	styleConfirmCmd    = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C")).Bold(true)
	styleConfirmHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Bold(true)

	styleFrame = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#313244")).
			Padding(0, 1)
	styleTranscriptFrame = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#3F4752")).
				Padding(0, 1)
	styleInputFrame = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#D4A85C")).
			Padding(0, 1)
	styleSidebarFrame = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#2F3540")).
				Padding(0, 1)
	styleSectionTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C")).Bold(true)
	styleMutedBlock   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	styleKeycap       = lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#C7D2FE")).Bold(true).Padding(0, 1)
	styleSidebarLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C")).Bold(true)
	styleSidebarValue = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))
	styleAgentIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379")).Bold(true)
	styleAgentBusy    = lipgloss.NewStyle().Foreground(lipgloss.Color("#D4A85C")).Bold(true)
	styleAgentSleep   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7")).Bold(true)
	styleAgentOff     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Bold(true)
	styleSidebarBadge = lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#D4A85C")).Bold(true).Padding(0, 1)

	styleChipLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	styleChipValue   = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8C0CC"))
	styleChipThread  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
	styleChipBlocker = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75"))
	styleChipSpeaker = lipgloss.NewStyle().Foreground(lipgloss.Color("#56B6C2"))
	styleChipSummary = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8790"))
	styleChipMembers = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
)

type AgentInfo struct {
	ID     string
	Name   string
	Status string
	Runs   int
	Caps   []string
}

type TeamMemberInfo struct {
	ID   string
	Name string
	Role string
	Kind string
}

type ThreadInfo struct {
	ID              string
	Title           string
	Status          string
	UpdatedAt       string
	CurrentRound    int
	Goal            string
	BestAnswer      string
	OpenBlockers    string
	LatestSummary   string
	LatestSummaryAt string
}

type TeamStatus struct {
	ID             string
	Name           string
	Status         string
	LeaderID       string
	LatestSummary  string
	CurrentBlocker string
	SummaryTime    string
	ActiveSpeaker  string
	Members        []TeamMemberInfo
	RecentThreads  []ThreadInfo
	ActiveThread   *ThreadInfo
}

type StatusInfo struct {
	Model     string
	TokenIn   int
	TokenOut  int
	Workspace string
	Session   string
	Agents    []AgentInfo
	Team      *TeamStatus
}

type CLI struct {
	bus       *bus.Bus
	router    *command.Router
	hooks     *hook.Manager
	statusFn  func() StatusInfo
	sessionFn func() []msg.Message
	stop      chan struct{}

	program *tea.Program
	model   *model
	events  chan bus.Msg

	confirmMu sync.Mutex
	confirmCh chan tool.Approval
	confirmOn bool
	confirmQ  string
}

func NewCLI(b *bus.Bus, r *command.Router, h *hook.Manager, sf func() StatusInfo, sessionFn func() []msg.Message) *CLI {
	return &CLI{
		bus:       b,
		router:    r,
		hooks:     h,
		statusFn:  sf,
		sessionFn: sessionFn,
		stop:      make(chan struct{}),
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
	ta.FocusedStyle.Placeholder = stylePlaceholder
	ta.BlurredStyle.Placeholder = stylePlaceholder
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
		cli:         c,
		input:       ta,
		output:      []string{styleDim.Render("mink. type 'exit' to quit")},
		agents:      make(map[string]*agentState),
		agentKeys:   []string{},
		tools:       make(map[string]*toolState),
		toolLog:     []string{},
		delegations: make(map[string]*delegationState),
		delegateIDs: []string{},
		spinner:     s,
	}
	c.model = m

	fmt.Print("\033[2J\033[H")
	c.program = tea.NewProgram(m)
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
	ch := make(chan bus.Msg, 512)
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
