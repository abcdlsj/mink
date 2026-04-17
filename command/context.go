package command

import "context"

type sourceKey struct{}

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
