# P4 RFC: Web UI

## Goal

Define the first coherent Web UI for Mink.

This RFC covers:

- team collaboration in the web UI
- normal single-main chat in the web UI
- channel / thread / direct message / agent conversation in one shell
- one shared visual system for all of the above

The main product requirement is:

- team and main chat should feel like the same product
- team mode should not become a separate admin tool
- main chat should not become a watered-down version of the team workspace
- Slack-like collaboration and ChatGPT-like conversation lists should be fused, not split into separate products

## Design Reference Direction

The reference images point to a very specific direction.

This is not:

- generic SaaS dashboard UI
- soft glassmorphism
- bland white cards
- modern rounded-everything chat app

This is:

- high-contrast workspace UI
- editorial / operations-console feel
- strong black borders
- warm paper-like canvas
- bright utility accents
- dense but readable spatial structure

The emotional target is:

- looks operational
- looks opinionated
- looks built for active work
- still feels human and readable

## Product Judgment

The Web UI should be built around one shared shell:

1. workspace rail
2. conversation index pane
3. central conversation workspace
4. optional right context pane
5. bottom composer

The difference between modes is not the shell.
The difference is what the index pane, center pane, and right pane are showing.

This is the key fusion:

- Slack side: channels, threads, DMs, workspace switching
- ChatGPT side: titled conversation list, resumable chat history, multiple parallel conversations with the same counterpart

Mink should support both at once.

That means:

- a channel is a conversation container
- a DM is a conversation container
- an agent chat is also a conversation container
- one agent is allowed to have many parallel conversations
- thread is a focus view inside a conversation container, not a separate product

## Core Principles

- Conversation remains the primary surface.
- Metadata is visible, but secondary.
- Navigation should be always legible.
- Team mode and main mode must share one visual language.
- Container types should differ in payload, not in shell.
- The UI should feel like a collaborative desk, not a floating chatbot.
- Strong borders are allowed, but only when structure stays clear.
- Accent color should signal category and state, not decoration.
- Highlighting should feel like markup, not like status LEDs.

## Unified Information Architecture

The UI should be driven by a simple product model.

### Objects

- `Workspace`: the top-level environment
- `Conversation Container`: a channel, DM, or agent conversation
- `Thread`: a scoped branch of discussion inside a container
- `Message`: a user, agent, or system message
- `Artifact`: code excerpt, citation, document, image, or structured evidence

### Product Consequences

- channels behave like shared containers
- DMs behave like private containers
- agent chats behave like resumable containers with titles, timestamps, and history
- the same agent can own multiple conversation containers at once
- thread is always subordinate to the parent container

This lets the UI unify:

- `#troubleshooting`
- `DM / PM`
- `Claude / RFC rewrite`
- `Codex / Web UI follow-up`

under one consistent navigation model.

## Visual Language

### Palette

Use a controlled, slightly print-like palette:

- page background: warm paper or pale cream
- panel background: very light neutral
- primary text: charcoal or near-black
- border: solid dark graphite
- primary accent: saturated yellow
- secondary accent: hot pink or magenta for current selection
- tertiary accent: desaturated cyan or blue for thread/system context
- blocker/warning accent: orange-red
- muted text: dusty gray-brown

This is intentionally not the CLI palette.
CLI stays darker and terminal-native.
Web can be brighter and more editorial.

### Typography

- display headings: `Barlow Condensed`, fallback `IBM Plex Sans Condensed`
- body UI: `IBM Plex Sans`, fallback `Noto Sans SC`
- monospace: `IBM Plex Mono`, fallback `JetBrains Mono`
- strong, condensed headings are preferred for labels, titles, counts, and workspace identity
- body text should stay highly readable and neutral
- metadata labels can be uppercase or small-caps style
- code, stack traces, and technical excerpts should use monospace blocks

Typography rules:

- use condensed display type for workspace-level chrome, not long paragraphs
- use body font for conversation content, summaries, and thread replies
- use mono only for technical material or diagnostic evidence
- Chinese content should stay on the readable body font, not the condensed display face

Suggested scale:

- workspace title: 24 to 28px / semibold condensed
- pane title: 18 to 20px / semibold condensed
- conversation sender name: 13 to 14px / semibold body
- conversation body: 15 to 16px / regular body
- metadata and timestamps: 12 to 13px / medium body
- code and evidence snippets: 13 to 14px / regular mono

### Borders and Shapes

- use crisp rectangular geometry
- allow hard edges
- avoid pill-heavy consumer-chat styling
- use borders to define structure, not every tiny sub-element

## Shared Shell

Both team mode and main mode should use the same overall structure.

```text
+ Workspace Rail + Conversation Index + Main Workspace + Optional Context Pane
```

### Workspace Rail

The workspace rail is the thinnest, most stable layer on the left.

Its role:

- workspace identity
- global switching
- top-level mode selection
- quick access to search and saved content

Recommended items:

- workspace switcher at the top
- `Inbox`
- `Channels`
- `Direct`
- `Agents`
- `Threads`
- `Saved`
- current user identity at the bottom

This rail should stay visually simple and highly scannable.

### Conversation Index Pane

This is where the Slack-like list and the ChatGPT-like list are fused.

Its role:

- show the currently selected collection
- show recent items with titles and recency
- let users switch between many conversations quickly

Examples:

- under `Channels`: `#all`, `#troubleshooting`, `#mink-ui`
- under `Direct`: `PM`, `Opus-Claude`, `cow`
- under `Agents`: `Codex / Web UI RFC`, `Codex / Bug follow-up`, `Claude / Search cleanup`
- under `Threads`: open or pinned threads across the workspace

Critical rule:

- `Agents` is not just an agent roster
- it is an agent-scoped conversation list
- one agent can have many resumable conversations
- this is the direct bridge to the ChatGPT-style mental model

### Top Workspace Bar

The top bar should establish where the user is.

Examples:

- `troubleshooting — 排查问题的 channel`
- `PM`
- `Thread — #troubleshooting`

Actions can live on the right:

- view in channel
- leave
- thread/member counts
- settings
- close pane

## Team Mode

Team mode should use the shared shell, but the center area becomes a team conversation workspace.

### Center Pane

The center pane should show:

- the active thread or workstream
- message-by-message conversation
- visible ownership / agent attribution
- task state and thread activity

The conversation list should be the main visual body.

### Right Pane

The right pane should be used when thread context matters.

For team mode, it can contain:

- the selected thread conversation
- reference excerpts
- summarized findings
- supporting code or evidence blocks

This pane should feel like “working context”, not a generic sidebar.

### Composer

At the bottom:

- one clear input for the active channel or thread
- optional “As Task” control
- send action

Team mode should make it obvious whether the user is speaking to:

- the whole channel
- a specific thread
- a specific team context

## Agent Conversation Mode

Agent conversation mode is the direct answer to the user's single-main workflow.

It should still use the shared shell.

What changes:

- the conversation index shows titled conversations under one selected agent
- the main pane shows the active conversation
- the right pane is optional and usually lighter

What this enables:

- multiple parallel conversations with `Codex`
- multiple parallel conversations with `Claude`
- resumable history per conversation
- ChatGPT-like titled list without breaking the shared Mink shell

This is important:

- one agent must not map to one fixed DM forever
- one agent is a counterpart namespace
- each conversation under that namespace is its own resumable working thread

## Main Chat Mode

Main single-agent chat should use the same shell and same reading rhythm.

What changes:

- no need for heavy team metadata
- no need for multi-member status
- right pane can be absent by default

What stays the same:

- workspace rail + conversation index
- top workspace bar
- conversation as main surface
- bottom composer
- same border language
- same color system

This is important:

Main chat should not collapse into a completely different “simple chat app”.
It should still feel like Mink.

In practice, the main chat experience can be expressed as:

- `Direct > PM`
- `Agents > Codex > Web UI RFC`
- `Agents > Claude > Search cleanup`

The shell stays the same.
Only the payload changes.

## Conversation Rendering

The conversation area should feel structured and skimmable.

### Message Blocks

Each message block should support:

- avatar or identity marker
- sender name
- role or short descriptor
- time
- message body

For agent-heavy/team-heavy threads, this structure matters more than bubbles.

### Message Styling

- owner / user messages can be visually clean and neutral
- agent messages should be clearly attributable
- system/state lines should be quieter and smaller
- highlighted citations, keywords, and conclusions can use yellow marker treatment

### Technical Content

When showing code or diagnosis:

- use boxed monospace excerpts
- avoid giant raw dumps by default
- highlight key lines, not whole files

## Information Density

The reference images are fairly dense, but not noisy.

That density comes from:

- multiple panes
- strong borders
- clear labels
- compressed metadata

It should not come from:

- too many badges
- too many colors
- too many nested cards
- giant empty padding blocks

The rule is:

- dense structure
- restrained component variety

## Navigation Behavior

The navigation stack should support both collaboration and personal workflow.

### Workspace Rail Behavior

- clicking `Channels` swaps the index pane into a channel list
- clicking `Direct` swaps the index pane into a DM list
- clicking `Agents` swaps the index pane into an agent conversation list
- clicking `Threads` swaps the index pane into a thread list

### Index Pane Behavior

- selecting a channel opens its feed in the main pane
- selecting a DM opens that private conversation in the main pane
- selecting an agent conversation opens that exact titled session
- selecting a thread opens the parent conversation in the main pane and the focused thread in the right pane

### Composer Behavior

The composer must always state scope clearly.

Examples:

- `Message #troubleshooting`
- `Reply in thread`
- `Message PM`
- `Message Codex / Web UI RFC`

The user should never have to guess where a message will land.

## Interaction Model

### Desktop

Desktop is the primary target.

Default behavior:

- workspace rail always visible
- conversation index visible by default
- center pane dominant
- right pane visible when context exists

### Mobile / Narrow Width

On narrow screens:

- collapse the left stack into a drawer
- collapse right pane into a slide-over or tab
- keep conversation full-width
- keep composer anchored at the bottom

The mobile version should preserve the same product identity, not become a generic messenger clone.

## Team vs Main Consistency

This RFC explicitly requires consistency across the two modes.

Shared:

- shell layout
- navigation model
- typography family
- border language
- accent system
- composer treatment

Variable:

- metadata richness
- presence of right context pane
- number of identities in the conversation
- task/thread affordances

In short:

- same shell
- different payload

## Subpages

The first Web UI should explicitly support these subpages.

### 1. Workspace Inbox

Purpose:

- show recent activity across channels, DMs, threads, and agent conversations

Main contents:

- recent items with type markers
- unread count
- latest reply preview
- quick jump to the active container

### 2. Channel View

Purpose:

- shared team collaboration around a topic or domain

Main contents:

- channel header
- feed / tasks toggle
- channel conversation stream
- optional active thread pane
- composer with channel scope

### 3. Thread Focus View

Purpose:

- deep work inside a sub-problem without losing parent context

Main contents:

- thread header
- evidence and excerpts
- summary and key decisions
- reply list

### 4. Direct Message View

Purpose:

- one-to-one or private conversation container

Main contents:

- counterpart identity
- conversation history
- attachments / references when needed
- composer

### 5. Agent Conversation Index

Purpose:

- show multiple resumable conversations under one selected agent

Main contents:

- agent identity block
- titled conversation list
- timestamps and recency
- quick start `New conversation`

### 6. Agent Conversation View

Purpose:

- main single-agent work surface

Main contents:

- selected agent
- selected conversation title
- main dialogue stream
- optional right-side references or working memory
- composer

### 7. Search / Jump Surface

Purpose:

- unify navigation across channels, threads, DMs, and agent conversations

Main contents:

- command-style search
- recent searches
- grouped results by container type
- quick open

## Recommended Layouts

### Team Workspace Layout

```text
+ Workspace Rail
  workspace switcher / inbox / channels / direct / agents / threads / saved

+ Conversation Index
  channel list or thread list depending on current section

+ Center Pane
  active channel feed or task list

+ Right Pane
  active thread conversation and evidence

+ Bottom
  composer for the active context
```

### Agent Conversation Layout

```text
+ Workspace Rail
  workspace switcher / inbox / channels / direct / agents / threads / saved

+ Conversation Index
  multiple titled conversations under one selected agent

+ Center Pane
  active conversation with that agent

+ Optional Right Pane
  references / notes / cited files / memory

+ Bottom
  composer
```

### Main Chat Layout

```text
+ Workspace Rail
  workspace switcher / inbox / channels / direct / agents / threads / saved

+ Conversation Index
  DM list or agent conversation list

+ Center Pane
  main conversation with Mink

+ Optional Right Pane
  references / notes / thread context when needed

+ Bottom
  composer
```

## Low-Fidelity Wireframes

These wireframes are intentionally structural.

They lock:

- shell hierarchy
- pane ownership
- conversation priority
- composer placement

They do not yet lock:

- exact spacing scale
- icon set
- final typography pair
- responsive breakpoints

### Team Workspace Wireframe

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ Building! ▾                                                                                 │
├──────────┬───────────────────┬──────────────────────────────────────┬───────────────────────┤
│ Inbox    │ CHANNELS          │ troubleshooting — 排查问题的 channel  │ Thread — #feed-panic  │
│ Channels◀│ # all             │                                      │ [View in channel]     │
│ Direct   │ # troubleshooting◀│ [CHAT] [TASKS]                       │ [Leave] [3] [×]       │
│ Agents   │ # mink-ui         ├──────────────────────────────────────┤───────────────────────┤
│ Threads  │ # archive         │ lsoooj                               │ 2. 在 Go 侧 DynamicCtx │
│ Saved    │ …                 │ @P41Opus 这里是我们用来排查问题的群组…│ 需要看下当前线上的...  │
│          │                   │                                      │ [代码片段 / 高亮证据]   │
│          │ DIRECT            │ ┌──────────────────────────────────┐ │                       │
│          │ PM                │ │ polymer panic 问题               │ │ P41Opus: 找到了...    │
│          │ Opus-Claude       │ │ [13 replies]                     │ │ lsoooj: 你把新版本..   │
│          │ cow               │ └──────────────────────────────────┘ │                       │
├──────────┴───────────────────┴──────────────────────────────────────┴───────────────────────┤
│ Message #troubleshooting                                                    [As Task] [Send] │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

This page establishes the core Web pattern:

- far left = persistent workspace rail
- left-middle = current collection index
- center = primary conversation workspace
- right = selected thread context and evidence
- bottom = stable composer

Important product judgment:

- the center pane is the main surface
- the right pane supports work, but does not dominate the layout
- the left stack is persistent on desktop because navigation is part of the product, not an overlay afterthought

### Agent Conversation Wireframe

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ Building! ▾                                                                                 │
├──────────┬─────────────────────────────┬────────────────────────────────────┬─────────────────┤
│ Inbox    │ CODEX                        │ Codex — Web UI RFC                │ Context         │
│ Channels │ • Web UI RFC ◀               │                                    │ [refs] [files]  │
│ Direct   │ • Thinking fallback          │ lsoooj: 写个 RFC 设计 Mink 的...   │ p4-web-ui.md    │
│ Agents◀  │ • Bug follow-up              │                                    │ design refs     │
│ Threads  │ • Search cleanup             │ Codex: 我建议统一成 shell +...     │ decision notes  │
│ Saved    │ + New conversation           │                                    │                 │
│          │                              │ [草图缩略图 / 引用块 / 代码卡片]    │                 │
│          │                              │                                    │                 │
│          │                              │ Codex: 这版我会补 3 个子页面...    │                 │
├──────────┴─────────────────────────────┴────────────────────────────────────┴─────────────────┤
│ Message Codex / Web UI RFC                                                         [Send]    │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

This page is the concrete fusion of ChatGPT-style and workspace-style UI.

It keeps:

- the same workspace rail
- the same conversation index pane
- the same workspace bar
- the same conversation rhythm
- the same bottom composer

It removes or weakens:

- heavy team metadata
- mandatory heavy thread context
- multi-member coordination controls

The result should feel like:

- one Mink shell
- one Mink visual language
- different workload density depending on context

### Direct Message Wireframe

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│ Building! ▾                                                                                 │
├──────────┬───────────────────┬───────────────────────────────────────────────────────────────┤
│ Inbox    │ DIRECT            │ PM ● Online                                                  │
│ Channels │ PM ◀              │                                                               │
│ Direct◀  │ Opus-Claude       │ lsoooj: 名字改下，你用 team UI 的话...                      │
│ Agents   │ cow               │                                                               │
│ Threads  │ ...               │ PM: 我会把文档名和标题从...                                  │
│ Saved    │                   │                                                               │
│          │                   │ [图片缩略图 / 引用块 / 文件卡片]                              │
│          │                   │                                                               │
├──────────┴───────────────────┴───────────────────────────────────────────────────────────────┤
│ Message PM                                                                          [Send]   │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

This page proves that:

- DM is just another container type
- it does not need a different product shell
- the user can move from channel to DM to agent conversation without relearning the interface

### Thread Detail Behavior

When the user selects a thread inside a team workspace:

- the right pane opens or becomes active
- the thread title and actions move into that pane header
- evidence, code excerpts, summaries, and follow-up replies stack vertically
- the center pane still owns the main channel feed

This matters because the product should support:

- scanning the broader workspace
- drilling into one thread
- replying without losing the larger context

The web UI should avoid forcing a hard page transition for every thread click.

### Empty and Light-Usage States

The shell should still look usable before a workspace becomes busy.

Good empty states:

- show starter prompts inside the center pane
- show saved threads or recent documents in the right pane
- keep the composer active

Bad empty states:

- giant blank dashboard panels
- decorative cards with no immediate action
- hiding the input until some setup flow is completed

## Visual Tokens

### Color Roles

- paper background: `#F6F1E8`
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

### Component Style Rules

- main panes use strong outer borders
- inner separators use thinner rules
- tags stay rectangular, not round pills
- selection can use filled pink or yellow blocks
- evidence highlights can use marker-like yellow backgrounds
- code blocks should look pasted onto the page, not like floating glass cards

## Responsive Guidance

### Desktop

- workspace rail is persistent
- conversation index is persistent
- right context pane is conditional

### Tablet

- workspace rail stays visible
- conversation index can collapse to a drawer
- right context pane becomes a tab or slide-over

### Mobile

- only the main conversation pane is persistent
- navigation opens from the left
- thread/context opens from the right or from an upper segmented control
- composer remains bottom-anchored

## Do Not Do

- do not make team mode look like a separate admin console
- do not make main chat look like a lightweight consumer chat app
- do not overuse rounded cards
- do not convert every metadata item into a badge
- do not hide navigation completely
- do not flatten the interface into one giant chat column
- do not make the right pane mandatory when no context exists

## Success Criteria

The Web UI succeeds if:

- users can immediately understand where they are
- team and main chat feel like one product family
- the shell feels distinctive and memorable
- conversation remains the main surface
- thread/context work is easier than in a single-column chat UI

The Web UI fails if:

- it looks like a generic AI chat app
- team and main appear unrelated
- the right pane becomes dead weight
- navigation feels hidden or bolted on

## Relationship to P3

`p3-console-ui.md` remains the source of truth for CLI/TUI console behavior.

This RFC is the Web counterpart.

They should share product principles:

- conversation first
- metadata secondary
- one coherent design system across modes

But they should not look identical.

CLI is terminal-native.
Web should embrace pane layout, persistent navigation, and a brighter editorial workspace style.
