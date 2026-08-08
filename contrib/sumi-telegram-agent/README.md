# sumi-telegram-agent

Telegram bot conversation plugin for
[`sumi-builtin-agent`](../../crates/sumi-builtin-agent). It long-polls the
Telegram Bot API, runs one persistent builtin agent session per chat, and
supports receiving and sending files and images.

This crate is intentionally outside the Sumi workspace: build and run it as an
independent binary.

## Run

```bash
cargo build --release
SUMI_TELEGRAM_TOKEN=<bot-token> \
SUMI_BUILTIN_API_KEY=<provider-key> \
SUMI_BUILTIN_MODEL=<model> \
SUMI_AGENT_HOME=./data \
./target/release/sumi-telegram-agent
```

Environment:

| Variable | Default | Purpose |
| --- | --- | --- |
| `SUMI_TELEGRAM_TOKEN` | required | Telegram bot token from BotFather |
| `SUMI_BUILTIN_API_KEY` | required | OpenAI-compatible API key |
| `SUMI_BUILTIN_MODEL` | required | Model name |
| `SUMI_BUILTIN_API_BASE` | `https://api.openai.com/v1` | OpenAI-compatible endpoint |
| `SUMI_AGENT_HOME` | `data` | Root holding per-chat agent homes and `conversations.json` |
| `SUMI_AGENT_IDENTITY` | `Telegram Agent` | Agent identity in prompts |
| `SUMI_AGENT_ROLE` | Help text | Agent role in prompts |
| `SUMI_PRODUCT_CONTRACT` | built-in | Extra platform rules |
| `SUMI_DRIVER_CONTRACT` | built-in | Tool mechanics rules |
| `SUMI_TURN_TIMEOUT_SECONDS` | `600` | Max seconds per agent turn |

Send `/reset` in any chat to start a fresh conversation.

## Replies, reactions and markdown

- Every bot message replies to the user's original message, including follow-up
  chunks and delivered images.
- While a request is being processed the original message gets a 👀 reaction;
  it becomes ✅ on success, ❌ on failure, and ⏰ on timeout.
- Agent replies are rendered as Telegram HTML from a CommonMark subset: bold,
  italic, strikethrough, inline code, fenced code blocks, links, headings,
  blockquotes, bullet/numbered/task lists, and horizontal rules.
- Inline images render as `[alt]` placeholders in the text and are delivered as
  separate photos: public `https://` URLs are sent by URL, and local paths such
  as `![diagram](workspace/diagram.png)` are read from the agent home and sent
  as photo bytes.

## Memory

Each chat has a persistent agent home with a `memory/` directory. On first use
the runtime provisions `memory/MEMORY.md` with the agent identity and role; the
agent maintains it with the `read`/`write`/`edit` tools and `memory/...` paths.
Every turn includes a memory projection (path, size, sha256, modified time) in
the run context so the agent knows what is stored. `sumi-builtin-agent` exposes
`AgentRuntime::list_memory/read_memory/write_memory` for host applications.

## Reminders

The builtin `reminder` plugin is injected into every conversation and persists
reminders in `<agent-home>/reminders.json`:

- `reminder.set` with `text` and `in_minutes` (1-43200) schedules a one-shot
  reminder; the bot delivers `⏰ Reminder: <text>` when it is due.
- `reminder.list` returns the scheduled reminders as JSON.
- `reminder.cancel` removes one by id.

Reminders survive restarts and are checked every 5 seconds. Due reminders reply
to the message that created them when available.

## Files and images

- Incoming `photo` messages are downloaded and stored under
  `workspace/attachments/<message-id>/`; image bytes are also passed to the
  model as `image_url` data when the endpoint supports vision.
- Incoming `document` messages are downloaded and stored at the same location;
  the model sees the path, name, mime type, and size.
- The agent can deliver files back with the `telegram.send_file` and
  `telegram.send_image` tools, using `workspace/...` or `memory/...` paths.
- Files are capped at 20 MiB in both directions.

## Sandbox

The shell tool requires `sandbox-exec` (macOS) or `bwrap` (Linux) on the host.
No Sumi server or daemon is needed.
