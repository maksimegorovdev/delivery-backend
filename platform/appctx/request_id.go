package appctx

import "context"

const requestIDKey ctxKey = "request_id"

func WithRequestID(ctx context.Context, reqID string) context.Context {
	return context.WithValue(ctx, requestIDKey, reqID)
}

func GetRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if reqID, ok := ctx.Value(requestIDKey).(string); ok {
		return reqID
	}
	return ""
}
