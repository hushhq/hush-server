package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	minPasswordLen = 8
	maxUsernameLen = 128
	maxDisplayLen  = 128
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// AuthRoutes returns the chi router for /api/auth.
func AuthRoutes(store db.Store, jwtSecret string, jwtExpiry time.Duration) chi.Router {
	r := chi.NewRouter()
	h := &authHandler{store: store, jwtSecret: jwtSecret, jwtExpiry: jwtExpiry}
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Post("/guest", h.guest)
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, store))
		r.Post("/logout", h.logout)
		r.Get("/me", h.me)
	})
	return r
}

type authHandler struct {
	store     db.Store
	jwtSecret string
	jwtExpiry time.Duration
}

func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}
	if err := validateUsername(req.Username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("hash password", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
		return
	}
	user, err := h.store.CreateUser(r.Context(), req.Username, req.DisplayName, &hash)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already taken"})
			return
		}
		slog.Error("create user", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
		return
	}
	h.sendAuthResponse(w, r, user)
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	user, err := h.store.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	if user.PasswordHash == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	if !auth.ComparePassword(*user.PasswordHash, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	h.sendAuthResponse(w, r, user)
}

func (h *authHandler) guest(w http.ResponseWriter, r *http.Request) {
	username := "guest_" + uuid.New().String()[:8]
	user, err := h.store.CreateUser(r.Context(), username, "Guest", nil)
	if err != nil {
		slog.Error("create guest user", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "guest creation failed"})
		return
	}
	h.sendAuthResponse(w, r, user)
}

func (h *authHandler) sendAuthResponse(w http.ResponseWriter, r *http.Request, user *models.User) {
	expiresAt := time.Now().Add(h.jwtExpiry)
	sessionID := uuid.New().String()
	tokenString, err := auth.SignJWT(user.ID, sessionID, h.jwtSecret, expiresAt)
	if err != nil {
		slog.Error("sign jwt", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	tokenHash := auth.TokenHash(tokenString)
	_, err = h.store.CreateSession(r.Context(), sessionID, user.ID, tokenHash, expiresAt)
	if err != nil {
		slog.Error("create session", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create session"})
		return
	}
	writeJSON(w, http.StatusOK, models.AuthResponse{Token: tokenString, User: *user})
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	sessionID := sessionIDFromContext(r.Context())
	if sessionID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	if err := h.store.DeleteSessionByID(r.Context(), sessionID); err != nil {
		slog.Error("delete session", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandler) me(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func validateUsername(s string) error {
	if s == "" {
		return errors.New("username is required")
	}
	if len(s) > maxUsernameLen {
		return errors.New("username too long")
	}
	if !usernameRE.MatchString(s) {
		return errors.New("username may only contain letters, numbers, dots, underscores, and hyphens")
	}
	return nil
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
