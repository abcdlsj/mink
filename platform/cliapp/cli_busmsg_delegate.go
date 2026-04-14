package cliapp

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/abcdlsj/mink/bus"
)

func (m *model) handleTaskStartMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	cmdText := truncate(payload["cmd"], 60)
	m.appendOutput(styleTool.Render(fmt.Sprintf("[%s] %s", taskID, cmdText)))
}

func (m *model) handleTaskDoneMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	if payload["status"] == "ok" {
		m.appendOutput(styleSuccess.Render(fmt.Sprintf("✓ [%s] completed", taskID)))
		return
	}
	errMsg := truncate(payload["error"], 80)
	m.appendOutput(styleFail.Render(fmt.Sprintf("✗ [%s] %s", taskID, errMsg)))
}

func (m *model) handleDelegateMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]any)
	if !ok {
		return
	}
	d := m.upsertDelegation(msg.ID)
	if d == nil {
		return
	}
	d.from = msg.From
	d.status = "queued"
	if desc, _ := payload["description"].(string); desc != "" {
		d.desc = desc
	}
	if target, _ := payload["target_agent"].(string); target != "" {
		d.to = target
	}
	if len(d.to) == 0 {
		if caps, ok := payload["capabilities"].([]any); ok && len(caps) > 0 {
			var items []string
			for _, cap := range caps {
				if text, ok := cap.(string); ok && text != "" {
					items = append(items, text)
				}
			}
			if len(items) > 0 {
				d.to = strings.Join(items, ",")
			}
		}
	}
}

func (m *model) handleDelegateAckMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	if taskID == "" {
		taskID = msg.ReplyTo
	}
	d := m.upsertDelegation(taskID)
	if d == nil {
		return
	}
	if d.to == "" {
		d.to = msg.From
	}
	status := payload["status"]
	if status == "" {
		status = "accepted"
	}
	d.status = status
	if errMsg := payload["error"]; errMsg != "" {
		d.status = "error"
		d.done = true
		d.detail = errMsg
	}
}

func (m *model) handleDelegateResultMsg(msg bus.Msg) {
	payload, ok := msg.Payload.(map[string]string)
	if !ok {
		return
	}
	taskID := payload["task_id"]
	if taskID == "" {
		taskID = msg.ReplyTo
	}
	d := m.upsertDelegation(taskID)
	if d == nil {
		return
	}
	d.to = msg.From
	d.done = true
	if errMsg := payload["error"]; errMsg != "" {
		d.status = "error"
		d.detail = errMsg
		return
	}
	d.status = payload["status"]
	if d.status == "" {
		d.status = "done"
	}
	if output := payload["output"]; output != "" {
		d.detail = truncate(stripANSI(output), 80)
	}
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
	task := truncate(payload["task"], 60)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleAgent

	if _, exists := m.agents[id]; !exists {
		m.agentKeys = append(m.agentKeys, id)
	}
	m.agents[id] = &agentState{
		id:           id,
		task:         task,
		lines:        []string{},
		directOutput: payload["direct_output"] == "true",
		spinner:      s,
	}

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
