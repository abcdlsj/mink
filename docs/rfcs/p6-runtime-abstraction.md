# P6 RFC: Runtime Abstraction Layer

## Goal

Decouple Mink's agent execution from its orchestration layer so that both native (built-in ReAct loop) and external (Claude Code, Codex, Kimi, etc.) runtimes can be managed through a single interface.

Mink becomes an agent orchestration platform. Execution is pluggable.

## Why This RFC Exists

Today Mink's agent loop (`agent/react.go`) directly calls `llm.Provider`, dispatches tools, writes to session, and publishes bus messages — all in one function. This works well for the native runtime, but makes it impossible to use Claude Code or Codex as execution backends without reimplementing the entire orchestration stack.

The slock daemon project demonstrates a viable pattern: spawn external CLI agents, inject capabilities via MCP bridge, and route messages through a unified protocol. Mink already has stronger primitives (workspace, scoped memory, team collaboration, session persistence) but lacks the runtime abstraction to leverage external execution engines.

The product direction is clear: users should be able to create agents backed by Claude Code or Codex, getting their superior tool use and code editing capabilities, while Mink provides workspace isolation, scoped memory, team collaboration, and a unified Web/CLI/Telegram UI.

## Design

### Runtime Interface

```go
type Runtime interface {
    Start(ctx context.Context, cfg RuntimeConfig) error
    Send(ctx context.Context, input string) error
    Messages() <-chan RuntimeMessage
    Stop() error
    Status() RuntimeStatus
}
```

`RuntimeMessage` is a union type covering:
- assistant text (streaming chunks or complete)
- tool call start / result
- thinking / reasoning
- error
- done (turn complete)

`RuntimeConfig` carries:
- workspace context (id, path)
- session history (for resume)
- system prompt
- available MCP tools (mink-provided capabilities)
- model / provider settings

### Two Implementations

#### NativeRuntime

Wraps the existing `agent/react.go` loop. No behavioral change — just adapts the current `Agent.step()` / `Agent.runSteps()` into the `Runtime` interface.

- `Start`: creates Agent with current deps, begins step loop
- `Send`: injects user message into session, triggers next step cycle
- `Messages`: converts bus events to RuntimeMessage
- `Stop`: cancels context

This is a refactor, not a rewrite. The ReAct loop stays identical.

#### ExternalRuntime

Spawns an external CLI process (claude, codex, etc.) and communicates via MCP bridge.

- `Start`: spawns process with `--mcp-config` pointing to a local MCP bridge server
- `Send`: delivers user input through the MCP bridge
- `Messages`: receives assistant output through the MCP bridge
- `Stop`: sends interrupt / kills process

The MCP bridge exposes mink capabilities as tools:
- `search_memory` / `read_memory` / `write_memory` (scoped)
- `session_context` (workspace/team/thread context)
- `notify` (bus message publishing for UI updates)

The external process's native tools (file editing, bash, etc.) are used directly — mink doesn't intercept or re-implement them.

### Where the Interface Lives

```
agent/
  runtime.go          # Runtime interface + RuntimeMessage types
  runtime_native.go   # NativeRuntime (wraps existing react loop)
  runtime_external.go # ExternalRuntime (spawns CLI process + MCP bridge)
  runtime_bridge.go   # MCP bridge server for external runtimes
  dispatcher.go       # Updated to use Runtime instead of Agent directly
```

### Dispatcher Changes

`Dispatcher` currently creates `Agent` instances directly. After this RFC:

```go
// Before
a := d.deps.newAgent(id, sess, false)
a.Run(ctx, src, input)

// After
rt := d.resolveRuntime(agentConfig)
rt.Start(ctx, cfg)
rt.Send(ctx, input)
for msg := range rt.Messages() {
    d.handleRuntimeMessage(src, msg)
}
```

`handleRuntimeMessage` writes to session, publishes bus events, and updates UI state — the same things that `react.go` does inline today, but now decoupled from execution.

### Session & Message Flow

For NativeRuntime, the flow is unchanged — session writes happen inside the step loop.

For ExternalRuntime:
1. User input → Dispatcher → `rt.Send(input)`
2. External process runs, calls MCP bridge tools as needed
3. External process produces output → MCP bridge → `rt.Messages()` channel
4. Dispatcher reads messages, writes to mink session store, publishes bus events
5. UI receives bus events, renders normally

Session is always owned by mink, never by the external runtime.

### Team Collaboration

`TeamDispatcher` currently orchestrates turns by calling `Agent.Run()` for each speaker. After this RFC, it calls `Runtime.Send()` instead. The turn policy, thread management, and memory injection remain in mink — only execution is delegated.

### Configuration

Agent config gains a `runtime` field:

```toml
[[agents]]
id = "coder"
runtime = "claude"  # "native" | "claude" | "codex"
model = "claude-sonnet-4-20250514"
```

When `runtime` is omitted or `"native"`, the existing ReAct loop is used. When set to an external runtime, the corresponding CLI is spawned.

## What Changes

### Must Change

1. Extract `Runtime` interface from current agent execution path
2. Wrap existing `Agent` as `NativeRuntime`
3. Move session write + bus publish out of `react.go` into dispatcher-level message handling
4. Update `Dispatcher` to use `Runtime` instead of `Agent` directly

### Must Build

1. `ExternalRuntime` — process spawning, lifecycle management
2. MCP bridge server — exposes mink memory/context as tools to external processes
3. Message protocol — parse external runtime output into `RuntimeMessage`

### Does Not Change

- Session store (sqlite)
- Memory system (scoped markdown + sqlite index)
- Workspace model
- Bus architecture
- Web/CLI/Telegram platform adapters
- Tool definitions (for native runtime)
- Team collaboration logic (turn policy, thread management)

## Execution Order

### Phase 1: Extract Runtime Interface

1. Define `Runtime` interface and `RuntimeMessage` types
2. Implement `NativeRuntime` wrapping existing `Agent`
3. Refactor `Dispatcher` to use `Runtime`
4. Verify all existing tests pass — zero behavioral change

### Phase 2: Build External Runtime

1. Implement MCP bridge server exposing memory/context tools
2. Implement `ExternalRuntime` for Claude Code
3. Add process lifecycle management (spawn, health check, restart)
4. Add message parsing (external output → RuntimeMessage)

### Phase 3: Integration

1. Wire external runtime into team collaboration
2. Add runtime selection to agent config
3. Test mixed teams (native + external agents collaborating)
4. Add Codex runtime driver

## Non-Goals

- Replacing the native ReAct loop (it stays as a first-class runtime)
- Building a generic MCP proxy (the bridge is mink-specific)
- Supporting arbitrary external tools in native runtime
- Cross-machine remote execution (future RFC)

## Open Questions

1. Should the MCP bridge run as a sidecar process or in-process goroutine?
   - Sidecar is simpler for external CLI compatibility
   - In-process is lower latency for native runtime
   - Recommendation: sidecar for external, skip for native

2. How should streaming work for external runtimes?
   - Option A: parse stdout chunks (fragile, runtime-specific)
   - Option B: external runtime pushes via MCP bridge (clean, but requires bridge to support streaming)
   - Recommendation: start with B, fall back to A for runtimes that don't support it

3. Should external runtime agents have access to mink's full tool registry?
   - No — they use their own native tools. Mink only injects memory/context/notify via MCP bridge.

## Success Criteria

P6 is successful when:

- existing native agent behavior is unchanged
- a Claude Code process can be spawned as a mink agent
- the spawned agent can read/write scoped memory
- session history from external runtime is persisted in mink's sqlite
- team collaboration works with mixed native + external agents
- Web UI renders external runtime output identically to native
