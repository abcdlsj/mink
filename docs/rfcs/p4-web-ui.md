# P4 RFC: Web UI

## Goal

Define the first coherent Web UI for Mink.

This RFC fixes the product model for Web and defines the implementation boundary for the first release so the design matches the current runtime instead of inventing unsupported objects.

## Product Judgment

Mink Web should be built around one shared shell:

1. workspace rail
2. conversation index pane
3. central conversation workspace
4. optional right context pane
5. bottom composer

The shell stays stable.
What changes between modes is the payload inside the index pane, center pane, and right pane.

The intended product direction is still a fusion of:

- Slack-like collaboration
- ChatGPT-like resumable conversation lists

But the first release must only ship what the current Mink runtime can actually persist and route.

## Core Principles

- Conversation remains the primary surface.
- Metadata is visible, but secondary.
- Navigation stays persistent and legible on desktop.
- Team mode and main mode share one visual language.
- Strong borders are allowed, but only when structure stays clear.
- Accent color signals category and state, not decoration.
- The UI should feel like a collaborative work desk, not a floating chatbot.

## Product Model

### Objects

- `Workspace`: the top-level environment
- `Team`: the long-lived collaborative unit with members, identity, and history
- `Conversation`: the primary resumable work surface
- `Thread`: a scoped branch or workstream inside a conversation
- `Message`: a user, agent, or system message
- `Artifact`: code excerpt, citation, document, image, or structured evidence

### Object Rules

- a team is not a thread
- a conversation is not a thread
- a thread is always subordinate to a conversation
- a future agent namespace can own many conversations
- those conversations are still not threads

### Current Runtime Mapping

The current Mink runtime already has:

- resumable main sessions
- persistent teams
- persistent team threads

The current Mink runtime does not yet have first-class:

- channel objects
- DM objects
- agent-scoped conversation namespaces with many titled conversations

So the first Web release must map to the current runtime honestly.

## P1 Scope

The first Web release supports:

- `Main Conversations`: resumable single-main sessions
- `Teams`
- `Team Threads`
- one shared shell across those modes

The first Web release does not need to implement:

- full Slack-like channels
- full DMs
- full agent conversation namespaces

Those remain valid long-term shell directions, but they are follow-up work after the runtime object model exists.

## Visual Direction

This is not:

- generic SaaS dashboard UI
- soft glassmorphism
- rounded-everything consumer chat

This is:

- high-contrast workspace UI
- editorial / operations-console feel
- warm paper-like canvas
- strong black or graphite borders
- dense but readable structure

### Palette

- page background: `#F6F1E8`
- panel background: `#FBF8F2`
- border ink: `#1E1B18`
- body text: `#201C18`
- muted text: `#7A6F64`
- active yellow: `#F5D90A`
- focus pink: `#FF5FA2`
- context blue: `#87B6FF`
- blocker orange: `#FF7A45`
- success green: `#7CCB5E`
- code surface: `#16181D`

### Typography

- display headings: `Barlow Condensed`, fallback `IBM Plex Sans Condensed`
- body UI: `IBM Plex Sans`, fallback `Noto Sans SC`
- monospace: `IBM Plex Mono`, fallback `JetBrains Mono`

Rules:

- condensed display type is for shell chrome, counts, pane titles, and labels
- body font is for conversation content and summaries
- mono is only for technical evidence
- Chinese content stays on the body font

## Shared Shell

```text
+ Workspace Rail + Conversation Index + Main Workspace + Optional Context Pane
```

### Workspace Rail

Long-term shell categories can include:

- `Inbox`
- `Channels`
- `Direct`
- `Agents`
- `Threads`
- `Saved`

P1 ships only the categories backed by real runtime objects:

- `Inbox`
- `Main`
- `Teams`
- `Threads`

### Conversation Index Pane

The index pane shows the selected collection and recent items.

P1 examples:

- under `Main`: resumable main sessions
- under `Teams`: teams and the selected team's thread list
- under `Threads`: recent team threads across all teams
- under `Inbox`: recent activity across main sessions and team threads

### Top Bar

The top bar establishes where the user is.

Examples:

- `Main — Session 20260409...`
- `release-review`
- `release-review — feed-panic`

Actions can live on the right:

- new conversation
- new session
- open thread
- settings

## Main Mode

Main mode is the Web version of single-main chat.

### Center Pane

- active main session conversation
- message-by-message transcript
- user / assistant / system identity

### Right Pane

- optional session metadata
- context anchor summary
- working notes

### Composer

- one clear input for the active main session
- label must state scope clearly

Example:

- `Message Main Session`

## Team Mode

Team mode uses the shared shell, but the center pane becomes a team thread workspace.

### P1 Default

This RFC explicitly chooses `thread-first` for the first Web implementation.

That means:

- center pane = active team thread conversation
- right pane = supporting context for that thread
- index pane = team list plus the selected team's threads

P1 does not use `channel feed in center + thread detail on right` as the default interaction model because the current runtime has teams and team threads, not full channel feeds.

### Center Pane

- active team thread conversation
- visible ownership / agent attribution
- task state and thread activity
- conversation remains the main visual body

### Right Pane

- summarized findings
- member list and ownership
- evidence blocks
- related context

### Composer

- one clear input for the active team thread
- optional `As Task` control
- send action

Example:

- `Message release-review / feed-panic`

## Thread Behavior

When the user selects a thread inside a team workspace:

- the center pane switches to that thread's conversation
- the right pane updates with summary, evidence, and related context
- the selected team remains visible in the index pane

The UI should support:

- scanning the broader workspace
- drilling into one thread
- replying without losing the larger team context

## Inbox

The inbox is a cross-workspace activity surface.

P1 contents:

- recent main sessions
- recent team threads
- latest activity preview
- quick jump into the active conversation

## Long-Term Modes

The following remain valid future extensions of the same shell:

- channel containers
- DM containers
- agent-scoped conversation namespaces

When those land, they should map into the shell as new conversation collections, not as a separate product.

## Wireframes

### Team Workspace

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ Building! ▾                                                                                 │
├──────────┬──────────────────────────┬──────────────────────────────────────┬────────────────┤
│ Inbox    │ TEAMS                    │ feed-panic — active thread          │ Context        │
│ Main     │ release-review ◀         │                                      │ team summary   │
│ Teams◀   │ bug-triage               │ lsoooj                               │ members        │
│ Threads  │                          │ @P41Opus 这里是我们用来排查问题的... │ evidence       │
│          │ THREADS                  │                                      │ related items  │
│          │ feed-panic ◀             │ P41Opus: 找到了 panic 点...          │                │
│          │ release-risk             │                                      │                │
│          │ rollout-check            │ lsoooj: 你把新版本日志贴一下         │                │
├──────────┴──────────────────────────┴──────────────────────────────────────┴────────────────┤
│ Message release-review / feed-panic                                         [As Task][Send]│
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Main Conversation

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ Building! ▾                                                                                 │
├──────────┬──────────────────────────┬──────────────────────────────────────┬────────────────┤
│ Inbox    │ MAIN                     │ Main — Session 20260409...           │ Context        │
│ Main◀    │ Session 20260409... ◀    │                                      │ summary        │
│ Teams    │ Session 20260408...      │ lsoooj: 写个 RFC 设计 Web UI         │ notes          │
│ Threads  │ Session 20260407...      │                                      │ files          │
│          │ + New session            │ Mink: 我建议先把对象模型定死...      │                │
├──────────┴──────────────────────────┴──────────────────────────────────────┴────────────────┤
│ Message Main Session                                                           [Send]       │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

## Interaction Rules

- desktop keeps rail and index visible
- right pane is conditional, not mandatory
- mobile can collapse rail and right pane into drawers
- composer is always anchored and always states scope clearly

## Do Not Do

- do not make team mode look like a separate admin tool
- do not fake unsupported objects just to satisfy the shell
- do not flatten everything into one giant chat column
- do not make metadata heavier than conversation
- do not make the right pane mandatory when no context exists

## Success Criteria

The first Web UI succeeds if:

- users immediately understand where they are
- main chat and team thread work feel like one product family
- the shell feels distinctive and opinionated
- the active conversation remains the main surface
- the first implementation maps cleanly to the current runtime

The first Web UI fails if:

- it pretends unsupported channel/DM models already exist
- center-pane ownership is ambiguous
- team and main look like unrelated products
- the right pane becomes dead weight

## Relationship to P3

`p3-console-ui.md` remains the source of truth for CLI/TUI behavior.

This RFC is the Web counterpart.

They share product principles:

- conversation first
- metadata secondary
- one coherent design system across modes

They do not need to look identical.

CLI stays terminal-native.
Web should embrace pane layout, persistent navigation, and a brighter editorial workspace style.
