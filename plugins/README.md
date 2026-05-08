# Plugins

Plugins are registration functions.

```go
type Plugin func(*app.App) error
```

Keep them small:

- register a runtime
- register a tool
- register a command
- register a service
- register an entrypoint

Current runtime plugins:

- `plugins/claude`
- `plugins/codex`
- `plugins/external`

`plugins/external` is a thin adapter for line-oriented JSON agent CLIs. Provider-specific parsing belongs in the leaf plugin, not in core.

Current feature plugins:

- `plugins/background`
- `plugins/collab`
- `plugins/cron`
- `plugins/memory`
- `plugins/search`
- `plugins/sessioncmd`
- `plugins/telegram`
- `plugins/web`
