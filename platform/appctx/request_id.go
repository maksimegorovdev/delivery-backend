package appctx

import "context"

const RequestIDKey ctxKey = "request_id"

func WithRequestID(ctx context.Context, reqID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, reqID)
}

func GetRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if reqID, ok := ctx.Value(RequestIDKey).(string); ok {
		return reqID
	}
	return ""
}
