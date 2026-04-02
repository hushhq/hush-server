package api

import "context"

const (
	contextKeyAdminID   contextKey = "adminID"
	contextKeyAdminRole contextKey = "adminRole"
)

func withAdminID(ctx context.Context, adminID string) context.Context {
	return context.WithValue(ctx, contextKeyAdminID, adminID)
}

func adminIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyAdminID).(string)
	return v
}

func withAdminRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, contextKeyAdminRole, role)
}

func adminRoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(contextKeyAdminRole).(string)
	return v
}
