package api

import "context"

type contextKey string

const (
	contextKeyUserID    contextKey = "userID"
	contextKeySessionID contextKey = "sessionID"
	contextKeyGuildRole contextKey = "guildRole"   // kept for handlers not yet migrated to levels
	contextKeyGuildLevel contextKey = "guildLevel" // integer permission level (0-3)
	contextKeyIsGuest   contextKey = "isGuest"     // true for ephemeral guest sessions
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

func withGuildRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, contextKeyGuildRole, role)
}

func guildRoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyGuildRole).(string)
	return v
}

func withGuildLevel(ctx context.Context, level int) context.Context {
	return context.WithValue(ctx, contextKeyGuildLevel, level)
}

func guildLevelFromContext(ctx context.Context) int {
	v, _ := ctx.Value(contextKeyGuildLevel).(int)
	return v
}

func withIsGuest(ctx context.Context, isGuest bool) context.Context {
	return context.WithValue(ctx, contextKeyIsGuest, isGuest)
}

func isGuestFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(contextKeyIsGuest).(bool)
	return v
}
