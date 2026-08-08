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
