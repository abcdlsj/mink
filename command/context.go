package command

import "context"

type sourceKey struct{}
type personaKey struct{}

func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

func SourceFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sourceKey{}).(string); ok {
		return v
	}
	return ""
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
