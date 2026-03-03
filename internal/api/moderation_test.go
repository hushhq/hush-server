package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildModerationRouter returns a Chi router wired with ModerationRoutes for tests.
func buildModerationRouter(store *mockStore) http.Handler {
	return ModerationRoutes(store, nil, testJWTSecret)
}

// deleteReq issues DELETE to the handler with optional auth token.
func deleteReq(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// putModerationJSON issues PUT to the handler with JSON body and optional auth token.
func putModerationJSON(handler http.Handler, path string, body interface{}, token string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// getModerationPath issues GET to the handler with optional auth token.
func getModerationPath(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- Kick ----------

func TestKickMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var auditLogged bool
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "mod", nil
			}
			return "member", nil
		},
		insertAuditLogFn: func(_ context.Context, _ string, _ *string, _, _ string, _ map[string]interface{}) error {
			auditLogged = true
			return nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "spamming"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, auditLogged, "audit log must be inserted on successful kick")
}

func TestKickMember_InsufficientRole(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "member", nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "reason"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "mod")
}

func TestKickMember_MissingReason(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: ""}, token)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "reason")
}

func TestKickMember_CannotKickHigherRole(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "mod", nil
			}
			return "admin", nil // target outranks actor
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "reason"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "equal or higher role")
}

func TestKickMember_CannotKickSelf(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: actorID, Reason: "reason"}, token)
	// Handler returns 400 for self-kick.
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "yourself")
}

// ---------- Ban ----------

func TestBanMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var banInserted bool
	var auditLogged bool
	expiresIn := 3600 // 1 hour

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			return "member", nil
		},
		insertBanFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*models.Ban, error) {
			banInserted = true
			return &models.Ban{ID: uuid.New().String()}, nil
		},
		insertAuditLogFn: func(_ context.Context, _ string, _ *string, _, _ string, _ map[string]interface{}) error {
			auditLogged = true
			return nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "harassment", ExpiresIn: &expiresIn}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, banInserted, "ban record must be created")
	require.True(t, auditLogged, "audit log must be inserted on successful ban")
}

func TestBanMember_ModCannotBan(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil // mod cannot ban
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "reason"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestBanMember_PermanentBan(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var capturedExpiresAt *time.Time
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			return "member", nil
		},
		insertBanFn: func(_ context.Context, _, _, _ string, expiresAt *time.Time) (*models.Ban, error) {
			capturedExpiresAt = expiresAt
			return &models.Ban{ID: uuid.New().String()}, nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	// No ExpiresIn — permanent ban.
	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "permanent ban reason"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Nil(t, capturedExpiresAt, "permanent ban must have nil expiresAt")
}

// ---------- Unban ----------

func TestUnbanMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	banID := uuid.New().String()

	var banLifted bool
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			return "member", nil
		},
		getActiveBanFn: func(_ context.Context, userID string) (*models.Ban, error) {
			return &models.Ban{ID: banID, UserID: userID}, nil
		},
		liftBanFn: func(_ context.Context, _, _ string) error {
			banLifted = true
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "appeals granted"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, banLifted, "ban must be lifted on successful unban")
}

func TestUnbanMember_ModCannotUnban(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil // mod cannot unban
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "reason"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestUnbanMember_NoBanExists(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "admin", nil
		},
		getActiveBanFn: func(_ context.Context, _ string) (*models.Ban, error) {
			return nil, nil // no active ban
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "reason"}, token)
	require.Equal(t, http.StatusNotFound, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "no active ban")
}

// ---------- Mute ----------

func TestMuteMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var muteInserted bool
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "mod", nil
			}
			return "member", nil
		},
		insertMuteFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*models.Mute, error) {
			muteInserted = true
			return &models.Mute{ID: uuid.New().String()}, nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/mute", models.MuteRequest{UserID: targetID, Reason: "disruptive"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, muteInserted, "mute record must be created on success")
}

// ---------- Unmute ----------

func TestUnmuteMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	muteID := uuid.New().String()

	var muteLifted bool
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "mod", nil
			}
			return "member", nil
		},
		getActiveMuteFn: func(_ context.Context, userID string) (*models.Mute, error) {
			return &models.Mute{ID: muteID, UserID: userID}, nil
		},
		liftMuteFn: func(_ context.Context, _, _ string) error {
			muteLifted = true
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := postServerJSON(router, "/unmute", models.UnmuteRequest{UserID: targetID, Reason: "time served"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, muteLifted, "mute must be lifted on successful unmute")
}

// ---------- Change Role ----------

func TestChangeRole_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var capturedMetadata map[string]interface{}
	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			return "member", nil // target current role
		},
		insertAuditLogFn: func(_ context.Context, _ string, _ *string, _, _ string, metadata map[string]interface{}) error {
			capturedMetadata = metadata
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := putModerationJSON(router, "/role", models.ChangeRoleRequest{UserID: targetID, NewRole: "mod", Reason: "trusted contributor"}, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.NotNil(t, capturedMetadata, "audit log must record role change metadata")
	assert.Equal(t, "member", capturedMetadata["old_role"])
	assert.Equal(t, "mod", capturedMetadata["new_role"])
}

func TestChangeRole_CannotPromoteToOwner(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "admin", nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := putModerationJSON(router, "/role", models.ChangeRoleRequest{UserID: targetID, NewRole: "owner", Reason: "reason"}, token)
	// "owner" is rejected as an invalid newRole value.
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChangeRole_CannotChangeHigherRole(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			return "owner", nil // target is owner — outranks admin
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := putModerationJSON(router, "/role", models.ChangeRoleRequest{UserID: targetID, NewRole: "member", Reason: "reason"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- Delete Message ----------

func TestDeleteMessage_OwnMessage(t *testing.T) {
	actorID := uuid.New().String()
	msgID := uuid.New().String()

	var deleted bool
	store := &mockStore{
		getMessageByIDFn: func(_ context.Context, messageID string) (*models.Message, error) {
			return &models.Message{
				ID:        messageID,
				SenderID:  actorID, // actor is sender
				ChannelID: "ch-1",
			}, nil
		},
		deleteMessageFn: func(_ context.Context, _ string) error {
			deleted = true
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, deleted, "message must be deleted by its own sender")
}

func TestDeleteMessage_ModDeletesOthers(t *testing.T) {
	actorID := uuid.New().String()
	ownerID := uuid.New().String()
	msgID := uuid.New().String()

	var deleted bool
	store := &mockStore{
		getMessageByIDFn: func(_ context.Context, messageID string) (*models.Message, error) {
			return &models.Message{
				ID:        messageID,
				SenderID:  ownerID, // different user sent the message
				ChannelID: "ch-1",
			}, nil
		},
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil
		},
		deleteMessageFn: func(_ context.Context, _ string) error {
			deleted = true
			return nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, deleted, "mod must be able to delete another user's message")
}

func TestDeleteMessage_MemberCannotDeleteOthers(t *testing.T) {
	actorID := uuid.New().String()
	ownerID := uuid.New().String()
	msgID := uuid.New().String()

	store := &mockStore{
		getMessageByIDFn: func(_ context.Context, messageID string) (*models.Message, error) {
			return &models.Message{
				ID:        messageID,
				SenderID:  ownerID, // different user
				ChannelID: "ch-1",
			}, nil
		},
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "member", nil // no mod+ role
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- Audit Log ----------

func TestGetAuditLog_AdminAccess(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "admin", nil
		},
		listAuditLogFn: func(_ context.Context, limit, offset int) ([]models.AuditLogEntry, error) {
			return []models.AuditLogEntry{
				{ID: "entry-1", ActorID: actorID, Action: "kick", Reason: "spamming"},
			}, nil
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := getModerationPath(router, "/audit-log", token)
	require.Equal(t, http.StatusOK, rr.Code)
	var entries []models.AuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "entry-1", entries[0].ID)
}

func TestGetAuditLog_ModDenied(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		getUserRoleFn: func(_ context.Context, _ string) (string, error) {
			return "mod", nil // mod cannot access audit log
		},
	}
	token := makeAuth(store, actorID)
	router := buildModerationRouter(store)

	rr := getModerationPath(router, "/audit-log", token)
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

// ---------- Claim Invite — Banned User ----------

func TestClaimInvite_BannedUser(t *testing.T) {
	userID := uuid.New().String()
	banExpiresAt := time.Now().Add(24 * time.Hour)

	store := &mockStore{
		getActiveBanFn: func(_ context.Context, _ string) (*models.Ban, error) {
			return &models.Ban{
				ID:        uuid.New().String(),
				UserID:    userID,
				Reason:    "harassment",
				ExpiresAt: &banExpiresAt,
			}, nil
		},
	}
	token := makeAuth(store, userID)
	router := InviteRoutes(store, testJWTSecret)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "ANYCODE"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "banned")
	assert.Contains(t, resp, "ban_expires_at")
}
