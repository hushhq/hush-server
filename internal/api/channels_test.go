package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func channelsRouter(store *mockStore) http.Handler {
	return ChannelRoutes(store, testJWTSecret)
}

func getChannelMessages(handler http.Handler, channelID, token string, before string, limit string) *httptest.ResponseRecorder {
	path := "/" + channelID + "/messages"
	if before != "" || limit != "" {
		path += "?"
		if before != "" {
			path += "before=" + before
		}
		if limit != "" {
			if before != "" {
				path += "&"
			}
			path += "limit=" + limit
		}
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestChannelsGetMessages_ValidRequest_Returns200(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, err := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)
	tokenHash := auth.TokenHash(token)
	channelID := uuid.New().String()
	msgs := []models.Message{
		{ID: "m1", ChannelID: channelID, SenderID: userID, Ciphertext: []byte("ct"), Timestamp: time.Now()},
	}
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		isChannelMemberFn: func(_ context.Context, chID, uID string) (bool, error) {
			return chID == channelID && uID == userID, nil
		},
		getMessagesFn: func(_ context.Context, chID, recID string, before time.Time, limit int) ([]models.Message, error) {
			if chID != channelID {
				return nil, nil
			}
			return msgs, nil
		},
	}
	router := channelsRouter(store)
	rr := getChannelMessages(router, channelID, token, "", "")
	assert.Equal(t, http.StatusOK, rr.Code)
	var out []struct {
		ID         string `json:"id"`
		ChannelID  string `json:"channelId"`
		SenderID   string `json:"senderId"`
		Ciphertext string `json:"ciphertext"`
	}
	require.NoError(t, jsonDecode(rr.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "m1", out[0].ID)
	assert.Equal(t, channelID, out[0].ChannelID)
	assert.Equal(t, userID, out[0].SenderID)
}

func TestChannelsGetMessages_NotMember_Returns403(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		isChannelMemberFn: func(context.Context, string, string) (bool, error) {
			return false, nil
		},
	}
	router := channelsRouter(store)
	rr := getChannelMessages(router, uuid.New().String(), token, "", "")
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestChannelsGetMessages_InvalidLimit_Returns400(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	router := channelsRouter(store)
	rr := getChannelMessages(router, uuid.New().String(), token, "", "not-a-number")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChannelsGetMessages_InvalidBefore_Returns400(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
	}
	router := channelsRouter(store)
	rr := getChannelMessages(router, uuid.New().String(), token, "not-a-timestamp", "")
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChannelsGetMessages_MissingAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	router := channelsRouter(store)
	rr := getChannelMessages(router, uuid.New().String(), "", "", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func jsonDecode(b []byte, v interface{}) error {
	return json.NewDecoder(bytes.NewReader(b)).Decode(v)
}
