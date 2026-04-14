package cliapp

import (
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
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
