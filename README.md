# Mink

Mink is a lightweight AI coding agent for local workflows. It supports interactive CLI usage, Telegram Bot mode, and a daemon for always-on tasks.

The design goal is simple: keep the core small, fast, and easy to extend. Instead of hiding everything behind a heavy framework, Mink focuses on a minimal runtime, straightforward configuration, and a few practical extension points such as skills, external tools, and background jobs.

## Install

```bash
go install github.com/abcdlsj/mink@latest
```

You can also download a binary from [Releases](https://github.com/abcdlsj/mink/releases).

## Quick Start

1. Create a config file:

```bash
mkdir -p ~/.mink
cp config.example.toml ~/.mink/config.toml
```

2. Edit `~/.mink/config.toml` and set at least your model and API key.

3. Start Mink:

```bash
mink
```

See `config.example.toml` for a full example.

## Common Commands

```bash
mink                # start interactive mode
mink tg             # start Telegram Bot mode
mink version        # show version
mink status         # show daemon status
mink reload         # reload daemon config
mink upgrade        # upgrade to latest release
```

You can also override config from flags:

```bash
mink -p openai -m gpt-4o -k <api_key>
```

## Daemon

```bash
make daemon-install
make daemon-status
make daemon-restart
make daemon-uninstall
```

The underlying script is `./scripts/install-mink.sh`, which supports `install`, `start`, `stop`, `restart`, `status`, `reload`, `devbuild`, and `upgrade`.

## Paths

- `~/.mink/config.toml` — main config
- `~/.mink/skills/` — custom skills
- `~/.mink/ext/` — external executable tools
- `~/.mink/SOUL.md` — extra behavior guidance

## License

MIT
