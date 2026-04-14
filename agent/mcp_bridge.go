package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/memory"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
)

type MCPBridge struct {
	mem     *memory.Store
	rt      *rtsqlite.DB
	bus     *bus.Bus
	agentID string
	source  string

	mu sync.Mutex
}

type MCPBridgeConfig struct {
	Memory  *memory.Store
	RT      *rtsqlite.DB
	Bus     *bus.Bus
	AgentID string
	Source  string
}

func NewMCPBridge(cfg MCPBridgeConfig) *MCPBridge {
	return &MCPBridge{
		mem:     cfg.Memory,
		rt:      cfg.RT,
		bus:     cfg.Bus,
		agentID: cfg.AgentID,
		source:  cfg.Source,
	}
}

func (b *MCPBridge) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			b.writeError(w, nil, -32700, "parse error")
			continue
		}

		resp := b.handleRequest(ctx, &req)
		if resp != nil {
			b.writeResponse(w, resp)
		}
	}
	return scanner.Err()
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (b *MCPBridge) handleRequest(ctx context.Context, req *jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return b.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return b.handleToolsList(req)
	case "tools/call":
		return b.handleToolsCall(ctx, req)
	case "ping":
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (b *MCPBridge) handleInitialize(req *jsonRPCRequest) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "mink-bridge",
				"version": "0.1.0",
			},
		},
	}
}

func (b *MCPBridge) writeResponse(w io.Writer, resp *jsonRPCResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}

func (b *MCPBridge) writeError(w io.Writer, id any, code int, message string) {
	b.writeResponse(w, &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}
