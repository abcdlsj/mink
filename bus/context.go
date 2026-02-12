package bus

import "context"

type sourceCtxKey string

const sourceKey sourceCtxKey = "msg:source"

func WithSource(ctx context.Context, src string) context.Context {
	return context.WithValue(ctx, sourceKey, src)
}

func SourceFrom(ctx context.Context) string {
	v, _ := ctx.Value(sourceKey).(string)
	return v
}
