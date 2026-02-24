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

	"hush.app/server/internal/auth"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testJWTSecret = "test-secret"
)

var testJWTExpiry = 1 * time.Hour

// ---------- helpers ----------

func newTestRouter(store *mockStore) http.Handler {
	return AuthRoutes(store, testJWTSecret, testJWTExpiry)
}

func postJSON(handler http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getWithAuth(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func postWithAuth(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeAuthResponse(t *testing.T, rr *httptest.ResponseRecorder) models.AuthResponse {
	t.Helper()
	var resp models.AuthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	return resp
}

func decodeErrorResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	return resp
}

func defaultCreateUser() func(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error) {
	return func(_ context.Context, username, displayName string, _ *string) (*models.User, error) {
		return &models.User{
			ID:          uuid.New().String(),
			Username:    username,
			DisplayName: displayName,
			CreatedAt:   time.Now(),
		}, nil
	}
}

// ---------- Register ----------

func TestRegister_ValidInput_Returns200(t *testing.T) {
	store := &mockStore{createUserFn: defaultCreateUser()}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:    "alice",
		Password:    "securepass",
		DisplayName: "Alice",
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "alice", resp.User.Username)
	assert.Equal(t, "Alice", resp.User.DisplayName)
}

func TestRegister_DuplicateUsername_Returns409(t *testing.T) {
	store := &mockStore{
		createUserFn: func(_ context.Context, _, _ string, _ *string) (*models.User, error) {
			return nil, errors.New("duplicate key value violates unique constraint")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username: "alice",
		Password: "securepass",
	})

	assert.Equal(t, http.StatusConflict, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "username already taken")
}

func TestRegister_ShortPassword_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username: "alice",
		Password: "short",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "password must be at least 8 characters")
}

func TestRegister_EmptyUsername_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username: "",
		Password: "securepass",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "username is required")
}

func TestRegister_InvalidUsernameChars_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	invalidNames := []string{"user name", "user@name", "user!name", "user name"}
	for _, name := range invalidNames {
		rr := postJSON(router, "/register", models.RegisterRequest{
			Username: name,
			Password: "securepass",
		})

		assert.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for username %q", name)
		resp := decodeErrorResponse(t, rr)
		assert.Contains(t, resp["error"], "username may only contain")
	}
}

// ---------- Login ----------

func TestLogin_ValidCredentials_Returns200(t *testing.T) {
	hash, err := auth.HashPassword("securepass")
	require.NoError(t, err)

	userID := uuid.New().String()
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, _ string) (*models.User, error) {
			return &models.User{
				ID:           userID,
				Username:     "alice",
				PasswordHash: &hash,
				DisplayName:  "Alice",
				CreatedAt:    time.Now(),
			}, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/login", models.LoginRequest{
		Username: "alice",
		Password: "securepass",
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "alice", resp.User.Username)
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	hash, err := auth.HashPassword("securepass")
	require.NoError(t, err)

	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, _ string) (*models.User, error) {
			return &models.User{
				ID:           uuid.New().String(),
				Username:     "alice",
				PasswordHash: &hash,
				DisplayName:  "Alice",
			}, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/login", models.LoginRequest{
		Username: "alice",
		Password: "wrongpassword",
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "invalid username or password")
}

func TestLogin_NonexistentUser_Returns401(t *testing.T) {
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, _ string) (*models.User, error) {
			return nil, errors.New("not found")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/login", models.LoginRequest{
		Username: "ghost",
		Password: "securepass",
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "invalid username or password")
}

func TestLogin_OAuthUserNoPassword_Returns401(t *testing.T) {
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, _ string) (*models.User, error) {
			return &models.User{
				ID:           uuid.New().String(),
				Username:     "oauth_user",
				PasswordHash: nil,
				DisplayName:  "OAuth User",
			}, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/login", models.LoginRequest{
		Username: "oauth_user",
		Password: "securepass",
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "invalid username or password")
}

// ---------- Guest ----------

func TestGuest_CreatesGuestUser_Returns200(t *testing.T) {
	store := &mockStore{createUserFn: defaultCreateUser()}
	router := newTestRouter(store)

	rr := postJSON(router, "/guest", nil)

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.True(t, strings.HasPrefix(resp.User.Username, "guest_"),
		"expected username to start with guest_, got %q", resp.User.Username)
}

// ---------- ValidateUsername (unit) ----------

func TestValidateUsername_ValidInput_ReturnsNil(t *testing.T) {
	validNames := []string{
		"alice",
		"Alice123",
		"user.name",
		"user_name",
		"user-name",
		"a",
		"A1._-b",
	}
	for _, name := range validNames {
		assert.NoError(t, validateUsername(name), "expected nil for %q", name)
	}
}

func TestValidateUsername_Empty_ReturnsError(t *testing.T) {
	err := validateUsername("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "username is required")
}

func TestValidateUsername_TooLong_ReturnsError(t *testing.T) {
	long := strings.Repeat("a", 129)
	err := validateUsername(long)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "username too long")
}

func TestValidateUsername_InvalidChars_ReturnsError(t *testing.T) {
	invalidNames := []string{
		"has space",
		"has@at",
		"has!bang",
		"has#hash",
		"has$dollar",
	}
	for _, name := range invalidNames {
		err := validateUsername(name)
		assert.Error(t, err, "expected error for %q", name)
		assert.Contains(t, err.Error(), "username may only contain")
	}
}
