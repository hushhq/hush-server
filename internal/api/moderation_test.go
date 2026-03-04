package api

import (
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

// buildModerationRouter returns a moderation routes handler wired with guild context.
// actorRole controls what guild role the requester appears to have.
func buildModerationRouter(store *mockStore, actorID, actorRole string) http.Handler {
	inner := ModerationRoutes(store, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUserID(r.Context(), actorID)
		ctx = withGuildRole(ctx, actorRole)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

// deleteReq issues DELETE to the handler.
func deleteReq(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// getModerationPath issues GET to the handler.
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
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			if userID == targetID {
				return "member", nil
			}
			return "", nil
		},
		insertAuditLogFn: func(_ context.Context, _, _ string, _ *string, _, _ string, _ map[string]interface{}) error {
			auditLogged = true
			return nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "spamming"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, auditLogged, "audit log must be inserted on successful kick")
}

func TestKickMember_InsufficientRole(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "member")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "reason"}, "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "mod")
}

func TestKickMember_MissingReason(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: ""}, "")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "reason")
}

func TestKickMember_CannotKickHigherRole(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			if userID == targetID {
				return "admin", nil // target outranks actor (mod)
			}
			return "", nil
		},
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "reason"}, "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "equal or higher role")
}

func TestKickMember_CannotKickSelf(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: actorID, Reason: "reason"}, "")
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
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			if userID == targetID {
				return "member", nil
			}
			return "", nil
		},
		insertBanFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) (*models.Ban, error) {
			banInserted = true
			return &models.Ban{ID: uuid.New().String()}, nil
		},
		insertAuditLogFn: func(_ context.Context, _, _ string, _ *string, _, _ string, _ map[string]interface{}) error {
			auditLogged = true
			return nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "harassment", ExpiresIn: &expiresIn}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, banInserted, "ban record must be created")
	require.True(t, auditLogged, "audit log must be inserted on successful ban")
}

func TestBanMember_ModCannotBan(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "mod") // mod cannot ban

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "reason"}, "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestBanMember_PermanentBan(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var capturedExpiresAt *time.Time
	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			if userID == targetID {
				return "member", nil
			}
			return "", nil
		},
		insertBanFn: func(_ context.Context, _, _, _, _ string, expiresAt *time.Time) (*models.Ban, error) {
			capturedExpiresAt = expiresAt
			return &models.Ban{ID: uuid.New().String()}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	// No ExpiresIn — permanent ban.
	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "permanent ban reason"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Nil(t, capturedExpiresAt, "permanent ban must have nil expiresAt")
}

// TestBan_GuildScoped_DoesNotAffectOtherGuilds verifies that InsertBan is called with
// the specific serverID from the URL context, not a global scope (IROLE-04).
// We use ServerRoutes so chi URL params are properly resolved.
func TestBan_GuildScoped_DoesNotAffectOtherGuilds(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	guildAID := uuid.New().String()
	guildBID := uuid.New().String()

	var capturedServerIDs []string
	store := &mockStore{
		getServerMemberRoleFn: func(_ context.Context, serverID, userID string) (string, error) {
			if userID == actorID {
				return "admin", nil
			}
			if userID == targetID {
				return "member", nil
			}
			return "", nil
		},
		insertBanFn: func(_ context.Context, serverID, _, _, _ string, _ *time.Time) (*models.Ban, error) {
			capturedServerIDs = append(capturedServerIDs, serverID)
			return &models.Ban{ID: uuid.New().String()}, nil
		},
	}
	token := makeAuth(store, actorID)
	router := ServerRoutes(store, nil, testJWTSecret)

	// Ban in Guild A.
	rrA := postServerJSON(router, "/"+guildAID+"/moderation/ban",
		models.BanRequest{UserID: targetID, Reason: "behaviour"}, token)
	require.Equal(t, http.StatusNoContent, rrA.Code)

	// Reset and ban in Guild B.
	rrB := postServerJSON(router, "/"+guildBID+"/moderation/ban",
		models.BanRequest{UserID: targetID, Reason: "different issue"}, token)
	require.Equal(t, http.StatusNoContent, rrB.Code)

	require.Len(t, capturedServerIDs, 2)
	assert.Equal(t, guildAID, capturedServerIDs[0], "first ban must be scoped to Guild A (IROLE-04)")
	assert.Equal(t, guildBID, capturedServerIDs[1], "second ban must be scoped to Guild B (IROLE-04)")
	assert.NotEqual(t, capturedServerIDs[0], capturedServerIDs[1], "bans in different guilds must have different serverIDs")
}

// ---------- Unban ----------

func TestUnbanMember_Success(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	banID := uuid.New().String()

	var banLifted bool
	store := &mockStore{
		getActiveBanFn: func(_ context.Context, _, userID string) (*models.Ban, error) {
			return &models.Ban{ID: banID, UserID: userID}, nil
		},
		liftBanFn: func(_ context.Context, _, _ string) error {
			banLifted = true
			return nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "appeals granted"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, banLifted, "ban must be lifted on successful unban")
}

func TestUnbanMember_ModCannotUnban(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "mod") // mod cannot unban

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "reason"}, "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestUnbanMember_NoBanExists(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getActiveBanFn: func(_ context.Context, _, _ string) (*models.Ban, error) {
			return nil, nil // no active ban
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "reason"}, "")
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
		getServerMemberRoleFn: func(_ context.Context, _, userID string) (string, error) {
			if userID == targetID {
				return "member", nil
			}
			return "", nil
		},
		insertMuteFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) (*models.Mute, error) {
			muteInserted = true
			return &models.Mute{ID: uuid.New().String()}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/mute", models.MuteRequest{UserID: targetID, Reason: "disruptive"}, "")
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
		getActiveMuteFn: func(_ context.Context, _, userID string) (*models.Mute, error) {
			return &models.Mute{ID: muteID, UserID: userID}, nil
		},
		liftMuteFn: func(_ context.Context, _, _ string) error {
			muteLifted = true
			return nil
		},
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/unmute", models.UnmuteRequest{UserID: targetID, Reason: "time served"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, muteLifted, "mute must be lifted on successful unmute")
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
	router := buildModerationRouter(store, actorID, "member")

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), "")
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
		deleteMessageFn: func(_ context.Context, _ string) error {
			deleted = true
			return nil
		},
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), "")
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
	}
	router := buildModerationRouter(store, actorID, "member")

	rr := deleteReq(router, fmt.Sprintf("/messages/%s", msgID), "")
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- Audit Log ----------

func TestGetAuditLog_AdminAccess(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		listAuditLogFn: func(_ context.Context, _ string, limit, offset int) ([]models.AuditLogEntry, error) {
			return []models.AuditLogEntry{
				{ID: "entry-1", ActorID: actorID, Action: "kick", Reason: "spamming"},
			}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/audit-log", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var entries []models.AuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "entry-1", entries[0].ID)
}

func TestGetAuditLog_ModDenied(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "mod") // mod cannot access audit log

	rr := getModerationPath(router, "/audit-log", "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

// ---------- Claim Invite — Banned User ----------

func TestClaimInvite_BannedUser(t *testing.T) {
	userID := uuid.New().String()
	serverID := uuid.New().String()
	banExpiresAt := time.Now().Add(24 * time.Hour)

	store := &mockStore{
		getInviteByCodeFn: func(_ context.Context, _ string) (*models.InviteCode, error) {
			return &models.InviteCode{
				Code:      "ANYCODE",
				ServerID:  &serverID,
				ExpiresAt: time.Now().Add(time.Hour),
				MaxUses:   10,
			}, nil
		},
		getActiveBanFn: func(_ context.Context, _, _ string) (*models.Ban, error) {
			return &models.Ban{
				ID:        uuid.New().String(),
				UserID:    userID,
				Reason:    "harassment",
				ExpiresAt: &banExpiresAt,
			}, nil
		},
		getServerByIDFn: func(_ context.Context, _ string) (*models.Server, error) {
			return &models.Server{ID: serverID, Name: "Test Guild"}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "ANYCODE"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	errStr, _ := resp["error"].(string)
	assert.Contains(t, errStr, "banned")
	assert.Contains(t, resp, "ban_expires_at")
}

// TestClaimInvite_BannedFromGuild_Rejected verifies guild-scoped ban prevents claiming invite for that guild.
func TestClaimInvite_BannedFromGuild_Rejected(t *testing.T) {
	userID := uuid.New().String()
	guildAID := uuid.New().String()

	store := &mockStore{
		getInviteByCodeFn: func(_ context.Context, _ string) (*models.InviteCode, error) {
			return &models.InviteCode{
				Code:      "GUILD-A-INVITE",
				ServerID:  &guildAID,
				ExpiresAt: time.Now().Add(time.Hour),
				MaxUses:   10,
			}, nil
		},
		getActiveBanFn: func(_ context.Context, serverID, _ string) (*models.Ban, error) {
			if serverID == guildAID {
				return &models.Ban{ID: uuid.New().String(), UserID: userID}, nil
			}
			return nil, nil
		},
		getServerByIDFn: func(_ context.Context, _ string) (*models.Server, error) {
			return &models.Server{ID: guildAID, Name: "Guild A"}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "GUILD-A-INVITE"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// TestClaimInvite_BannedFromOtherGuild_Allowed verifies that a guild-scoped ban does NOT
// prevent the user from claiming an invite for a different guild (IROLE-04).
func TestClaimInvite_BannedFromOtherGuild_Allowed(t *testing.T) {
	userID := uuid.New().String()
	guildAID := uuid.New().String()
	guildBID := uuid.New().String()

	store := &mockStore{
		getInviteByCodeFn: func(_ context.Context, _ string) (*models.InviteCode, error) {
			// Invite is for Guild B.
			return &models.InviteCode{
				Code:      "GUILD-B-INVITE",
				ServerID:  &guildBID,
				ExpiresAt: time.Now().Add(time.Hour),
				MaxUses:   10,
			}, nil
		},
		getActiveBanFn: func(_ context.Context, serverID, _ string) (*models.Ban, error) {
			// User is banned from Guild A only.
			if serverID == guildAID {
				return &models.Ban{ID: uuid.New().String(), UserID: userID}, nil
			}
			return nil, nil
		},
		claimInviteUseFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
		getServerByIDFn: func(_ context.Context, id string) (*models.Server, error) {
			return &models.Server{ID: id, Name: "Guild B"}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "GUILD-B-INVITE"}, token)
	// Guild B invite must succeed even though user is banned from Guild A.
	require.Equal(t, http.StatusOK, rr.Code)
}
