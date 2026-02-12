package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	"github.com/abcdlsj/mink/hook"
)

var (
	stylePrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Bold(true)
	styleAssist  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1"))
	styleTool    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF")).Faint(true)
	styleCmd     = lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")).Faint(true)
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("#94E2D5"))
	styleFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#585B70"))
	styleAgent   = lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)
)

type agentState struct {
	id      string
	task    string
	lines   []string
	done    bool
	spinner spinner.Model
}

type busMsg bus.Msg

type CLI struct {
	bus    *bus.Bus
	router *cmd.Router
	hooks  *hook.Manager
	stop   chan struct{}

	program   *tea.Program
	model     *model
	confirmMu sync.Mutex
	confirmCh chan bool
}

type model struct {
	cli       *CLI
	input     textinput.Model
	output    []string
	agents    map[string]*agentState
	agentKeys []string
	quitting  bool
	width     int
	height    int
	pending   int
	spinner   spinner.Model
	streaming bool   // 是否正在流式输出
	streamBuf string // 流式缓冲
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
	c.subscribeMessages(ctx)
	return nil
}

func (c *CLI) Run() error {
	ti := textinput.New()
	ti.Placeholder = "Type your message..."
	ti.Prompt = ""
	ti.Focus()
	ti.Width = 80

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleDim

	m := &model{
		cli:       c,
		input:     ti,
		output:    []string{styleDim.Render("mink. type 'exit' to quit")},
		agents:    make(map[string]*agentState),
		agentKeys: []string{},
		spinner:   s,
	}
	c.model = m

	c.program = tea.NewProgram(m, tea.WithAltScreen())
	_, err := c.program.Run()
	return err
}

func (c *CLI) Stop() error {
	close(c.stop)
	if c.program != nil {
		c.program.Quit()
	}
	return nil
}

func (c *CLI) subscribeMessages(ctx context.Context) {
	ch := make(chan bus.Msg, 64)
	c.bus.Subscribe(bus.TypeAssistant, ch)
	c.bus.Subscribe(bus.TypeTurnDone, ch)
	c.bus.Subscribe(bus.TypeToolCall, ch)
	c.bus.Subscribe(bus.TypeToolResult, ch)
	c.bus.Subscribe(bus.TypeToolError, ch)
	c.bus.Subscribe(bus.TypeCommand, ch)
	c.bus.Subscribe(bus.TypeCommandOK, ch)
	c.bus.Subscribe(bus.TypeCommandError, ch)
	c.bus.Subscribe(bus.TypeAgentSpawn, ch)
	c.bus.Subscribe(bus.TypeAgentDone, ch)
	c.bus.Subscribe(bus.TypeTaskStart, ch)
	c.bus.Subscribe(bus.TypeTaskDone, ch)
	c.bus.Subscribe(bus.TypeStreamChunk, ch)
	c.bus.Subscribe(bus.TypeStreamEnd, ch)
	c.bus.Subscribe(bus.TypeSessionNew, ch)

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

func (c *CLI) Allow(ctx context.Context, raw string) (bool, error) {
	// 简化版：直接返回 true，后续可以用 tea 实现确认
	return true, nil
}

// bubbletea Model 接口实现

func (m *model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			return m.handleSubmit()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 4

	case busMsg:
		return m.handleBusMsg(bus.Msg(msg))

	case spinner.TickMsg:
		if m.pending > 0 {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		for _, agent := range m.agents {
			if !agent.done {
				var cmd tea.Cmd
				agent.spinner, cmd = agent.spinner.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *model) handleSubmit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	m.input.SetValue("")

	if text == "" {
		return m, nil
	}

	if text == "exit" {
		m.quitting = true
		return m, tea.Quit
	}

	m.output = append(m.output, stylePrompt.Render("› ")+text)

	ctx := context.Background()
	m.cli.hooks.Trigger(ctx, hook.BeforeInput, text)

	if cmd.IsCommand(text) {
		out, ok, err := m.cli.router.Route(ctx, text)
		if ok {
			if err != nil {
				m.output = append(m.output, styleFail.Render("✗ "+err.Error()))
			} else {
				m.output = append(m.output, styleDim.Render(out))
			}
			m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
			return m, nil
		}
	}

	_ = m.cli.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    bus.AddrPlatformCLI,
		To:      bus.AddrAgentMain,
		Payload: text,
	})

	m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
	m.pending++
	return m, m.spinner.Tick
}

func (m *model) handleBusMsg(msg bus.Msg) (tea.Model, tea.Cmd) {
	if msg.To != bus.AddrBroadcast && msg.To != bus.AddrPlatformCLI {
		return m, nil
	}

	isSubAgent := msg.From != "" && msg.From != bus.AddrAgentMain && msg.From != bus.AddrSystemSup

	switch msg.Type {
	case bus.TypeAgentSpawn:
		return m.handleAgentSpawn(msg)

	case bus.TypeAgentDone:
		return m.handleAgentDone(msg)

	case bus.TypeAssistant:
		if isSubAgent {
			m.appendToAgent(msg.From, styleAssist.Render(truncate(fmt.Sprintf("%v", msg.Payload), 60)))
		} else {
			m.output = append(m.output, "")
			m.output = append(m.output, styleAssist.Render(fmt.Sprintf("%v", msg.Payload)))
		}

	case bus.TypeToolCall:
		line := styleTool.Render("◉ " + truncate(fmt.Sprintf("%v", msg.Payload), 60))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeToolResult:
		line := styleSuccess.Render("✓ done")
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeToolError:
		line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 60))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeCommand:
		line := styleCmd.Render("$ " + fmt.Sprintf("%v", msg.Payload))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeCommandOK:
		line := styleSuccess.Render("✓ done")
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeCommandError:
		line := styleFail.Render("✗ " + truncate(fmt.Sprintf("%v", msg.Payload), 60))
		if isSubAgent {
			m.appendToAgent(msg.From, line)
		} else {
			m.output = append(m.output, line)
		}

	case bus.TypeTurnDone:
		if msg.From == bus.AddrAgentMain {
			if m.pending > 0 {
				m.pending--
			}
			m.agentKeys = []string{}
			m.agents = make(map[string]*agentState)
		}

	case bus.TypeTaskStart:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			cmd := truncate(payload["cmd"], 40)
			m.output = append(m.output, styleTool.Render(fmt.Sprintf("[%s] %s", taskID, cmd)))
		}

	case bus.TypeTaskDone:
		if payload, ok := msg.Payload.(map[string]string); ok {
			taskID := payload["task_id"]
			status := payload["status"]
			if status == "ok" {
				m.output = append(m.output, styleSuccess.Render(fmt.Sprintf("✓ [%s] completed", taskID)))
			} else {
				errMsg := truncate(payload["error"], 40)
				m.output = append(m.output, styleFail.Render(fmt.Sprintf("✗ [%s] %s", taskID, errMsg)))
			}
		}

	case bus.TypeStreamChunk:
		delta, _ := msg.Payload.(string)
		if m.streaming {
			m.streamBuf += delta
			if len(m.output) > 0 {
				m.output[len(m.output)-1] = styleAssist.Render(m.streamBuf)
			}
		} else {
			m.streaming = true
			m.streamBuf = delta
			m.output = append(m.output, "")
			m.output = append(m.output, styleAssist.Render(delta))
		}

	case bus.TypeStreamEnd:
		m.streaming = false
		m.streamBuf = ""

	case bus.TypeSessionNew:
		if id, ok := msg.Payload.(string); ok {
			m.output = append(m.output, styleDim.Render(fmt.Sprintf("[Session] %s", id)))
		}
	}

	return m, nil
}

func (m *model) handleAgentSpawn(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}
	task := truncate(payload["task"], 40)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleAgent

	m.agents[id] = &agentState{
		id:      id,
		task:    task,
		lines:   []string{},
		spinner: s,
	}
	m.agentKeys = append(m.agentKeys, id)

	return m, m.agents[id].spinner.Tick
}

func (m *model) handleAgentDone(msg bus.Msg) (tea.Model, tea.Cmd) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return m, nil
	}

	id := payload["agent_id"]
	if id == "" {
		id = msg.From
	}

	if agent, exists := m.agents[id]; exists {
		agent.done = true
	}

	return m, nil
}

func (m *model) appendToAgent(id string, line string) {
	if agent, exists := m.agents[id]; exists {
		agent.lines = append(agent.lines, line)
		if len(agent.lines) > 5 {
			agent.lines = agent.lines[len(agent.lines)-5:]
		}
	}
}

func (m *model) View() string {
	if m.quitting {
		return "Bye!\n"
	}

	var b strings.Builder

	// 输出区域（最近的消息）
	outputLines := m.output
	maxOutput := max(m.height-10, 5)
	if len(outputLines) > maxOutput {
		outputLines = outputLines[len(outputLines)-maxOutput:]
	}
	for _, line := range outputLines {
		b.WriteString(line + "\n")
	}

	// Agent 面板
	if len(m.agentKeys) > 0 {
		b.WriteString("\n")
		b.WriteString(styleDim.Render("─── agents ───") + "\n")
		for _, id := range m.agentKeys {
			agent := m.agents[id]
			if agent == nil {
				continue
			}

			var status string
			if agent.done {
				status = styleSuccess.Render("✓")
			} else {
				status = agent.spinner.View()
			}

			fmt.Fprintf(&b, "%s %s %s\n",
				status,
				styleAgent.Render(id),
				styleDim.Render(agent.task))

			for _, line := range agent.lines {
				b.WriteString("  " + line + "\n")
			}
		}
		b.WriteString(styleDim.Render("──────────────") + "\n")
	}

	// 输入区域
	b.WriteString("\n")
	if m.pending > 0 {
		b.WriteString(m.spinner.View() + " ")
	}
	b.WriteString(stylePrompt.Render("› ") + m.input.View())

	return b.String()
}
