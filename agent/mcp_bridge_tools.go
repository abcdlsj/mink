package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
)

func (b *MCPBridge) handleToolsList(req *jsonRPCRequest) *jsonRPCResponse {
	tools := []map[string]any{
		{
			"name":        "search_memory",
			"description": "Search mink memory across scoped notes.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":      map[string]string{"type": "string", "description": "Search query"},
					"scope_kind": map[string]string{"type": "string", "description": "Scope kind: global, workspace, team, agent, channel"},
					"scope_key":  map[string]string{"type": "string", "description": "Scope key"},
					"limit":      map[string]any{"type": "integer", "description": "Max results (default 5)"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "read_memory",
			"description": "Read recent memory docs from a scope.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope_kind": map[string]string{"type": "string", "description": "Scope kind: global, workspace, team, agent, channel"},
					"scope_key":  map[string]string{"type": "string", "description": "Scope key"},
					"limit":      map[string]any{"type": "integer", "description": "Max docs (default 3)"},
				},
			},
		},
		{
			"name":        "write_memory",
			"description": "Write a memory doc to a scope.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope_kind": map[string]string{"type": "string", "description": "Scope kind"},
					"scope_key":  map[string]string{"type": "string", "description": "Scope key"},
					"title":      map[string]string{"type": "string", "description": "Doc title"},
					"body":       map[string]string{"type": "string", "description": "Doc body"},
					"summary":    map[string]string{"type": "string", "description": "Optional summary"},
					"kind":       map[string]string{"type": "string", "description": "Doc kind (default: note)"},
				},
				"required": []string{"title", "body"},
			},
		},
		{
			"name":        "session_context",
			"description": "Get current session context: agent ID, source, workspace info.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "notify",
			"description": "Send a notification message to the mink bus (visible in UI).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]string{"type": "string", "description": "Notification message"},
				},
				"required": []string{"message"},
			},
		},
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": tools},
	}
}

func (b *MCPBridge) handleToolsCall(ctx context.Context, req *jsonRPCRequest) *jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	result, err := b.callTool(ctx, params.Name, params.Arguments)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "error: " + err.Error()},
				},
				"isError": true,
			},
		}
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": result},
			},
		},
	}
}

func (b *MCPBridge) callTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	switch name {
	case "search_memory":
		return b.toolSearchMemory(ctx, args)
	case "read_memory":
		return b.toolReadMemory(ctx, args)
	case "write_memory":
		return b.toolWriteMemory(ctx, args)
	case "session_context":
		return b.toolSessionContext(ctx)
	case "notify":
		return b.toolNotify(ctx, args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (b *MCPBridge) toolSearchMemory(ctx context.Context, args json.RawMessage) (string, error) {
	if b.mem == nil {
		return "memory store not available", nil
	}
	var params struct {
		Query     string `json:"query"`
		ScopeKind string `json:"scope_kind"`
		ScopeKey  string `json:"scope_key"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 5
	}
	var scopes []memory.Scope
	if params.ScopeKind != "" {
		scopes = []memory.Scope{{Kind: params.ScopeKind, Key: params.ScopeKey}}
	}
	docs, err := b.mem.SearchScoped(ctx, scopes, params.Query, limit)
	if err != nil {
		return "", err
	}
	return formatDocs(docs), nil
}

func (b *MCPBridge) toolReadMemory(ctx context.Context, args json.RawMessage) (string, error) {
	if b.mem == nil {
		return "memory store not available", nil
	}
	var params struct {
		ScopeKind string `json:"scope_kind"`
		ScopeKey  string `json:"scope_key"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 3
	}
	scope := memory.GlobalScope()
	if params.ScopeKind != "" {
		scope = memory.Scope{Kind: params.ScopeKind, Key: params.ScopeKey}
	} else if b.agentID != "" {
		scope = memory.AgentScope(b.agentID)
	}
	docs, err := b.mem.RecentByScope(ctx, scope, limit)
	if err != nil {
		return "", err
	}
	return formatDocs(docs), nil
}

func (b *MCPBridge) toolWriteMemory(ctx context.Context, args json.RawMessage) (string, error) {
	if b.mem == nil {
		return "", fmt.Errorf("memory store not available")
	}
	var params struct {
		ScopeKind string `json:"scope_kind"`
		ScopeKey  string `json:"scope_key"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		Summary   string `json:"summary"`
		Kind      string `json:"kind"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	scope := memory.AgentScope(b.agentID)
	if params.ScopeKind != "" {
		scope = memory.Scope{Kind: params.ScopeKind, Key: params.ScopeKey}
	}
	doc := memory.Doc{
		Title:   params.Title,
		Body:    params.Body,
		Summary: params.Summary,
		Kind:    params.Kind,
		Source:  b.source,
	}
	saved, err := b.mem.PutScoped(ctx, scope, doc)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved memory doc %s in scope %s", saved.ID, scope.String()), nil
}

func (b *MCPBridge) toolSessionContext(_ context.Context) (string, error) {
	info := map[string]string{
		"agent_id": b.agentID,
		"source":   b.source,
	}
	if b.rt != nil {
		info["workspace_id"] = b.rt.WorkspaceID()
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	return string(data), nil
}

func (b *MCPBridge) toolNotify(_ context.Context, args json.RawMessage) (string, error) {
	if b.bus == nil {
		return "", fmt.Errorf("bus not available")
	}
	var params struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Message == "" {
		return "", fmt.Errorf("message is required")
	}
	_ = b.bus.Pub(bus.Msg{
		Type:    bus.TypeAssistant,
		From:    b.agentID,
		To:      b.source,
		Payload: params.Message,
	})
	return "notification sent", nil
}

func formatDocs(docs []memory.Doc) string {
	if len(docs) == 0 {
		return "no memory docs found"
	}
	var sb strings.Builder
	for i, doc := range docs {
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, doc.Title)
		fmt.Fprintf(&sb, "  id: %s  scope: %s\n", doc.ID, doc.Scope.String())
		if doc.Summary != "" {
			fmt.Fprintf(&sb, "  summary: %s\n", doc.Summary)
		}
		if doc.Body != "" {
			body := doc.Body
			if len(body) > 1200 {
				body = body[:1200] + "..."
			}
			fmt.Fprintf(&sb, "  body:\n%s\n", body)
		}
	}
	return sb.String()
}
