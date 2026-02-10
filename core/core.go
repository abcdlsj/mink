package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

// Core Agent核心
type Core struct {
	id      string
	p       llm.Provider
	reg     *tool.Registry
	sm      *session.Manager
	bus     *bus.Bus
	tv      *toolView
	sid     string
	maxStep int
	handlers map[string]bus.Handler
}

func New(id string, p llm.Provider, dir string, b *bus.Bus) *Core {
	c := &Core{
		id:       id,
		p:        p,
		reg:      tool.NewRegistry(),
		sm:       session.NewManager(dir),
		bus:      b,
		tv:       newToolView(),
		maxStep:  100,
		handlers: make(map[string]bus.Handler),
	}
	c.registerHandlers()
	return c
}

func (c *Core) registerHandlers() {
	// 注册消息处理器
	c.bus.RegisterHandler(bus.TypeUserInput, c.handleUserInput)
	c.bus.RegisterHandler(bus.TypeToolCall, c.handleToolCall)
}

func (c *Core) handleUserInput(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	// 检查是否是给自己的消息
	if m.To != "" && m.To != c.id && m.To != "*" {
		return bus.Msg{}, nil
	}
	
	input := m.Payload.(string)
	
	// 解析 ,cmd
	if cmd, ok := c.parseCmd(input); ok {
		if err := c.execCmd(ctx, cmd); err != nil {
			return bus.Msg{}, err
		}
		return bus.Msg{Type: bus.TypeAssistant, Payload: "executed"}, nil
	}
	
	// 运行对话
	if err := c.run(ctx, input); err != nil {
		return bus.Msg{}, err
	}
	
	return bus.Msg{}, nil
}

func (c *Core) handleToolCall(ctx context.Context, m bus.Msg) (bus.Msg, error) {
	// 执行工具调用
	return bus.Msg{}, nil
}

func (c *Core) Start(ctx context.Context) {
	// 启动消息处理循环
	conn := c.bus.RegisterAgent(c.id, false)
	
	go func() {
		for {
			select {
			case m := <-conn.Send:
				// 处理收到的消息
				if h, ok := c.handlers[m.Type]; ok {
					resp, _ := h(ctx, m)
					if resp.Type != "" {
						conn.Recv <- resp
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (c *Core) run(ctx context.Context, input string) error {
	if c.sid == "" {
		if _, err := c.NewSession(); err != nil {
			return err
		}
	}
	
	c.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    c.id,
		Payload: input,
		Context: bus.MsgContext{SessionID: c.sid},
	})
	
	c.sm.AddMessage(c.sid, session.Message{Role: "user", Content: input})
	
	for i := 0; i < c.maxStep; i++ {
		done, err := c.step(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	
	return fmt.Errorf("max steps")
}

func (c *Core) step(ctx context.Context) (bool, error) {
	h, _ := c.sm.GetHistory(c.sid, -1)
	m := c.buildMsgs(h)
	t := c.tv.tools(c.reg)
	
	r, err := c.p.Chat(ctx, m, t)
	if err != nil {
		return false, err
	}
	
	// 提取 ,cmd
	cmdOut, rest := c.extractCmd(r.Content)
	if cmdOut != "" {
		if cmd, ok := c.parseCmd(cmdOut); ok {
			if err := c.execCmd(ctx, cmd); err != nil {
				c.sm.AddMessage(c.sid, session.Message{
					Role:    "system",
					Content: fmt.Sprintf("<cmd name=\"%s\" status=\"error\">%s</cmd>", cmd.name, err.Error()),
				})
			} else {
				c.sm.AddMessage(c.sid, session.Message{
					Role:    "system",
					Content: fmt.Sprintf("<cmd name=\"%s\" status=\"ok\">executed</cmd>", cmd.name),
				})
			}
		}
	}
	
	if rest != "" {
		c.bus.Pub(bus.Msg{
			Type:    bus.TypeAssistant,
			From:    c.id,
			Payload: rest,
		})
		c.sm.AddMessage(c.sid, session.Message{Role: "assistant", Content: rest})
	}
	
	if len(r.ToolCalls) == 0 && cmdOut == "" {
		return true, nil
	}
	
	for _, tc := range r.ToolCalls {
		c.tv.expand(tc.Name)
		
		out, err := c.execTool(ctx, tc)
		
		c.bus.Pub(bus.Msg{
			Type:    bus.TypeToolResult,
			From:    c.id,
			Payload: map[string]string{"name": tc.Name, "result": out, "error": func() string { if err != nil { return err.Error() }; return "" }()},
		})
		
		tr := session.ToolResult{ToolCallID: tc.ID, Content: out}
		if err != nil {
			tr.Error = err.Error()
		}
		c.sm.AddMessage(c.sid, session.Message{Role: "tool", ToolResults: []session.ToolResult{tr}})
	}
	
	return false, nil
}

// ... 其他方法和之前类似，省略重复代码 ...

func (c *Core) NewSession() (*session.Session, error) {
	s, err := c.sm.Create()
	if err != nil {
		return nil, err
	}
	c.sid = s.ID
	c.tv.reset()
	c.bus.Pub(bus.Msg{Type: bus.TypeSessionNew, From: c.id, Payload: s})
	return s, nil
}

func (c *Core) buildMsgs(h []session.Message) []llm.Message {
	var r []llm.Message
	r = append(r, llm.Message{Role: "system", Content: c.sysPrompt()})
	
	for _, m := range h {
		msg := llm.Message{Role: m.Role, Content: m.Content}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args})
		}
		for _, tr := range m.ToolResults {
			msg.ToolResults = append(msg.ToolResults, llm.ToolResult{ToolCallID: tr.ToolCallID, Content: tr.Content, Error: tr.Error})
		}
		r = append(r, msg)
	}
	return r
}

func (c *Core) sysPrompt() string {
	var b strings.Builder
	b.WriteString("You are a helpful coding assistant.\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range c.tv.compact(c.reg) {
		b.WriteString(fmt.Sprintf("- %s: %s\n", t.name, t.desc))
	}
	return b.String()
}

// toolView is a minimal copy of the agent's toolView for bus-based Core
type toolView struct {
	expanded map[string]bool
}

type compactTool struct {
	name string
	desc string
}

func newToolView() *toolView {
	return &toolView{expanded: make(map[string]bool)}
}

func (v *toolView) reset() {
	v.expanded = make(map[string]bool)
}

func (v *toolView) expand(name string) {
	v.expanded[name] = true
}

func (v *toolView) isExpanded(name string) bool {
	return v.expanded[name]
}

func (v *toolView) compact(reg *tool.Registry) []compactTool {
	var r []compactTool
	for _, t := range reg.All() {
		r = append(r, compactTool{name: t.Name(), desc: t.Desc()})
	}
	return r
}

func (v *toolView) tools(reg *tool.Registry) []llm.Tool {
	var r []llm.Tool
	for _, t := range reg.All() {
		if v.isExpanded(t.Name()) {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  t.Schema(),
				},
			})
		} else {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  map[string]any{"type": "object"},
				},
			})
		}
	}
	return r
}

func (v *toolView) expandFromHint(s string) {
	words := strings.Fields(s)
	for _, w := range words {
		if strings.HasPrefix(w, "$") {
			v.expand(strings.TrimPrefix(w, "$"))
		}
	}
}

func (c *Core) execTool(ctx context.Context, tc llm.ToolCall) (string, error) {
	if t := c.reg.Get(tc.Name); t != nil {
		return t.Run(ctx, tc.Args)
	}
	return "", fmt.Errorf("unknown: %s", tc.Name)
}

type cmd struct {
	name string
	args []string
}

func (c *Core) parseCmd(s string) (cmd, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, ",") {
		return cmd{}, false
	}
	parts := strings.Fields(s[1:])
	if len(parts) == 0 {
		return cmd{}, false
	}
	return cmd{name: parts[0], args: parts[1:]}, true
}

func (c *Core) extractCmd(s string) (string, string) {
	lines := strings.Split(s, "\n")
	var cmdLines, otherLines []string
	inCmd := false
	
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, ",") {
			cmdLines = append(cmdLines, trim)
			inCmd = true
		} else if inCmd && trim == "" {
			continue
		} else {
			otherLines = append(otherLines, line)
			inCmd = false
		}
	}
	
	return strings.Join(cmdLines, "\n"), strings.Join(otherLines, "\n")
}

func (c *Core) execCmd(ctx context.Context, cmd cmd) error {
	return nil
}
