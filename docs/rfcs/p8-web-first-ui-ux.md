# P8 RFC: Web-First UI/UX Design

## Goal

Define the interaction model, information architecture, and visual direction for Mink's Web-first product surface.

This RFC is the product companion to P7 (Technical Architecture). It answers:

- what the user sees and how they navigate
- how the 4 core use cases map to screens and flows
- what the Web shell layout is
- what interaction patterns govern the product
- what the visual direction is

This RFC does not define:

- backend API contracts
- storage schema
- daemon or driver internals
- pixel-level visual specs

## Design Principles

1. Workspace is home. Every interaction happens inside a workspace context.
2. Conversation is the unit of work. Users think in conversations, not sessions or executions.
3. Show collaboration, hide compute. Agent responses stream in naturally. Execution details stay behind a panel.
4. Familiar patterns, not novel UI. Borrow from Slack (channels/threads), ChatGPT (conversation efficiency), and Linear (workspace navigation). Don't invent new paradigms.
5. Progressive disclosure. Start simple, reveal complexity on demand.
6. Keyboard-first, mouse-friendly. Power users navigate without touching the mouse. Casual users never feel lost.

## Information Architecture

```
Workspace
├── Home (default entry)
│   ├── Recent Conversations
│   ├── Active Channels
│   ├── Open Threads
│   ├── Inbox (attention-needed filter)
│   └── Pinned Items
├── DMs
│   ├── Agent DM (1:1 with an agent)
│   └── User DM (future: 1:1 with another user)
├── Channels (containers)
│   ├── #channel-name (container)
│   │   ├── Active Conversation (default view)
│   │   ├── Past Conversations (list)
│   │   └── Threads (branched from conversations)
│   └── #another-channel
├── Agents
│   ├── Agent Profile & Config
│   └── Agent Memory
└── Settings
    ├── Workspace Settings
    ├── Machine Management
    └── Memory Management
```

Rules:

- Workspace Home is the default landing page, not an empty chat window
- Inbox is a Home module (filtered by unread / mentioned / waiting-on-you), not a separate product root
- Channel is a container, not the work unit. The main feed inside a channel is always a conversation view. A channel may surface one active conversation by default, but the domain model remains container → conversation → thread. DMs are also containers.
- Channels and DMs are both containers, listed in the sidebar
- A container can hold many conversations over time. Unread, last activity, search results, and deep links all resolve to conversation, not container.
- Threads are always subordinate to a conversation, never top-level navigation

## Web Shell Layout

Three-pane layout with a persistent sidebar.

```
┌──────────────────────────────────────────────────────────┐
│  Top Bar                                                 │
│  [Workspace ▾] [Search ⌘K] [Breadcrumb] [Machine ●]  [⚙]│
├────────┬─────────────────────────────┬───────────────────┤
│        │                             │                   │
│  Left  │     Center                  │   Right           │
│  Side  │     Main Feed               │   Context Pane    │
│  bar   │                             │                   │
│        │                             │                   │
│  Nav   │     Conversation /          │   Thread /        │
│        │     Channel Feed /          │   Artifacts /     │
│        │     Home                    │   Memory /        │
│        │                             │   Run Details     │
│        │                             │                   │
│        ├─────────────────────────────┤                   │
│        │  Composer                   │                   │
│        │  [Model ▾] [Agent ▾] [+]   │                   │
├────────┴─────────────────────────────┴───────────────────┘
```

No bottom status bar. Machine connection and execution state are surfaced in the top bar as compact indicators (dot + label on hover). Token usage appears inline on completed message turns.

### Left Sidebar (240px, collapsible)

Top section:
- Workspace switcher (dropdown or icon grid)
- Quick actions: New Conversation, New Channel

Navigation sections:
- Home (always first)
- DMs (grouped, sorted by recent activity)
- Channels (grouped, sorted by recent activity)
- Agents (list of workspace agents)

Each item shows:
- Name / title
- Unread indicator (dot or count badge)
- Presence indicator for agents (online / idle / offline)
- Last activity preview (one line, truncated)

Bottom section:
- Settings

### Center Pane (flexible width, min 480px)

Displays the active view:

- Home: card grid of recent conversations, active channels, attention items
- Conversation: message feed with streaming, tool calls, artifacts inline
- Channel (container): shows the active conversation by default. User can switch to past conversations within the same channel via a conversation list.

The center pane always has a composer at the bottom when viewing a conversation or channel.

### Right Context Pane (320px, collapsible, hidden by default)

Opens on demand for:

- Thread view (click a thread indicator on a message)
- Artifact detail (click an artifact card)
- Memory browser (view/edit workspace or agent memory)
- Run details / diagnostics (click execution status)
- Agent profile (click agent avatar)

Only one context pane is open at a time. Clicking a different trigger replaces the content.

### Top Bar

- Workspace switcher (left)
- Global search (⌘K) — searches conversations, messages, agents, memory, artifacts
- Breadcrumb: Workspace > Container > Conversation (> Thread)
- Machine status: compact dot indicator (green/yellow/red) with label on hover
- Execution status: shows "running" / "idle" next to machine dot when active
- User avatar + settings (right)

## Core User Flows

### Flow 1: Workspace Single Chat

User opens a 1:1 conversation with an agent to solve a problem (debug a server, configure a package, review code).

```
1. User lands on Workspace Home
2. Clicks "New Conversation" or selects an existing DM
3. Selects model from composer dropdown (or uses agent default)
4. Types message, sends
5. Agent response streams in the center pane
6. Tool calls appear as collapsible cards inline:
   ┌─ tool: shell — ls -la /etc/nginx/
   │  [output preview, expandable]
   └─ completed ✓
7. User continues conversation, context is preserved
8. User can branch into a thread for a sub-problem
9. User can resume this conversation later from Home or DM list
```

Key interactions:
- Model selector in composer (persistent per conversation, changeable mid-conversation)
- Tool call cards are collapsed by default, expandable on click
- Streaming text appears character-by-character with a cursor indicator
- Conversation auto-saves, no explicit save action needed

### Flow 2: Multi-Agent Group Discussion

User creates a channel with multiple agents to discuss a problem collaboratively.

```
1. User creates a new channel: "infra-migration"
2. Adds agents: @devops-agent, @security-agent, @architect-agent
3. User posts a question or task
4. Agents respond based on their roles and memory
5. User can @mention a specific agent to direct a question
6. Agents can reference each other's outputs
7. Thread branches for focused sub-discussions
```

Key interactions:
- Agent avatars and role labels distinguish participants
- @mention autocomplete in composer (⌘+@ or just @)
- Each agent's messages have a subtle role indicator (e.g., colored left border or role badge)
- Channel topic displayed below channel name in header

### Flow 3: Agent DM

User has a private 1:1 conversation with a specific agent.

```
1. User clicks an agent in the sidebar DM section
2. Opens a DM container with that agent
3. Conversation history is preserved across sessions
4. Agent has its own memory scope (remembers past interactions)
5. User can start new conversations within the same DM
```

Key interactions:
- DM list shows agent name, avatar, presence, last message preview
- Agent memory is accessible via right pane (click agent avatar → memory tab)
- Conversation list within a DM (if multiple conversations exist)

### Flow 4: Channel + Thread Collaboration

User works in a channel and branches into threads for focused sub-problems.

```
1. User is in #backend-refactor channel
2. Sees ongoing conversation in the main feed
3. Clicks "Reply in thread" on a specific message
4. Right pane opens with thread view
5. Thread has its own context isolation
6. Thread scratchpad memory is available
7. Thread can be resolved/closed when sub-problem is done
8. Main channel shows thread indicator: "3 replies"
```

Key interactions:
- Thread indicator on messages (reply count + participant avatars)
- Thread opens in right pane, not a new page
- Thread has its own composer
- Thread can be "resolved" (visual indicator, still accessible)
- Thread scratchpad memory auto-clears on resolve (configurable)

## Key Components

### Message Bubble

```
┌──────────────────────────────────────┐
│ [Avatar] Agent Name · role · 2m ago  │
├──────────────────────────────────────┤
│                                      │
│  Message content with **markdown**   │
│  rendering and `code` support.       │
│                                      │
│  ┌─ tool: read_file                 │
│  │  path: src/main.go               │
│  │  [> Show output]                 │
│  └──────────────────────────────────│
│                                      │
│  ┌─ artifact: config.yaml           │
│  │  [Preview] [Open in pane]        │
│  └──────────────────────────────────│
│                                      │
│  [Reply in thread] [Pin]             │
│  [Save to memory] [... More]         │
└──────────────────────────────────────┘
```

Rules:
- User messages align right (or have distinct background), agent messages align left
- Tool calls are collapsible cards within the message flow
- Artifacts are clickable cards that open in the right pane
- Action buttons appear on hover, not always visible
- Streaming messages show a pulsing cursor at the end

### Composer

```
┌──────────────────────────────────────────────┐
│ [Claude 4 ▾] [Agent: default ▾]             │
├──────────────────────────────────────────────┤
│                                              │
│  Type a message...                    [Send] │
│                                              │
│  [Attach] [Memory] [⌘↵ Send]                │
└──────────────────────────────────────────────┘
```

Features:
- Multi-line input (auto-expand, Shift+Enter for newline, Enter or ⌘+Enter to send)
- Model selector dropdown (persists per conversation)
- Agent selector (in channels with multiple agents, choose who to address)
- File attachment
- Memory reference (insert from workspace/agent memory)
- Slash commands for power users (/thread, /memory, /model)

### Workspace Home

```
┌──────────────────────────────────────────────┐
│  Welcome back, lsoooj                        │
│                                              │
│  ┌─ Inbox (3 items need attention) ────────┐ │
│  │  @devops-agent replied in #infra-mig... │ │
│  │  @security-agent mentioned you in ...   │ │
│  │  Thread resolved in #backend-refactor   │ │
│  └─────────────────────────────────────────┘ │
│                                              │
│  ┌─ Recent Conversations ──────────────────┐ │
│  │  Debug nginx config · 5m ago            │ │
│  │  Code review: auth module · 2h ago      │ │
│  │  Server migration plan · yesterday      │ │
│  └─────────────────────────────────────────┘ │
│                                              │
│  ┌─ Active Channels ──────────────────────┐  │
│  │  #infra-migration (3 unread)           │  │
│  │  #backend-refactor (1 thread active)   │  │
│  └────────────────────────────────────────┘  │
│                                              │
│  ┌─ Agents ───────────────────────────────┐  │
│  │  ● devops-agent · idle                 │  │
│  │  ● security-agent · running task       │  │
│  │  ○ architect-agent · offline           │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
```

### Execution Status (Progressive Disclosure)

Execution details are not shown by default. They surface through:

Level 1 — Top bar indicator (always visible):
- Machine connection status (colored dot: green/yellow/red)
- Current execution state label on hover (idle / running / error)

Level 2 — Inline indicators (in message flow):
- Tool call cards with status
- "Agent is thinking..." indicator
- Token usage on completed turns (subtle, muted text)

Level 3 — Run details pane (on demand):
- Click execution indicator in top bar → right pane shows:
  - Driver (custom / claude / codex)
  - Machine
  - Duration
  - Token usage breakdown
  - Error details if failed
  - Full tool call history

## Navigation Model

### Keyboard Shortcuts

| Shortcut | Action |
|----------|--------|
| ⌘K | Global search |
| ⌘N | New conversation |
| ⌘[ / ⌘] | Navigate back / forward |
| ⌘1-9 | Jump to sidebar item by position |
| ⌘Shift+T | Open thread pane |
| ⌘Shift+M | Open memory pane |
| ⌘. | Toggle right pane |
| Esc | Close right pane / cancel |
| ↑ (in empty composer) | Edit last message |

### URL Structure

Aligned with P7 technical RFC:

- `/w/:workspaceId` → Workspace Home
- `/w/:workspaceId/c/:containerId/v/:conversationId` → Conversation view
- `/w/:workspaceId/c/:containerId/v/:conversationId/t/:threadId` → Thread focused view
- `/w/:workspaceId/agents/:agentId` → Agent profile

Rules:
- Every view is deep-linkable and shareable
- Browser back/forward works naturally
- Thread URLs open the right pane, not a new page

## Visual Direction

### Tone

- Professional workspace, not playful chat app
- Clean, low-noise, high-density information display
- Dark mode as default (developers), light mode available
- Monospace for code, system font for UI text

### Iconography

- Use a single SVG icon library: Lucide (preferred) or Heroicons
- All icons render at consistent size via fixed viewBox (20x20 or 24x24)
- No emoji as UI icons. No colorful icon sets. Icons are monochrome, inheriting text color
- Presence and status use simple geometric shapes: filled circle (●) for online, hollow circle (○) for offline, half-filled or pulsing for transitional states
- Brand logos use official SVGs from Simple Icons when needed
- Hover states use color or opacity transitions, never layout-shifting scale transforms

### Color System

- Neutral base: dark grays for background, light grays for text
- Accent: single brand color for interactive elements and focus states
- Agent identity: each agent gets a consistent color (auto-assigned from palette, overridable)
- Status colors: green (online/success), yellow (running/warning), red (error/offline), gray (idle/neutral)
- Semantic only: no decorative color, every color carries meaning
- Dark mode: ensure text contrast against dark backgrounds, borders remain visible (e.g., subtle border-gray-700)
- Light mode: opaque card backgrounds (e.g., bg-white/80), primary text at high contrast, muted text at minimum AA contrast
- Both modes must pass WCAG AA contrast checks before shipping

### Typography

- UI text: system font stack (-apple-system, system-ui, sans-serif)
- Code and terminal output: monospace (JetBrains Mono, Fira Code, or system monospace)
- Message text: 14px base, comfortable line height (1.6)
- Compact mode option: 13px base, tighter spacing for power users

### Spacing and Density

- Default: comfortable spacing for readability
- Compact mode: tighter spacing, smaller avatars, condensed message layout
- User preference, persisted per workspace

### Motion

- Minimal, functional animation only
- Micro-interactions: 150-300ms duration range
- Only animate `transform` and `opacity` properties for performance. Never animate `width`, `height`, or `top/left`
- Message streaming: character-by-character with cursor
- Pane transitions: translateX slide (200ms ease-out)
- Tool call expand/collapse: height via max-height transition (150ms)
- Respect `prefers-reduced-motion`: disable all non-essential animation when set
- Loading states: use skeleton screens for content areas, subtle spinner for actions
- No bouncing, no confetti, no gratuitous motion

## Responsive Behavior

### Desktop (>1200px)

Full three-pane layout as described above.

### Tablet (768px-1200px)

- Left sidebar collapses to icon-only rail (48px)
- Right pane overlays center pane (slide-over)
- Expand sidebar on hover or click

### Mobile (<768px)

- Single pane navigation (stack-based)
- Sidebar → full screen navigation
- Conversation → full screen feed
- Thread → full screen (push navigation)
- Bottom tab bar: Home, DMs, Channels, Agents

Mobile is not P1 priority but the layout should not prevent it.

## Accessibility

- All interactive elements keyboard-accessible
- ARIA labels on custom components
- Focus management on pane transitions
- Screen reader support for message streaming (aria-live regions)
- Sufficient color contrast (WCAG AA minimum)
- Reduced motion preference respected

## What This RFC Does Not Cover

- Component library implementation details
- Design token values (exact hex codes, pixel values)
- Animation specifications
- Backend API shape
- State management implementation
- Auth flow UI (login, signup, OAuth)

These belong in implementation specs, not this product-level RFC.

## Success Criteria

This RFC is successful when:

- A developer can read it and build the Web shell without ambiguity about layout or navigation
- The 4 core use cases each have a clear, complete flow from entry to completion
- No internal execution concept (session, driver, runtime) appears in the user-facing design
- The information architecture maps cleanly to the P7 domain model
- A new user can understand where they are and what they can do within 10 seconds of landing
