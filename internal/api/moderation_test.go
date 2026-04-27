package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildModerationRouter returns a moderation routes handler wired with guild context.
// actorRole maps to a permission level integer via guildLevelFromRoleName.
func buildModerationRouter(store *mockStore, actorID, actorRole string) http.Handler {
	inner := ModerationRoutes(store, nil)
	level := guildLevelFromRoleName(actorRole)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUserID(r.Context(), actorID)
		ctx = withGuildLevel(ctx, level)
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
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
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
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelAdmin, nil // target outranks actor (mod)
			}
			return models.PermissionLevelMember, nil
		},
	}
	router := buildModerationRouter(store, actorID, "mod")

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "reason"}, "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "equal or higher permission level")
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
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
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
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		insertBanFn: func(_ context.Context, _, _, _, _ string, expiresAt *time.Time) (*models.Ban, error) {
			capturedExpiresAt = expiresAt
			return &models.Ban{ID: uuid.New().String()}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	// No ExpiresIn - permanent ban.
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
		getServerMemberLevelFn: func(_ context.Context, serverID, userID string) (int, error) {
			if userID == actorID {
				return models.PermissionLevelAdmin, nil
			}
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
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
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
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

// ---------- List Bans ----------

func TestListBans_ReturnsOnlyActive(t *testing.T) {
	actorID := uuid.New().String()
	ban1ID := uuid.New().String()
	ban2ID := uuid.New().String()

	store := &mockStore{
		listActiveBansFn: func(_ context.Context, _ string) ([]models.Ban, error) {
			return []models.Ban{
				{ID: ban1ID, UserID: uuid.New().String(), ActorID: actorID, Reason: "harassment"},
				{ID: ban2ID, UserID: uuid.New().String(), ActorID: actorID, Reason: "spamming"},
			}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/bans", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var bans []models.Ban
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bans))
	require.Len(t, bans, 2)
}

func TestListBans_NonAdminDenied(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "member")

	rr := getModerationPath(router, "/bans", "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestListBans_EmptyWhenNoBans(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		listActiveBansFn: func(_ context.Context, _ string) ([]models.Ban, error) {
			return nil, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/bans", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var bans []models.Ban
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bans))
	assert.Len(t, bans, 0, "empty array must be returned when no active bans exist")
}

// ---------- List Mutes ----------

func TestListMutes_ReturnsOnlyActive(t *testing.T) {
	actorID := uuid.New().String()
	mute1ID := uuid.New().String()
	mute2ID := uuid.New().String()

	store := &mockStore{
		listActiveMutesFn: func(_ context.Context, _ string) ([]models.Mute, error) {
			return []models.Mute{
				{ID: mute1ID, UserID: uuid.New().String(), ActorID: actorID, Reason: "excessive noise"},
				{ID: mute2ID, UserID: uuid.New().String(), ActorID: actorID, Reason: "spam"},
			}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/mutes", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var mutes []models.Mute
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&mutes))
	require.Len(t, mutes, 2)
}

func TestListMutes_NonAdminDenied(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{}
	router := buildModerationRouter(store, actorID, "member")

	rr := getModerationPath(router, "/mutes", "")
	require.Equal(t, http.StatusForbidden, rr.Code)
	errBody := decodeError(t, rr)
	assert.Contains(t, errBody["error"], "admin")
}

func TestListMutes_EmptyWhenNoMutes(t *testing.T) {
	actorID := uuid.New().String()

	store := &mockStore{
		listActiveMutesFn: func(_ context.Context, _ string) ([]models.Mute, error) {
			return nil, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/mutes", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var mutes []models.Mute
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&mutes))
	assert.Len(t, mutes, 0, "empty array must be returned when no active mutes exist")
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
				SenderID:  &actorID, // actor is sender
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
				SenderID:  &ownerID, // different user sent the message
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
				SenderID:  &ownerID, // different user
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
		listAuditLogFn: func(_ context.Context, _ string, limit, offset int, _ *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
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

// TestAuditLog_FilterByAction verifies that ?action=kick causes the handler to pass a
// non-nil AuditLogFilter with Action="kick" to the store and returns only matching entries.
func TestAuditLog_FilterByAction(t *testing.T) {
	actorID := uuid.New().String()

	allEntries := []models.AuditLogEntry{
		{ID: "e-kick-1", ActorID: actorID, Action: "kick", Reason: "spamming"},
		{ID: "e-kick-2", ActorID: actorID, Action: "kick", Reason: "toxicity"},
		{ID: "e-ban-1", ActorID: actorID, Action: "ban", Reason: "repeated violations"},
	}

	var capturedFilter *db.AuditLogFilter
	store := &mockStore{
		listAuditLogFn: func(_ context.Context, _ string, _, _ int, filter *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
			capturedFilter = filter
			// Simulate DB filtering: only return entries matching the action filter.
			if filter != nil && filter.Action != "" {
				var filtered []models.AuditLogEntry
				for _, e := range allEntries {
					if e.Action == filter.Action {
						filtered = append(filtered, e)
					}
				}
				return filtered, nil
			}
			return allEntries, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/audit-log?action=kick", "")
	require.Equal(t, http.StatusOK, rr.Code)

	var entries []models.AuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 2, "only kick entries should be returned when filtering by action=kick")
	assert.Equal(t, "kick", entries[0].Action)
	assert.Equal(t, "kick", entries[1].Action)

	require.NotNil(t, capturedFilter, "filter must be non-nil when action param is provided")
	assert.Equal(t, "kick", capturedFilter.Action)
	assert.Empty(t, capturedFilter.ActorID)
	assert.Empty(t, capturedFilter.TargetID)
}

// TestAuditLog_FilterByActorID verifies that ?actor_id=X causes the handler to pass a
// non-nil AuditLogFilter with ActorID=X to the store and returns only matching entries.
func TestAuditLog_FilterByActorID(t *testing.T) {
	actorA := uuid.New().String()
	actorB := uuid.New().String()

	allEntries := []models.AuditLogEntry{
		{ID: "e-1", ActorID: actorA, Action: "kick", Reason: "rule1"},
		{ID: "e-2", ActorID: actorA, Action: "mute", Reason: "rule2"},
		{ID: "e-3", ActorID: actorB, Action: "ban", Reason: "rule3"},
	}

	var capturedFilter *db.AuditLogFilter
	store := &mockStore{
		listAuditLogFn: func(_ context.Context, _ string, _, _ int, filter *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
			capturedFilter = filter
			// Simulate DB filtering: only return entries matching the actor filter.
			if filter != nil && filter.ActorID != "" {
				var filtered []models.AuditLogEntry
				for _, e := range allEntries {
					if e.ActorID == filter.ActorID {
						filtered = append(filtered, e)
					}
				}
				return filtered, nil
			}
			return allEntries, nil
		},
	}
	router := buildModerationRouter(store, actorA, "admin")

	rr := getModerationPath(router, "/audit-log?actor_id="+actorA, "")
	require.Equal(t, http.StatusOK, rr.Code)

	var entries []models.AuditLogEntry
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.Len(t, entries, 2, "only entries by actorA should be returned when filtering by actor_id=actorA")
	for _, e := range entries {
		assert.Equal(t, actorA, e.ActorID)
	}

	require.NotNil(t, capturedFilter, "filter must be non-nil when actor_id param is provided")
	assert.Equal(t, actorA, capturedFilter.ActorID)
	assert.Empty(t, capturedFilter.Action)
	assert.Empty(t, capturedFilter.TargetID)
}

// TestAuditLog_NoFilter verifies that when no filter params are provided, the handler
// passes a nil filter to the store (no WHERE clause narrowing beyond server_id).
func TestAuditLog_NoFilter(t *testing.T) {
	actorID := uuid.New().String()

	var capturedFilter *db.AuditLogFilter
	store := &mockStore{
		listAuditLogFn: func(_ context.Context, _ string, _, _ int, filter *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
			capturedFilter = filter
			return []models.AuditLogEntry{}, nil
		},
	}
	router := buildModerationRouter(store, actorID, "admin")

	rr := getModerationPath(router, "/audit-log", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Nil(t, capturedFilter, "filter must be nil when no filter params are provided")
}

// ---------- Claim Invite - Banned User ----------

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
			return &models.Server{ID: serverID}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret, nil)

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
			return &models.Server{ID: guildAID}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret, nil)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "GUILD-A-INVITE"}, token)
	require.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- DisconnectUser verification ----------

// mockHub implements GlobalBroadcaster and records broadcast and disconnect calls.
type mockHub struct {
	broadcastCalls  []mockBroadcast
	disconnectCalls []string
}

type mockBroadcast struct {
	serverID string
	message  []byte
}

func (m *mockHub) BroadcastToAll(msg []byte)                { /* no-op */ }
func (m *mockHub) BroadcastToServer(sid string, msg []byte) { m.broadcastCalls = append(m.broadcastCalls, mockBroadcast{sid, msg}) }
func (m *mockHub) BroadcastToUser(_ string, _ []byte)       { /* no-op */ }
func (m *mockHub) DisconnectUser(uid string)                 { m.disconnectCalls = append(m.disconnectCalls, uid) }
func (m *mockHub) DisconnectDevice(_ string, _ string)       { /* no-op */ }

// buildModerationRouterWithHub wires ModerationRoutes with a custom hub for testing broadcast/disconnect.
// actorRole maps to a permission level integer via guildLevelFromRoleName.
func buildModerationRouterWithHub(store *mockStore, actorID, actorRole string, hub GlobalBroadcaster) http.Handler {
	inner := ModerationRoutes(store, hub)
	level := guildLevelFromRoleName(actorRole)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUserID(r.Context(), actorID)
		ctx = withGuildLevel(ctx, level)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TestKickMember_CallsDisconnectUser verifies kickMember broadcasts member_kicked and
// then calls DisconnectUser on the target so their WS session is terminated immediately.
func TestKickMember_CallsDisconnectUser(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "mod", hub)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "spamming"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)

	// 2 broadcasts: system_message (from EmitSystemMessage) + member_kicked
	require.Len(t, hub.broadcastCalls, 2, "kickMember must broadcast system_message and member_kicked")
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(hub.broadcastCalls[1].message, &payload))
	assert.Equal(t, "member_kicked", payload["type"])

	// DisconnectUser runs in a goroutine after 500ms flush delay
	time.Sleep(700 * time.Millisecond)
	require.Len(t, hub.disconnectCalls, 1, "kickMember must call DisconnectUser once")
	assert.Equal(t, targetID, hub.disconnectCalls[0])
}

// TestBanMember_CallsDisconnectUser verifies banMember broadcasts member_banned and
// then calls DisconnectUser on the target so their WS session is terminated immediately.
func TestBanMember_CallsDisconnectUser(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		insertBanFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) (*models.Ban, error) {
			return &models.Ban{ID: uuid.New().String()}, nil
		},
		deleteSessionsByUserIDFn: func(_ context.Context, _ string) error { return nil },
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "admin", hub)

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "harassment"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)

	// 2 broadcasts: system_message (from EmitSystemMessage) + member_banned
	require.Len(t, hub.broadcastCalls, 2, "banMember must broadcast system_message and member_banned")
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(hub.broadcastCalls[1].message, &payload))
	assert.Equal(t, "member_banned", payload["type"])

	// DisconnectUser runs in a goroutine after 500ms flush delay
	time.Sleep(700 * time.Millisecond)
	require.Len(t, hub.disconnectCalls, 1, "banMember must call DisconnectUser once")
	assert.Equal(t, targetID, hub.disconnectCalls[0])
}

// ---------- System Message Emission Tests ----------

// TestKickMember_EmitsSystemMessage verifies kickMember calls EmitSystemMessage
// with event_type="member_kicked" after the audit log insert.
func TestKickMember_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()

	var sysMsgCalled bool
	var capturedEventType string
	var capturedActorID string
	var capturedTargetID *string
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, actor string, target *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			capturedActorID = actor
			capturedTargetID = target
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "mod", hub)

	rr := postServerJSON(router, "/kick", models.KickRequest{UserID: targetID, Reason: "spamming"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "kickMember must emit system message")
	assert.Equal(t, "member_kicked", capturedEventType)
	assert.Equal(t, actorID, capturedActorID)
	require.NotNil(t, capturedTargetID)
	assert.Equal(t, targetID, *capturedTargetID)
}

// TestBanMember_EmitsSystemMessage verifies banMember calls EmitSystemMessage
// with event_type="member_banned" and metadata containing expires_in.
func TestBanMember_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	expiresIn := 3600

	var sysMsgCalled bool
	var capturedEventType string
	var capturedMetadata map[string]interface{}
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		insertBanFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) (*models.Ban, error) {
			return &models.Ban{ID: uuid.New().String()}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, metadata map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			capturedMetadata = metadata
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "admin", hub)

	rr := postServerJSON(router, "/ban", models.BanRequest{UserID: targetID, Reason: "harassment", ExpiresIn: &expiresIn}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "banMember must emit system message")
	assert.Equal(t, "member_banned", capturedEventType)
	assert.NotNil(t, capturedMetadata)
	assert.Equal(t, 3600, capturedMetadata["expires_in"])
}

// TestUnbanMember_EmitsSystemMessage verifies unbanMember calls EmitSystemMessage
// with event_type="member_unbanned".
func TestUnbanMember_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	banID := uuid.New().String()

	var sysMsgCalled bool
	var capturedEventType string
	store := &mockStore{
		getActiveBanFn: func(_ context.Context, _, userID string) (*models.Ban, error) {
			return &models.Ban{ID: banID, UserID: userID}, nil
		},
		liftBanFn: func(_ context.Context, _, _ string) error {
			return nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "admin", hub)

	rr := postServerJSON(router, "/unban", models.UnbanRequest{UserID: targetID, Reason: "appeals granted"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "unbanMember must emit system message")
	assert.Equal(t, "member_unbanned", capturedEventType)
}

// TestMuteMember_EmitsSystemMessage verifies muteMember calls EmitSystemMessage
// with event_type="member_muted" and metadata containing expires_in.
func TestMuteMember_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	expiresIn := 1800

	var sysMsgCalled bool
	var capturedEventType string
	var capturedMetadata map[string]interface{}
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, userID string) (int, error) {
			if userID == targetID {
				return models.PermissionLevelMember, nil
			}
			return models.PermissionLevelMember, nil
		},
		insertMuteFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) (*models.Mute, error) {
			return &models.Mute{ID: uuid.New().String()}, nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, metadata map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			capturedMetadata = metadata
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "mod", hub)

	rr := postServerJSON(router, "/mute", models.MuteRequest{UserID: targetID, Reason: "disruptive", ExpiresIn: &expiresIn}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "muteMember must emit system message")
	assert.Equal(t, "member_muted", capturedEventType)
	assert.NotNil(t, capturedMetadata)
	assert.Equal(t, 1800, capturedMetadata["expires_in"])
}

// TestUnmuteMember_EmitsSystemMessage verifies unmuteMember calls EmitSystemMessage
// with event_type="member_unmuted".
func TestUnmuteMember_EmitsSystemMessage(t *testing.T) {
	actorID := uuid.New().String()
	targetID := uuid.New().String()
	muteID := uuid.New().String()

	var sysMsgCalled bool
	var capturedEventType string
	store := &mockStore{
		getActiveMuteFn: func(_ context.Context, _, userID string) (*models.Mute, error) {
			return &models.Mute{ID: muteID, UserID: userID}, nil
		},
		liftMuteFn: func(_ context.Context, _, _ string) error {
			return nil
		},
		insertSystemMessageFn: func(_ context.Context, _, eventType, _ string, _ *string, _ string, _ map[string]interface{}) (*models.SystemMessage, error) {
			sysMsgCalled = true
			capturedEventType = eventType
			return &models.SystemMessage{ID: uuid.New().String()}, nil
		},
	}
	hub := &mockHub{}
	router := buildModerationRouterWithHub(store, actorID, "mod", hub)

	rr := postServerJSON(router, "/unmute", models.UnmuteRequest{UserID: targetID, Reason: "time served"}, "")
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.True(t, sysMsgCalled, "unmuteMember must emit system message")
	assert.Equal(t, "member_unmuted", capturedEventType)
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
			return &models.Server{ID: id}, nil
		},
	}
	token := makeAuth(store, userID)
	router := PublicInviteRoutes(store, testJWTSecret, nil)

	rr := postServerJSON(router, "/claim", map[string]string{"code": "GUILD-B-INVITE"}, token)
	// Guild B invite must succeed even though user is banned from Guild A.
	require.Equal(t, http.StatusOK, rr.Code)
}
