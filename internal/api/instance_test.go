package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func instanceRouter(store *mockStore) http.Handler {
	return InstanceRoutes(store, nil, testJWTSecret)
}

// ---------- GET /instance ----------

func TestGetInstanceConfig_ReturnsConfig(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	ownerID := uuid.New().String()
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "My Hush",
			OwnerID:          &ownerID,
			RegistrationMode: "open",
		}, nil
	}
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		return "owner", nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp instanceConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "My Hush", resp.Name)
	assert.Equal(t, "open", resp.RegistrationMode)
	assert.True(t, resp.Bootstrapped, "bootstrapped must be true when ownerID is set")
	assert.Equal(t, "owner", resp.MyRole)
}

func TestGetInstanceConfig_Unbootstrapped_BootstrappedFalse(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getInstanceConfigFn = func(_ context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			ID:               "inst-1",
			Name:             "Fresh Instance",
			OwnerID:          nil,
			RegistrationMode: "open",
		}, nil
	}
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) {
		return "member", nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var resp instanceConfigResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Bootstrapped, "bootstrapped must be false when ownerID is nil")
	assert.Equal(t, "member", resp.MyRole)
}

func TestGetInstanceConfig_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := instanceRouter(store)
	rr := getServer(router, "/", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- PUT /instance ----------

func TestUpdateInstanceConfig_OwnerCanUpdate_Returns204(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, uid string) (string, error) {
		if uid == userID {
			return "owner", nil
		}
		return "member", nil
	}
	var updatedName string
	store.updateInstanceConfigFn = func(_ context.Context, name *string, _ *string, _ *string, _ *string) error {
		if name != nil {
			updatedName = *name
		}
		return nil
	}
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Updated Name"}, token)
	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, "Updated Name", updatedName)
}

func TestUpdateInstanceConfig_NonOwnerForbidden_Returns403(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "member", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Hack"}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	err := decodeError(t, rr)
	assert.Contains(t, err["error"], "owner")
}

func TestUpdateInstanceConfig_AdminForbidden_Returns403(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "admin", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"name": "Hack"}, token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestUpdateInstanceConfig_InvalidRegistrationMode_Returns400(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.getUserRoleFn = func(_ context.Context, _ string) (string, error) { return "owner", nil }
	router := instanceRouter(store)
	rr := putServerJSON(router, "/", map[string]string{"registrationMode": "banana"}, token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- GET /instance/members ----------

func TestListMembers_ReturnsAllUsers(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.listMembersFn = func(_ context.Context) ([]models.Member, error) {
		return []models.Member{
			{ID: "u1", Username: "alice", DisplayName: "Alice", Role: "owner"},
			{ID: "u2", Username: "bob", DisplayName: "Bob", Role: "member"},
		}, nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/members", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var members []models.Member
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	require.Len(t, members, 2)
	assert.Equal(t, "alice", members[0].Username)
	assert.Equal(t, "owner", members[0].Role)
	assert.Equal(t, "bob", members[1].Username)
}

func TestListMembers_EmptyList_ReturnsEmptyArray(t *testing.T) {
	userID := uuid.New().String()
	store := &mockStore{}
	token := makeAuth(store, userID)
	store.listMembersFn = func(_ context.Context) ([]models.Member, error) {
		return nil, nil
	}
	router := instanceRouter(store)
	rr := getServer(router, "/members", token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var members []models.Member
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&members))
	assert.Empty(t, members)
}

func TestListMembers_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	router := instanceRouter(store)
	rr := getServer(router, "/members", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
