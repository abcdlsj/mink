package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abcdlsj/mink/event"
)

type Model struct {
	agent    Agent
	bus      *event.Bus
	vp       viewport.Model
	input    textarea.Model
	msgs     []Msg
	thinking bool
	w, h     int
	ready    bool
}

type Agent interface {
	NewSession() error
	Run(input string) error
	Cmd(name string, args []string) (string, error)
}

type Msg struct {
	Role    string
	Content string
	Tool    string
}

func New(a Agent, b *event.Bus) *Model {
	in := textarea.New()
	in.Placeholder = "Type..."
	in.ShowLineNumbers = false
	return &Model{agent: a, bus: b, input: in}
}

func (m *Model) Init() tea.Cmd {
	m.bus.Subscribe(event.AssistantMsg, func(e event.Event) {
		m.msgs = append(m.msgs, Msg{Role: "assistant", Content: e.Data.(string)})
		m.update()
	})
	m.bus.Subscribe(event.ToolStart, func(e event.Event) {
		d := e.Data.(map[string]string)
		m.msgs = append(m.msgs, Msg{Role: "tool", Tool: d["name"]})
		m.thinking = true
		m.update()
	})
	m.bus.Subscribe(event.ToolEnd, func(e event.Event) {
		m.thinking = false
		m.update()
	})
	m.bus.Subscribe(event.AgentStart, func(e event.Event) { m.thinking = true })
	m.bus.Subscribe(event.AgentEnd, func(e event.Event) { m.thinking = false })
	return nil
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if !m.ready {
			m.vp = viewport.New(m.w, m.h-3)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = m.w, m.h-3
		}
		m.input.SetWidth(m.w)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if m.thinking {
				return m, nil
			}
			in := m.input.Value()
			if in == "" {
				return m, nil
			}
			m.msgs = append(m.msgs, Msg{Role: "user", Content: in})
			m.input.Reset()
			m.update()

			if strings.HasPrefix(in, "/") {
				parts := strings.Fields(in[1:])
				if len(parts) > 0 {
					out, err := m.agent.Cmd(parts[0], parts[1:])
					if err != nil {
						m.msgs = append(m.msgs, Msg{Role: "error", Content: err.Error()})
					} else {
						m.msgs = append(m.msgs, Msg{Role: "system", Content: out})
					}
					m.update()
				}
			} else {
				go m.agent.Run(in)
			}

		case tea.KeyCtrlC:
			return m, tea.Quit
		}
	}

	m.input, _ = m.input.Update(msg)
	m.vp, _ = m.vp.Update(msg)
	return m, nil
}

func (m *Model) View() string {
	if !m.ready {
		return "loading..."
	}
	return fmt.Sprintf("%s\n%s\n%s", m.vp.View(), m.input.View(), m.status())
}

func (m *Model) status() string {
	if m.thinking {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render("thinking...")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render("ready")
}

func (m *Model) update() {
	var b strings.Builder
	for _, msg := range m.msgs {
		switch msg.Role {
		case "user":
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#0FF")).Render("you: ") + msg.Content)
		case "assistant":
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#0F0")).Render("ai: ") + msg.Content)
		case "system":
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Render("sys: ") + msg.Content)
		case "error":
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#F00")).Render("err: ") + msg.Content)
		case "tool":
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0")).Render("🔧 " + msg.Tool))
		}
		b.WriteString("\n\n")
	}
	m.vp.SetContent(b.String())
	m.vp.GotoBottom()
}
