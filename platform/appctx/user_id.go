package appctx

import "context"

const userIDKey ctxKey = "user_id"

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

func GetUserID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(userIDKey).(string); ok {
		return id
	}
	return ""
}
