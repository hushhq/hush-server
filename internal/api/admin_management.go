package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/go-chi/chi/v5"
)

type createInstanceAdminRequest struct {
	Username string  `json:"username"`
	Email    *string `json:"email"`
	Password string  `json:"password"`
	Role     string  `json:"role"`
}

type patchInstanceAdminRequest struct {
	Email    *string `json:"email"`
	Role     *string `json:"role"`
	IsActive *bool   `json:"isActive"`
}

type resetInstanceAdminPasswordRequest struct {
	Password string `json:"password"`
}

type serviceIdentityResponse struct {
	Configured         bool    `json:"configured"`
	Provisioned        bool    `json:"provisioned"`
	Username           *string `json:"username,omitempty"`
	PublicKey          *string `json:"publicKey,omitempty"`
	WrappingKeyVersion *string `json:"wrappingKeyVersion,omitempty"`
}

func (h *adminHandler) listAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := h.store.ListInstanceAdmins(r.Context())
	if err != nil {
		slog.Error("admin listAdmins", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list admins"})
		return
	}
	writeJSON(w, http.StatusOK, admins)
}

func (h *adminHandler) createAdminAccount(w http.ResponseWriter, r *http.Request) {
	var req createInstanceAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != "owner" && role != "admin" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be owner or admin"})
		return
	}
	admin, err := h.createAdmin(r.Context(), req.Username, req.Email, req.Password, role)
	if err != nil {
		status := http.StatusInternalServerError
		if _, ok := err.(*adminRequestError); ok {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, admin)
}

func (h *adminHandler) patchAdminAccount(w http.ResponseWriter, r *http.Request) {
	adminID := chi.URLParam(r, "adminId")
	if adminID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing admin ID"})
		return
	}
	var req patchInstanceAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	current, err := h.store.GetInstanceAdminByID(r.Context(), adminID)
	if err != nil {
		slog.Error("admin patchAdminAccount load", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load admin"})
		return
	}
	if current == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "admin not found"})
		return
	}

	email := current.Email
	if req.Email != nil {
		email, err = normalizeAdminEmail(req.Email)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	role := current.Role
	if req.Role != nil {
		if *req.Role != "owner" && *req.Role != "admin" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be owner or admin"})
			return
		}
		role = *req.Role
	}
	isActive := current.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	if err := h.ensureOwnerSafety(r.Context(), current, role, isActive); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	admin, err := h.store.UpdateInstanceAdmin(r.Context(), adminID, email, role, isActive)
	if err != nil {
		slog.Error("admin patchAdminAccount update", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update admin"})
		return
	}
	writeJSON(w, http.StatusOK, admin)
}

func (h *adminHandler) resetAdminPassword(w http.ResponseWriter, r *http.Request) {
	adminID := chi.URLParam(r, "adminId")
	if adminID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing admin ID"})
		return
	}
	var req resetInstanceAdminPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if len(req.Password) < 12 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 12 characters"})
		return
	}
	passwordHash, err := auth.HashAdminPassword(req.Password)
	if err != nil {
		slog.Error("admin resetAdminPassword hash", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reset password"})
		return
	}
	if err := h.store.UpdateInstanceAdminPassword(r.Context(), adminID, passwordHash); err != nil {
		slog.Error("admin resetAdminPassword update", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to reset password"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *adminHandler) getServiceIdentity(w http.ResponseWriter, r *http.Request) {
	response := serviceIdentityResponse{
		Configured: h.serviceIdentityMasterKey != "",
	}
	identity, err := h.store.GetInstanceServiceIdentity(r.Context())
	if err != nil {
		slog.Error("admin getServiceIdentity", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read service identity"})
		return
	}
	if identity != nil {
		response.Provisioned = true
		response.Username = &identity.Username
		publicKey := encodePublicKeyBase64(identity.PublicKey)
		response.PublicKey = &publicKey
		response.WrappingKeyVersion = &identity.WrappingKeyVersion
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *adminHandler) provisionServiceIdentity(w http.ResponseWriter, r *http.Request) {
	identity, err := h.ensureServiceIdentity(r.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if _, ok := err.(*adminRequestError); ok {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	publicKey := encodePublicKeyBase64(identity.PublicKey)
	writeJSON(w, http.StatusCreated, serviceIdentityResponse{
		Configured:         true,
		Provisioned:        true,
		Username:           &identity.Username,
		PublicKey:          &publicKey,
		WrappingKeyVersion: &identity.WrappingKeyVersion,
	})
}

func (h *adminHandler) ensureOwnerSafety(
	ctx context.Context,
	target *models.InstanceAdmin,
	nextRole string,
	nextIsActive bool,
) error {
	if target.Role != "owner" {
		return nil
	}
	if nextRole == "owner" && nextIsActive {
		return nil
	}
	admins, err := h.store.ListInstanceAdmins(ctx)
	if err != nil {
		return errBadRequest("failed to validate owner transition")
	}
	activeOwnerCount := 0
	for _, admin := range admins {
		if admin.Role == "owner" && admin.IsActive {
			activeOwnerCount++
		}
	}
	if activeOwnerCount <= 1 {
		return errBadRequest("cannot remove or deactivate the last active owner")
	}
	return nil
}

func (h *adminHandler) ensureServiceIdentity(ctx context.Context) (*models.InstanceServiceIdentity, error) {
	if h.serviceIdentityMasterKey == "" {
		return nil, errBadRequest("service identity master key is not configured")
	}
	existing, err := h.store.GetInstanceServiceIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	publicKey, privateKey, err := auth.GenerateServiceIdentity()
	if err != nil {
		return nil, err
	}
	wrappedPrivateKey, version, err := auth.WrapServiceIdentityPrivateKey([]byte(privateKey), h.serviceIdentityMasterKey)
	if err != nil {
		return nil, err
	}
	return h.store.UpsertInstanceServiceIdentity(ctx, "@instance-service", []byte(publicKey), wrappedPrivateKey, version)
}
