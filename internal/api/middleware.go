package api

import (
	"net/http"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/db"

	"github.com/go-chi/chi/v5"
)

// RequireGuildMember extracts {serverId} from the URL, verifies the requesting
// user is a member of that guild, and injects the guild role into the request
// context. Must be applied after RequireAuth so userID is available.
func RequireGuildMember(store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverID := chi.URLParam(r, "serverId")
			if serverID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
				return
			}
			userID := userIDFromContext(r.Context())
			role, err := store.GetServerMemberRole(r.Context(), serverID, userID)
			if err != nil || role == "" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a guild member"})
				return
			}
			ctx := withGuildRole(r.Context(), role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth returns middleware that validates the bearer JWT, verifies the
// session against the database, and injects userID and sessionID into the
// request context. Both jwtSecret and pool are captured at construction time
// so callers don't need to carry them through handler structs.
func RequireAuth(jwtSecret string, store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := bearerToken(r)
			if token == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization"})
				return
			}
			userID, sessionID, err := auth.ValidateJWT(token, jwtSecret)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
				return
			}
			tokenHash := auth.TokenHash(token)
			sess, err := store.GetSessionByTokenHash(r.Context(), tokenHash)
			if err != nil || sess == nil || sess.ID != sessionID {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session not found or expired"})
				return
			}
			if sess.UserID != userID {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session mismatch"})
				return
			}
			r = r.WithContext(withUserID(r.Context(), userID))
			r = r.WithContext(withSessionID(r.Context(), sessionID))
			next.ServeHTTP(w, r)
		})
	}
}
