package platform

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/hook"
)

type busMsg bus.Msg

type agentState struct {
	id           string
	task         string
	lines        []string
	done         bool
	directOutput bool
	spinner      spinner.Model
}

type toolState struct {
	id      string
	agentID string
	name    string
	args    string
	line    int
	done    bool
	err     string
	spinner spinner.Model
}

type delegationState struct {
	id     string
	from   string
	to     string
	desc   string
	status string
	detail string
	done   bool
}

type model struct {
	cli         *CLI
	input       textarea.Model
	output      []string
	agents      map[string]*agentState
	agentKeys   []string
	tools       map[string]*toolState
	toolLog     []string
	delegations map[string]*delegationState
	delegateIDs []string
	quitting    bool
	width       int
	height      int
	pending     int
	spinner     spinner.Model
	streaming   bool
	streamBuf   strings.Builder
	streamStart int
	thinking    bool
	thinkingBuf strings.Builder
	thinkStart  int
	scroll      int
	statusCache *StatusInfo
	lastSession string
}

type layoutMetrics struct {
	outputHeight    int
	maxScroll       int
	agentDetailLine int
	mainWidth       int
	sidebarWidth    int
	showSidebar     bool
}

func (m *model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	m.statusCache = nil
	m.refreshInputMode()

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+p", "alt+up":
			m.scrollUp(1)
			return m, nil
		case "ctrl+n", "alt+down":
			m.scrollDown(1)
			return m, nil
		case "ctrl+b":
			m.scrollUp(m.pageSize())
			return m, nil
		case "ctrl+f":
			m.scrollDown(m.pageSize())
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			if m.input.Value() == "" {
				m.scrollUp(1)
				return m, nil
			}
		case tea.KeyDown:
			if m.input.Value() == "" {
				m.scrollDown(1)
				return m, nil
			}
		case tea.KeyEscape:
			return m.handleInterrupt()
		case tea.KeyEnter:
			return m.handleSubmit()
		case tea.KeyCtrlJ:
			m.input, _ = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m.updateInputHeight()
			return m, nil
		case tea.KeyPgUp:
			m.scrollUp(m.pageSize())
			return m, nil
		case tea.KeyPgDown:
			m.scrollDown(m.pageSize())
			return m, nil
		case tea.KeyHome:
			m.scrollToTop()
			return m, nil
		case tea.KeyEnd:
			m.scrollToBottom()
			return m, nil
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollUp(mouseScrollStep)
			return m, nil
		case tea.MouseButtonWheelDown:
			m.scrollDown(mouseScrollStep)
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeInput()
		m.updateInputHeight()

	case busMsg:
		return m.handleBusMsg(bus.Msg(msg))

	case confirmRequestMsg:
		m.refreshInputMode()
		return m, nil

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
		for _, ts := range m.tools {
			if !ts.done && ts.line >= 0 && ts.line < len(m.output) {
				var cmd tea.Cmd
				ts.spinner, cmd = ts.spinner.Update(msg)
				cmds = append(cmds, cmd)
				display := truncate(ts.name+" "+ts.args, 100)
				m.output[ts.line] = ts.spinner.View() + " " + styleTool.Render(display)
			}
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateInputHeight()
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *model) handleSubmit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	m.input.SetHeight(2)

	if text == "" {
		return m, nil
	}

	if m.tryHandleConfirm(text) {
		return m, nil
	}

	if text == "exit" {
		m.quitting = true
		return m, tea.Quit
	}

	m.appendOutput(stylePrompt.Render("» ") + text)

	src := m.cli.Source()
	ctx := bus.WithSource(context.Background(), src)

	m.cli.hooks.Trigger(ctx, hook.BeforeInput, text)

	if command.IsCommand(text) {
		out, ok, err := m.cli.router.Route(ctx, text)
		if ok {
			if err != nil {
				m.appendOutput(styleFail.Render("✗ " + err.Error()))
			} else {
				m.appendOutput(renderMarkdown(out))
			}
			m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
			return m, nil
		}
	}

	_ = m.cli.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: text,
	})

	m.cli.hooks.Trigger(ctx, hook.AfterInput, text)
	m.pending++
	m.scrollToBottom()
	return m, m.spinner.Tick
}

func (m *model) handleInterrupt() (tea.Model, tea.Cmd) {
	if m.pending == 0 {
		return m, nil
	}

	_ = m.cli.bus.Pub(bus.Msg{
		Type:    bus.TypeInterrupt,
		From:    m.cli.Source(),
		To:      bus.AddrAgentMain,
		Payload: "user interrupted",
	})

	m.appendOutput(styleDim.Render("[Interrupted]"))
	return m, nil
}

