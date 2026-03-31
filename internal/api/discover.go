package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"
)

const (
	discoverPageSizeDefault = 20
	discoverPageSizeMax     = 50
	searchUsersLimit        = 20
	searchQueryMinLen       = 2
)

// GuildRoutes mounts the /api/guilds sub-router.
// Provides DM creation, guild discovery, and public user search.
// All routes require authentication.
func GuildRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &discoverHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/discover", h.discoverGuilds)
	r.Post("/dm", h.createOrFindDM)
	r.Get("/users/search", h.searchUsers)
	return r
}

type discoverHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// discoverGuilds handles GET /api/guilds/discover.
// Returns a paginated list of publicly discoverable, non-closed guilds.
// Query params: category, search, sort (members|newest), page, pageSize.
func (h *discoverHandler) discoverGuilds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	category := q.Get("category")
	search := q.Get("search")
	sort := q.Get("sort")
	if sort == "" {
		sort = "members"
	}

	page := 1
	if ps := q.Get("page"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid page"})
			return
		}
		page = n
	}

	pageSize := discoverPageSizeDefault
	if ps := q.Get("pageSize"); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pageSize"})
			return
		}
		if n > discoverPageSizeMax {
			n = discoverPageSizeMax
		}
		pageSize = n
	}

	guilds, total, err := h.store.DiscoverGuilds(r.Context(), category, search, sort, page, pageSize)
	if err != nil {
		slog.Error("discoverGuilds", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch guilds"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"guilds":   guilds,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// createOrFindDM handles POST /api/guilds/dm.
// Idempotent: returns existing DM guild if one already exists for the user pair,
// otherwise creates a new one.
func (h *discoverHandler) createOrFindDM(w http.ResponseWriter, r *http.Request) {
	callerID := userIDFromContext(r.Context())
	if callerID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req models.CreateDMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.OtherUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "otherUserId is required"})
		return
	}
	if req.OtherUserID == callerID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot create a DM with yourself"})
		return
	}

	// Verify the other user exists.
	otherUser, err := h.store.GetUserByID(r.Context(), req.OtherUserID)
	if err != nil {
		slog.Error("createOrFindDM: get other user", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up user"})
		return
	}
	if otherUser == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	otherPublic := models.UserSearchPublicResult{
		ID:          otherUser.ID,
		Username:    otherUser.Username,
		DisplayName: otherUser.DisplayName,
	}

	// Check for existing DM.
	existing, err := h.store.FindDMGuild(r.Context(), callerID, req.OtherUserID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("createOrFindDM: find dm guild", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up DM"})
		return
	}

	if existing != nil {
		// Fetch the DM channel to include channelId in the response.
		channels, err := h.store.ListChannels(r.Context(), existing.ID)
		if err != nil {
			slog.Error("createOrFindDM: list dm channels", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch DM channel"})
			return
		}
		channelID := ""
		if len(channels) > 0 {
			channelID = channels[0].ID
		}
		writeJSON(w, http.StatusOK, models.DMResponse{
			Server:    *existing,
			OtherUser: otherPublic,
			ChannelID: channelID,
		})
		return
	}

	// Create new DM guild.
	server, ch, err := h.store.CreateDMGuild(r.Context(), callerID, req.OtherUserID)
	if err != nil {
		slog.Error("createOrFindDM: create dm guild", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create DM"})
		return
	}

	// Broadcast member_joined to the new DM guild so both clients can discover it.
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_joined",
			"server_id": server.ID,
			"user_id":   callerID,
		})
		h.hub.BroadcastToServer(server.ID, msg)
	}

	writeJSON(w, http.StatusCreated, models.DMResponse{
		Server:    *server,
		OtherUser: otherPublic,
		ChannelID: ch.ID,
	})
}

// searchUsers handles GET /api/guilds/users/search.
// Returns up to 20 users matching the query on username or displayName.
// Minimum query length is 2 characters. No ban/role info is returned.
func (h *discoverHandler) searchUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < searchQueryMinLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "query must be at least 2 characters",
		})
		return
	}

	results, err := h.store.SearchUsersPublic(r.Context(), q, searchUsersLimit)
	if err != nil {
		slog.Error("searchUsers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to search users"})
		return
	}

	writeJSON(w, http.StatusOK, results)
}
