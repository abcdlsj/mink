package command

import "strings"

type EntrypointMode string

const (
	ModeDirect     EntrypointMode = "direct"
	ModeRouted     EntrypointMode = "routed"
	ModeCron       EntrypointMode = "cron"
	ModeBackground EntrypointMode = "background"
)

type SessionStrategy string

const (
	SessionSource  SessionStrategy = "source"
	SessionPersona SessionStrategy = "persona"
)

type DeliveryStrategy string

const (
	DeliverySource DeliveryStrategy = "source"
	DeliveryNotice DeliveryStrategy = "notice"
)

type MentionBehavior string

const (
	MentionLeading MentionBehavior = "leading"
	MentionRouted  MentionBehavior = "routed"
	MentionText    MentionBehavior = "text"
	MentionNone    MentionBehavior = "none"
)

type Entrypoint struct {
	Mode       EntrypointMode
	Session    SessionStrategy
	Delivery   DeliveryStrategy
	Mention    MentionBehavior
	Permission string
}

func EntrypointPolicy(source string) Entrypoint {
	source = strings.TrimSpace(source)
	switch {
	case strings.HasPrefix(source, "cron:"):
		return Entrypoint{Mode: ModeCron, Session: SessionSource, Delivery: DeliveryNotice, Mention: MentionText, Permission: "cron"}
	case strings.HasPrefix(source, "background:"):
		return Entrypoint{Mode: ModeBackground, Session: SessionSource, Delivery: DeliveryNotice, Mention: MentionText, Permission: "default"}
	case strings.HasPrefix(source, "tg:dm:"), strings.HasPrefix(source, "tg:channel:"):
		return Entrypoint{Mode: ModeDirect, Session: SessionSource, Delivery: DeliverySource, Mention: MentionText, Permission: "telegram"}
	case source == "cli":
		return Entrypoint{Mode: ModeDirect, Session: SessionSource, Delivery: DeliverySource, Mention: MentionText, Permission: "default"}
	case source == "desktop", strings.HasPrefix(source, "desktop:channel:"), strings.HasPrefix(source, "desktop:direct:"):
		return Entrypoint{Mode: ModeRouted, Session: SessionPersona, Delivery: DeliverySource, Mention: MentionRouted, Permission: "default"}
	case strings.HasPrefix(source, "desktop:agent:"), strings.HasPrefix(source, "cli:agent:"):
		return Entrypoint{Mode: ModeDirect, Session: SessionPersona, Delivery: DeliverySource, Mention: MentionNone, Permission: "default"}
	default:
		return Entrypoint{Mode: ModeDirect, Session: SessionPersona, Delivery: DeliverySource, Mention: MentionLeading, Permission: "default"}
	}
}

func (p Entrypoint) SessionSource(source, personaID string) string {
	if p.Session == SessionSource {
		return strings.TrimSpace(source)
	}
	return PersonaSessionSource(source, personaID)
}

func PersonaSessionSource(source, personaID string) string {
	source = strings.TrimSpace(source)
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return source
	}
	if source == "" {
		source = "default"
	}
	return source + ":persona:" + personaID
}
