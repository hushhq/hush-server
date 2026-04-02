package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/models"
)

const adminSessionCookieName = "hush_admin_session"

type adminBootstrapStatusResponse struct {
	BootstrapAvailable bool `json:"bootstrapAvailable"`
	HasAdmins          bool `json:"hasAdmins"`
	HasBootstrapSecret bool `json:"hasBootstrapSecret"`
}

type adminBootstrapClaimRequest struct {
	BootstrapSecret string  `json:"bootstrapSecret"`
	Username        string  `json:"username"`
	Email           *string `json:"email"`
	Password        string  `json:"password"`
}

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminSessionResponse struct {
	Admin models.InstanceAdmin `json:"admin"`
}

// RequireAdminSession validates the dashboard session cookie and injects admin identity.
func RequireAdminSession(store adminStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionToken, err := readAdminSessionCookie(r)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			session, err := store.GetInstanceAdminSessionByTokenHash(r.Context(), auth.TokenHash(sessionToken))
			if err != nil || session == nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			admin, err := store.GetInstanceAdminByID(r.Context(), session.AdminID)
			if err != nil || admin == nil || !admin.IsActive {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			_ = store.UpdateInstanceAdminSessionLastSeen(r.Context(), session.ID, time.Now().UTC())
			ctx := withAdminID(r.Context(), admin.ID)
			ctx = withAdminRole(ctx, admin.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminOrigin rejects mutating admin requests from unexpected origins.
func RequireAdminOrigin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin == "" || !sameOrigin(origin, r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid admin request origin"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdminRole ensures the authenticated admin has one of the allowed roles.
func RequireAdminRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[adminRoleFromContext(r.Context())]; !ok {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient admin role"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *adminHandler) bootstrapStatus(w http.ResponseWriter, r *http.Request) {
	adminCount, err := h.store.CountInstanceAdmins(r.Context())
	if err != nil {
		slog.Error("admin bootstrapStatus", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read admin bootstrap status"})
		return
	}
	writeJSON(w, http.StatusOK, adminBootstrapStatusResponse{
		BootstrapAvailable: adminCount == 0 && h.bootstrapSecret != "",
		HasAdmins:          adminCount > 0,
		HasBootstrapSecret: h.bootstrapSecret != "",
	})
}

func (h *adminHandler) bootstrapClaim(w http.ResponseWriter, r *http.Request) {
	var req adminBootstrapClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.BootstrapSecret)), []byte(h.bootstrapSecret)) != 1 || h.bootstrapSecret == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid bootstrap secret"})
		return
	}
	adminCount, err := h.store.CountInstanceAdmins(r.Context())
	if err != nil {
		slog.Error("admin bootstrapClaim count", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read admin bootstrap state"})
		return
	}
	if adminCount > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "bootstrap already completed"})
		return
	}

	admin, err := h.createAdmin(r.Context(), req.Username, req.Email, req.Password, "owner")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if _, err := h.ensureServiceIdentity(r.Context()); err != nil {
		slog.Warn("admin bootstrapClaim: service identity not provisioned", "err", err)
	}
	if err := h.issueAdminSession(w, r, admin); err != nil {
		slog.Error("admin bootstrapClaim session", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create admin session"})
		return
	}
	writeJSON(w, http.StatusCreated, adminSessionResponse{Admin: *admin})
}

func (h *adminHandler) login(w http.ResponseWriter, r *http.Request) {
	var req adminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	username := strings.TrimSpace(req.Username)
	password := req.Password
	admin, err := h.store.GetInstanceAdminByUsername(r.Context(), username)
	if err != nil {
		slog.Error("admin login", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	if admin == nil || !admin.IsActive {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	valid, err := auth.VerifyAdminPassword(password, admin.PasswordHash)
	if err != nil {
		slog.Error("admin login verify password", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	if !valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	if err := h.issueAdminSession(w, r, admin); err != nil {
		slog.Error("admin login issue session", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
		return
	}
	writeJSON(w, http.StatusOK, adminSessionResponse{Admin: *admin})
}

func (h *adminHandler) logout(w http.ResponseWriter, r *http.Request) {
	if sessionToken, err := readAdminSessionCookie(r); err == nil {
		session, err := h.store.GetInstanceAdminSessionByTokenHash(r.Context(), auth.TokenHash(sessionToken))
		if err == nil && session != nil {
			_ = h.store.DeleteInstanceAdminSessionByID(r.Context(), session.ID)
		}
	}
	clearAdminSessionCookie(w, h.secureCookies)
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) me(w http.ResponseWriter, r *http.Request) {
	admin, err := h.store.GetInstanceAdminByID(r.Context(), adminIDFromContext(r.Context()))
	if err != nil || admin == nil || !admin.IsActive {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, adminSessionResponse{Admin: *admin})
}

func readAdminSessionCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

func setAdminSessionCookie(w http.ResponseWriter, value string, secure bool, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expiresAt,
	})
}

func clearAdminSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func sameOrigin(origin string, r *http.Request) bool {
	if origin == "" {
		return false
	}
	return strings.EqualFold(strings.TrimRight(origin, "/"), requestOrigin(r))
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return scheme + "://" + r.Host
}

func (h *adminHandler) issueAdminSession(w http.ResponseWriter, r *http.Request, admin *models.InstanceAdmin) error {
	sessionToken, err := auth.GenerateSessionToken()
	if err != nil {
		return err
	}
	sessionID := uuid.NewString()
	expiresAt := time.Now().UTC().Add(h.sessionTTL)
	createdIP := clientAddress(r)
	userAgent := strings.TrimSpace(r.UserAgent())
	var createdIPPtr *string
	var userAgentPtr *string
	if createdIP != "" {
		createdIPPtr = &createdIP
	}
	if userAgent != "" {
		userAgentPtr = &userAgent
	}
	if _, err := h.store.CreateInstanceAdminSession(
		r.Context(),
		sessionID,
		admin.ID,
		auth.TokenHash(sessionToken),
		expiresAt,
		createdIPPtr,
		userAgentPtr,
	); err != nil {
		return err
	}
	if err := h.store.TouchInstanceAdminLastLogin(r.Context(), admin.ID, time.Now().UTC()); err != nil {
		slog.Warn("admin session last login update failed", "admin_id", admin.ID, "err", err)
	}
	setAdminSessionCookie(w, sessionToken, h.secureCookies, expiresAt)
	lastLoginAt := time.Now().UTC()
	admin.LastLoginAt = &lastLoginAt
	return nil
}

func (h *adminHandler) createAdmin(
	ctx context.Context,
	username string,
	email *string,
	password string,
	role string,
) (*models.InstanceAdmin, error) {
	normalizedUsername := strings.TrimSpace(username)
	if err := validateUsername(normalizedUsername); err != nil {
		return nil, err
	}
	normalizedEmail, err := normalizeAdminEmail(email)
	if err != nil {
		return nil, err
	}
	if len(password) < 12 {
		return nil, errBadRequest("password must be at least 12 characters")
	}
	passwordHash, err := auth.HashAdminPassword(password)
	if err != nil {
		return nil, err
	}
	admin, err := h.store.CreateInstanceAdmin(ctx, normalizedUsername, normalizedEmail, passwordHash, role)
	if err != nil {
		slog.Error("create instance admin", "err", err)
		return nil, errBadRequest("failed to create admin account")
	}
	return admin, nil
}

func normalizeAdminEmail(email *string) (*string, error) {
	if email == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*email)
	if trimmed == "" {
		return nil, nil
	}
	address, err := mail.ParseAddress(trimmed)
	if err != nil {
		return nil, errBadRequest("invalid email")
	}
	normalized := strings.ToLower(strings.TrimSpace(address.Address))
	return &normalized, nil
}

type adminRequestError struct {
	message string
}

func (e *adminRequestError) Error() string {
	return e.message
}

func errBadRequest(message string) error {
	return &adminRequestError{message: message}
}

func clientAddress(r *http.Request) string {
	forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwardedFor != "" {
		return strings.TrimSpace(strings.Split(forwardedFor, ",")[0])
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func encodePublicKeyBase64(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}
