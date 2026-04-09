# P3 RFC: Console UI

## Goal

Define the final UI direction for the Mink console.

This RFC is intentionally UI-first.
It exists to answer one product question clearly:

What should the user actually look at while working with a team?

The final judgment for this RFC is:

- the main content should be the conversation process itself
- team, thread, channel, member, and status data are auxiliary metadata
- runtime/tool/debug detail must never dominate the screen

This RFC should replace earlier transcript-plus-cards or sidebar-first UI directions.
Only the final version should be treated as canonical.

This RFC applies to both:

- normal single-agent TUI
- team mode TUI

The two modes should share one design language.
Team mode is an extension of the normal console, not a different product universe.

## Core Product Judgment

The active Mink console screen is not:

- a debug console
- a runtime monitor
- a page of stacked summary panels
- a sidebar-led layout

The active Mink console screen is:

- a conversation-first workspace
- with a compact metadata layer
- and a stable input surface

The correct layout direction is:

1. header
2. metadata bar
3. conversation main area
4. input

If a sidebar is easy to support later, it can be an enhancement.
It is not the primary layout dependency.

This same layout should also be the base skeleton for normal single-agent mode.

## Non-Goals

- making metadata visually heavier than conversation
- surfacing raw tool/runtime logs as first-class content
- relying on a mandatory sidebar to explain the page
- using oversized summary cards as the page skeleton
- making the user read execution noise to understand state

## UI Principles

- Conversation is the primary surface.
- Metadata is secondary context.
- Summary should support reading, not replace the conversation body.
- System/tool detail must be folded, weakened, or compacted.
- The page should feel like a product workspace, not an internal terminal.
- Color should separate meaning, not decorate empty space.
- Input should stay stable and always remain the final visual anchor.

## Information Hierarchy

The intended hierarchy is:

1. conversation content
2. lightweight metadata state
3. system and execution detail

Within conversation content:

1. leader turns
2. member turns
3. important system events
4. low-level runtime noise

## Main Surfaces

The console should be treated as four stacked surfaces.

### 1. Header

The header should stay compact.

Its job is only to establish:

- product identity
- current team
- current thread or channel
- overall state

Recommended content:

- `MINK CONSOLE`
- team name
- current thread
- overall status

The header should not try to summarize the whole thread.

In normal mode, the header should use the same structure, but with single-agent metadata:

- product identity
- model
- session id
- lightweight token or runtime status

### 2. Metadata Bar

The metadata bar sits directly under the header.

This is the preferred replacement for a mandatory sidebar.
It carries high-value context without taking over the page.

Recommended fields:

- members
- current thread
- channel or scope
- blocker status
- active speaker
- latest summary time

### Metadata Bar Rules

- metadata must be visually lighter than the conversation area
- metadata should be compact, tag-like, and scannable
- metadata should use color coding to separate meaning
- metadata should not be rendered as large stacked boxes
- metadata should not require users to read paragraphs

### Metadata Bar Visual Form

Prefer chips, tags, or compact labeled cells such as:

- `members: 4`
- `thread: review`
- `channel: release-review`
- `blocker: 2`
- `speaker: main`
- `summary: 20:07`

If a field is empty, it should still feel intentional:

- `blocker: none`
- `speaker: idle`

Avoid placeholder-feeling copy such as:

- `No blocker tracked`
- `No active speaker`

### 3. Conversation Main Area

This is the primary surface.

The user should spend most of their attention here.
The page should make that obvious.

The conversation area should show:

- leader messages
- member messages
- compact system updates when they materially affect the work

The conversation area should not default to showing:

- raw shell output blocks
- repeated timeout/status spam
- repeated `still working` lines
- verbose internal reasoning dumps
- every tool invocation in full

### Conversation Rendering

Recommended hierarchy:

- `leader`: strongest visual weight
- `member`: medium visual weight
- `system`: quiet and compact

The key is not to make everything colorful.
The key is to make the reading path obvious.

### Reading Path

When the user opens the screen, they should naturally read:

1. the latest conversation turn
2. the recent exchange that led to it
3. only then the supporting metadata if needed

Not:

1. a wall of metadata
2. a wall of boxes
3. a runtime log

### System and Tool Events

System/tool events should be shown only when they change understanding.

Examples worth showing:

- member invited
- member failed to join
- blocker created
- blocker resolved
- leader changed current plan

Examples that should be folded or weakened:

- repeated wait messages
- repeated timeout retries
- low-level shell chatter
- long tool payloads

In normal mode, the same rule applies:

- assistant output is primary
- user messages are clear
- thinking/tool/runtime detail is secondary
- session bookkeeping should not dominate the transcript

## 4. Input

Input remains docked at the bottom.

Its role is simple:

- stable
- always visible
- clearly addressed to the team

Input should communicate:

- the user is speaking to the current team
- not privately to one agent

The input area should never move around to accommodate metadata noise.

In normal mode, the same bottom-docked input treatment should be preserved.

## Thread View vs. Team Home

This RFC makes one important refinement:

- when the user is actively working inside a thread, the default working view should be conversation-first
- team-level browsing can still exist, but it is not the dominant working surface

That means:

- `Team Home` can exist as a secondary navigation or overview page
- the active work surface should still be the conversation page

If there is a team home, it should not dictate the information hierarchy of the active thread view.

## Normal Mode Console

Normal mode should share the same structural language as team mode:

1. header
2. metadata bar
3. conversation
4. input

The difference is not layout family.
The difference is only metadata richness.

### Normal Mode Metadata

Recommended compact fields:

- model
- session
- token usage or lightweight runtime stats
- mode label if needed

Example:

- `model: arkcoding`
- `session: 20260408195646_949bbc`
- `tokens: ↑531 ↓4.3k`

### Normal Mode Rules

- do not create a separate visual language for ordinary chat
- do not reintroduce oversized empty panels
- do not let the top of the screen become visually dead while content sits low on the page
- do not make `Conversation` a heavy section title unless it adds real value
- keep the reading path identical to team mode: metadata first glance, conversation main, input stable

### Unified Language Across Modes

The user should feel that:

- normal mode is the base console
- team mode is the same console with richer metadata and multi-agent rendering

Not:

- one mode is a chat page
- the other mode is a dashboard

The family resemblance should come from:

- same header style
- same metadata-chip language
- same conversation-first body
- same input behavior
- same restrained palette

## Color Direction

Use a restrained terminal palette with semantic separation.

Recommended direction:

- background: near-black or charcoal
- primary text: cool off-white
- muted text: soft gray
- brand accent: slate blue or desaturated cyan
- thread/channel chip: desaturated blue
- blocker chip: muted orange-red
- active speaker chip: cool cyan or teal
- summary/time chip: cool gray-blue
- system events: low-contrast gray-blue

### Color Rules

- color should separate categories, not create noise
- do not give every element a strong accent
- keep strongest contrast in the conversation area
- metadata chips should read quickly but stay secondary

## Interaction Model

### Scroll

- the main scroll target is the conversation area
- metadata should remain fixed or semi-fixed
- scrolling must preserve terminal copyability

### Focus

The user should feel three stable zones:

1. metadata context
2. conversation
3. input

### Expand/Collapse

Execution-heavy system detail should be collapsible or compact by default.

Recommended behavior:

- show a short system line first
- allow expansion only when the user wants details

Examples:

- `backend-expert invite timed out`
- `tool output collapsed`
- `3 repeated status updates hidden`

### Keyboard

Keep interaction simple:

- conversation scroll keys continue to work
- input remains stable
- metadata does not require complex navigation to be useful

## Screen Skeleton

```text
MINK CONSOLE
release-review  /  review  /  running

[ members: 4 ] [ thread: review ] [ channel: release-review ]
[ blocker: 2 ] [ speaker: main ] [ summary: 20:07 ]

------------------------------------------------------------

Leader: 我先拆解 mink 代码架构，然后组织团队成员并行分析。

Member/backend-expert: 后端部分可以按模块拆成 router / service / storage。

System: llm-expert invite timed out
System: 2 repeated waiting updates hidden

Leader: 先不等 llm-expert，继续推进主线分析。

------------------------------------------------------------

> Message team...
```

This is the intended final direction.

Important properties of this skeleton:

- conversation is the main body
- metadata is compact and secondary
- system noise is compressed
- input is stable

### Normal Mode Skeleton

```text
MINK CONSOLE
arkcoding  /  20260408195646_949bbc  /  running

[ model: arkcoding ] [ session: 20260408195646_949bbc ] [ tokens: ↑531 ↓4.3k ] [ mode: normal ]

------------------------------------------------------------

你: 你好

mink: 你好。

你: 你是什么模型

mink: 我是 mink，运行在你本地机器上的自主助手 agent。
mink: 底层模型由你的 mink 配置决定。

system: thinking hidden

------------------------------------------------------------

» Message...
```

Properties:

- same overall skeleton as team mode
- no giant empty decorative region
- no separate dashboard style
- conversation remains dominant
- thinking/runtime detail is weakened rather than front-loaded

## What Must Change From the Current Direction

- remove large summary-first stacked panels from the top of the page
- stop making `Goal / Best Answer / Blocker / Speaker` equal-sized first-class boxes
- move team/thread/member/channel state into a metadata bar
- make conversation visually dominant again
- reduce visible runtime chatter
- keep summary and blocker information lightweight unless truly important
- align normal TUI and team TUI under one shared visual system
- remove top-heavy emptiness from normal mode
- demote `thinking` and session bookkeeping in normal mode

## Relationship to P2

P2 remains the source of truth for:

- team memory
- thread inheritance
- team/thread/channel mapping
- runtime orchestration

P3 is the source of truth for:

- active working layout
- visual hierarchy
- metadata placement
- conversation prominence
- transcript noise handling

If P2 and P3 conflict on UI layout, P3 should win.

## Implementation Order

1. convert current top summary blocks into a compact metadata bar
2. make conversation the dominant visual surface again
3. weaken or fold runtime/tool/system noise
4. keep input docked and team-addressed
5. add semantic color coding for metadata
6. only then polish spacing, borders, and typography

Do not start from:

- more cards
- more panels
- more runtime detail
- more sidebar structure

## Success Criteria

The UI succeeds if the user can instantly feel:

- this is a team conversation workspace
- the conversation is the main thing to read
- metadata is available but not overwhelming
- the page is not a debug console

And in normal mode:

- this is the same product family
- the conversation remains primary
- model/session info is available without overshadowing the transcript

The UI fails if users still feel they are reading a runtime monitor with some product decoration on top.
