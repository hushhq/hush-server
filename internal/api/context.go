package api

import "context"

type contextKey string

const (
	contextKeyUserID    contextKey = "userID"
	contextKeySessionID contextKey = "sessionID"
)

func withUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, contextKeyUserID, userID)
}

func userIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyUserID).(string)
	return v
}

func withSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, contextKeySessionID, sessionID)
}

func sessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeySessionID).(string)
	return v
}
