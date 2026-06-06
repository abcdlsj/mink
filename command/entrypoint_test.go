package command

import "testing"

func TestEntrypointPolicyMatrix(t *testing.T) {
	tests := []struct {
		source     string
		mode       EntrypointMode
		session    SessionStrategy
		delivery   DeliveryStrategy
		mention    MentionBehavior
		permission string
	}{
		{"desktop", ModeRouted, SessionPersona, DeliverySource, MentionRouted, "default"},
		{"desktop:channel:abc", ModeRouted, SessionPersona, DeliverySource, MentionRouted, "default"},
		{"desktop:channel:abc:thread:root", ModeRouted, SessionPersona, DeliverySource, MentionRouted, "default"},
		{"desktop:direct:team", ModeRouted, SessionPersona, DeliverySource, MentionRouted, "default"},
		{"desktop:agent:bob", ModeDirect, SessionPersona, DeliverySource, MentionNone, "default"},
		{"cli:agent:bob", ModeDirect, SessionPersona, DeliverySource, MentionNone, "default"},
		{"cli", ModeDirect, SessionSource, DeliverySource, MentionText, "default"},
		{"cli:channel:bugfix", ModeRouted, SessionPersona, DeliverySource, MentionRouted, "default"},
		{"tg:dm:42", ModeDirect, SessionSource, DeliverySource, MentionText, "telegram"},
		{"tg:channel:42", ModeDirect, SessionSource, DeliverySource, MentionText, "telegram"},
		{"cron:bazaar", ModeCron, SessionSource, DeliveryNotice, MentionText, "cron"},
		{"background:task-123", ModeBackground, SessionSource, DeliveryNotice, MentionText, "default"},
		{"test", ModeDirect, SessionPersona, DeliverySource, MentionLeading, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := EntrypointPolicy(tt.source)
			if got.Mode != tt.mode || got.Session != tt.session || got.Delivery != tt.delivery || got.Mention != tt.mention || got.Permission != tt.permission {
				t.Fatalf("policy = %+v", got)
			}
		})
	}
}

func TestEntrypointPolicySessionSource(t *testing.T) {
	if got := EntrypointPolicy("tg:dm:42").SessionSource("tg:dm:42", "bob"); got != "tg:dm:42" {
		t.Fatalf("telegram session = %q", got)
	}
	if got := EntrypointPolicy("desktop:agent:bob").SessionSource("desktop:agent:bob", "bob"); got != "desktop:agent:bob:persona:bob" {
		t.Fatalf("agent dm session = %q", got)
	}
	if got := PersonaSessionSource("", "bob"); got != "default:persona:bob" {
		t.Fatalf("fallback session = %q", got)
	}
}
