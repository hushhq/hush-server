package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patchJSONWithAuth performs an authenticated PATCH with a JSON body.
// Sibling of postJSONWithAuth in devices_test.go.
func patchJSONWithAuth(handler http.Handler, path, token string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- ValidateDisplayName (unit) ----------

func TestValidateDisplayName_AcceptsValid(t *testing.T) {
	cases := []string{
		"",                              // empty clears the field
		"Alice",                         // ASCII
		"Alice Cooper",                  // spaces
		"日本語ユーザー",                       // unicode CJK
		"José",                          // unicode with combining mark
		"a.b-c_d 1+2 (test) [x] {y}",    // punctuation
		strings.Repeat("a", maxDisplayLen),
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, validateDisplayName(name))
		})
	}
}

func TestValidateDisplayName_RejectsTooLong(t *testing.T) {
	tooLong := strings.Repeat("a", maxDisplayLen+1)
	err := validateDisplayName(tooLong)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too long")
}

func TestValidateDisplayName_RejectsControlChars(t *testing.T) {
	cases := map[string]string{
		"newline":         "line1\nline2",
		"tab":             "with\ttab",
		"carriage return": "with\rcr",
		"null":            "with\x00null",
		"DEL":             "with\x7fdel",
	}
	for label, input := range cases {
		t.Run(label, func(t *testing.T) {
			err := validateDisplayName(input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "control")
		})
	}
}

// ---------- PATCH /api/auth/me (integration via AuthRoutes) ----------

func TestUpdateMe_ValidDisplayName_Returns200WithUpdatedUser(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var capturedDisplayName string
	store.updateUserDisplayNameFn = func(_ context.Context, id, dn string) error {
		assert.Equal(t, userID, id)
		capturedDisplayName = dn
		return nil
	}
	store.getUserByIDFn = func(_ context.Context, id string) (*models.User, error) {
		return &models.User{
			ID:          id,
			Username:    "alice",
			DisplayName: "Alice Cooper",
			Role:        "member",
			CreatedAt:   time.Now(),
		}, nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": "Alice Cooper",
	})

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Alice Cooper", capturedDisplayName)

	var got models.User
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, userID, got.ID)
	assert.Equal(t, "Alice Cooper", got.DisplayName)
	assert.Equal(t, "alice", got.Username)
}

func TestUpdateMe_TrimsWhitespace(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var capturedDisplayName string
	store.updateUserDisplayNameFn = func(_ context.Context, _, dn string) error {
		capturedDisplayName = dn
		return nil
	}
	store.getUserByIDFn = func(_ context.Context, id string) (*models.User, error) {
		return &models.User{ID: id, Username: "alice", DisplayName: capturedDisplayName, Role: "member", CreatedAt: time.Now()}, nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": "  Alice  ",
	})

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Alice", capturedDisplayName)
}

func TestUpdateMe_EmptyStringClearsField(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	called := false
	var capturedDisplayName string
	store.updateUserDisplayNameFn = func(_ context.Context, _, dn string) error {
		called = true
		capturedDisplayName = dn
		return nil
	}
	store.getUserByIDFn = func(_ context.Context, id string) (*models.User, error) {
		return &models.User{ID: id, Username: "alice", DisplayName: "", Role: "member", CreatedAt: time.Now()}, nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": "",
	})

	require.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, called, "updateUserDisplayName should be called when displayName is present, even when empty")
	assert.Equal(t, "", capturedDisplayName)
}

func TestUpdateMe_DisplayNameOmitted_DoesNotCallStore(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	called := false
	store.updateUserDisplayNameFn = func(_ context.Context, _, _ string) error {
		called = true
		return nil
	}
	store.getUserByIDFn = func(_ context.Context, id string) (*models.User, error) {
		return &models.User{ID: id, Username: "alice", DisplayName: "Alice", Role: "member", CreatedAt: time.Now()}, nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{})

	require.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, called, "store update must not fire when displayName field is omitted")
}

func TestUpdateMe_TooLong_Returns422(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	called := false
	store.updateUserDisplayNameFn = func(_ context.Context, _, _ string) error {
		called = true
		return nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": strings.Repeat("a", maxDisplayLen+1),
	})

	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	assert.False(t, called, "store must not be touched when validation fails")
	body := decodeErrorResponse(t, rr)
	assert.Contains(t, strings.ToLower(body["error"]), "too long")
}

func TestUpdateMe_ControlChar_Returns422(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	called := false
	store.updateUserDisplayNameFn = func(_ context.Context, _, _ string) error {
		called = true
		return nil
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": "Alice\nCooper",
	})

	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	assert.False(t, called)
	body := decodeErrorResponse(t, rr)
	assert.Contains(t, strings.ToLower(body["error"]), "control")
}

func TestUpdateMe_MalformedJSON_Returns400(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	req := httptest.NewRequest(http.MethodPatch, "/me", strings.NewReader("not json {{{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	newTestRouter(store).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateMe_NoAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	req := httptest.NewRequest(http.MethodPatch, "/me", bytes.NewReader([]byte(`{"displayName":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	newTestRouter(store).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestUpdateMe_StoreError_Returns500(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	store.updateUserDisplayNameFn = func(_ context.Context, _, _ string) error {
		return errors.New("db unavailable")
	}

	rr := patchJSONWithAuth(newTestRouter(store), "/me", token, map[string]any{
		"displayName": "Alice",
	})

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}
