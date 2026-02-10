// Package bus provides unified message passing for multi-agent and multi-platform.
//
// Core concepts:
//   - Msg: unified message format with routing
//   - Bus: pub/sub + req/reply message bus
//   - Coordinator: spawn and manage child agents
//   - Platform: adapters for CLI, Telegram, WebUI
//
// Usage:
//
//	b := bus.New()
//	router := bus.NewRouter(b)
//	coord := bus.NewCoordinator(b)
//
//	// Spawn child agent
//	child, _ := coord.Spawn("parent-1", true) // share context
//
//	// Send message
//	b.Pub(bus.Msg{Type: bus.TypeUserInput, Payload: "hello"})
//
//	// Request with reply
//	resp, _ := b.Req(ctx, bus.Msg{Type: bus.TypeToolCall, To: "agent-1"})
//
package bus
