# 🦦 Mink

Minimal AI coding agent. Fast, elegant, extensible.

Mink can run as:
- CLI app
- Telegram bot
- Embeddable Go library

## Quick Start (CLI)

```bash
# 1) build CLI binary
go build -o mink ./cmd/mink

# 2) config
cp config.example.toml ~/.mink/config.toml
# edit ~/.mink/config.toml

# 3) run
./mink
```

## Telegram Mode

```bash
./mink tg -tg <telegram_bot_token>
```

## Library Usage

```go
package main

import (
	"context"
	"log"

	"github.com/abcdlsj/mink"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
)

func main() {
	app, err := mink.New(mink.Options{
		Config: config.Config{
			Provider: "openai",
			APIKey:   "sk-...",
			Model:    "gpt-4o",
			Stream:   true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}

	ch := make(chan bus.Msg, 32)
	app.Subscribe(bus.TypeAssistant, ch)
	defer app.Unsubscribe(bus.TypeAssistant, ch)

	if err := app.Submit("platform:api", "hello from embedded mink"); err != nil {
		log.Fatal(err)
	}

	msg := <-ch
	log.Printf("assistant: %v", msg.Payload)
}
```

## Config

`~/.mink/config.toml`:

```toml
provider = "anthropic"
model = "claude-sonnet-4-20250514"
api_key = "sk-..."
base_url = ""           # custom endpoint
telegram_token = ""     # optional
mode = "tui"            # tui or cli

[headers]
User-Agent = "custom"
```

Flags override config:
```bash
./mink -p openai -m gpt-4o -k sk-...
```

## Commands

- `/new` - new session
- `/branch <name>` - create branch
- `/switch <id>` - switch session
- `/compact [summary]` - compact history

## Extensions

Drop executables to `~/.mink/ext/` or skills to `~/.mink/skills/`.

Auto-reload on change.

## Code Style

Short names, small functions, composition over inheritance.

No over-abstraction. Aesthetics first.

## License

MIT
