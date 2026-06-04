package command

import "context"

type sourceKey struct{}
type noticeSourceKey struct{}
type runContextKey struct{}
type personaKey struct{}
type parentMessageKey struct{}

type MemoryScope struct {
	Kind string
	Key  string
}

type RunContext struct {
	Source     string
	Session    string
	Delivery   string
	Memory     []MemoryScope
	Permission string
}

func WithRunContext(ctx context.Context, rc RunContext) context.Context {
	return context.WithValue(ctx, runContextKey{}, rc)
}

func RunContextFrom(ctx context.Context) (RunContext, bool) {
	if ctx == nil {
		return RunContext{}, false
	}
	v, ok := ctx.Value(runContextKey{}).(RunContext)
	return v, ok
}

func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func SourceFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if rc, ok := RunContextFrom(ctx); ok && rc.Source != "" {
		return rc.Source
	}
	if v, ok := ctx.Value(sourceKey{}).(string); ok {
		return v
	}
	return ""
}

func WithNoticeSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, noticeSourceKey{}, source)
}

func NoticeSourceFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if rc, ok := RunContextFrom(ctx); ok && rc.Delivery != "" {
		return rc.Delivery
	}
	if v, ok := ctx.Value(noticeSourceKey{}).(string); ok {
		return v
	}
	return SourceFrom(ctx)
}

func SessionSourceFrom(ctx context.Context) string {
	if rc, ok := RunContextFrom(ctx); ok && rc.Session != "" {
		return rc.Session
	}
	return SourceFrom(ctx)
}

func MemoryScopesFrom(ctx context.Context) []MemoryScope {
	if rc, ok := RunContextFrom(ctx); ok && len(rc.Memory) > 0 {
		return rc.Memory
	}
	return nil
}

func PermissionFrom(ctx context.Context) string {
	if rc, ok := RunContextFrom(ctx); ok && rc.Permission != "" {
		return rc.Permission
	}
	return "default"
}

func WithPersona(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, personaKey{}, id)
}

func PersonaFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(personaKey{}).(string); ok {
		return v
	}
	return ""
}

func WithParentMessage(ctx context.Context, parentMessageID string) context.Context {
	return context.WithValue(ctx, parentMessageKey{}, parentMessageID)
}

func ParentMessageFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(parentMessageKey{}).(string); ok {
		return v
	}
	return ""
}
