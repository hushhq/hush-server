package api

import (
	"net/http"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

// RequireGuildMember extracts {serverId} from the URL, verifies the requesting
// user is a member of that guild, and injects the guild permission level into the
// request context. Must be applied after RequireAuth so userID is available.
func RequireGuildMember(store db.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverID := chi.URLParam(r, "serverId")
			if serverID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing server ID"})
				return
			}
			// Check federated membership first.
			fedID := federatedIdentityIDFromContext(r.Context())
			if fedID != "" {
				level, err := store.GetServerMemberLevelByFederatedID(r.Context(), serverID, fedID)
				if err != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a guild member"})
					return
				}
				ctx := withGuildLevel(r.Context(), level)
				ctx = withGuildRole(ctx, permissionLevelToRole(level))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Local user membership (existing logic).
			userID := userIDFromContext(r.Context())
			level, err := store.GetServerMemberLevel(r.Context(), serverID, userID)
			if err != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a guild member"})
				return
			}
			// Inject both the integer level and the legacy role string so handlers
			// that check guildRoleFromContext still work during the Plan 03 migration.
			ctx := withGuildLevel(r.Context(), level)
			ctx = withGuildRole(ctx, permissionLevelToRole(level))
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
			userID, sessionID, isGuest, isFederated, federatedIdentityID, err := auth.ValidateJWT(token, jwtSecret)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
				return
			}

			// Federated sessions are stateless — no DB session record exists.
			if isFederated {
				r = r.WithContext(withUserID(r.Context(), federatedIdentityID))
				r = r.WithContext(withSessionID(r.Context(), sessionID))
				r = r.WithContext(withIsGuest(r.Context(), false))
				r = r.WithContext(withIsFederated(r.Context(), true))
				r = r.WithContext(withFederatedIdentityID(r.Context(), federatedIdentityID))
				next.ServeHTTP(w, r)
				return
			}

			// Guest sessions are validated by JWT signature only — no DB record exists.
			if !isGuest {
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
			}
			r = r.WithContext(withUserID(r.Context(), userID))
			r = r.WithContext(withSessionID(r.Context(), sessionID))
			r = r.WithContext(withIsGuest(r.Context(), isGuest))
			next.ServeHTTP(w, r)
		})
	}
}

// permissionLevelToRole converts a guild permission level to a role string.
// Guild owner maps to "admin" at instance level (owner is a guild concept).
func permissionLevelToRole(level int) string {
	switch level {
	case models.PermissionLevelOwner, models.PermissionLevelAdmin:
		return "admin"
	case models.PermissionLevelMod:
		return "mod"
	default:
		return "member"
	}
}
