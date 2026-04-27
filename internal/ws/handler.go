package ws

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/db"

	"github.com/gorilla/websocket"
)

var errFederationUnsupported = errors.New("federation is not supported in this MVP")

// Handler returns an HTTP handler that upgrades to WebSocket and runs the client.
// Validates JWT (and session if pool is non-nil) from query param or first message.
// corsOrigin controls the WebSocket origin check: "*" allows all origins,
// otherwise only requests whose Origin header matches corsOrigin are accepted.
func Handler(hub *Hub, jwtSecret string, store db.Store, corsOrigin string) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if corsOrigin == "*" {
				return true
			}
			origin := r.Header.Get("Origin")
			if origin != corsOrigin {
				slog.Warn("ws upgrade rejected: origin mismatch", "origin", origin, "expected", corsOrigin)
				return false
			}
			return true
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			userID, deviceID, federatedID, err := authFromFirstMessage(conn, jwtSecret, store, r)
			if err != nil {
				closeReason := "auth required"
				if errors.Is(err, errFederationUnsupported) {
					closeReason = errFederationUnsupported.Error()
				}
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, closeReason))
				_ = conn.Close()
				return
			}
			msgHandler := NewMessageHandler(store, hub)
			client := NewClient(conn, hub, userID, deviceID, federatedID, msgHandler)
			hub.Register(client)
			client.Run()
			return
		}
		userID, sessionID, deviceID, isGuest, isFederated, federatedIdentityID, err := auth.ValidateJWT(token, jwtSecret)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if isFederated {
			http.Error(w, errFederationUnsupported.Error(), http.StatusForbidden)
			return
		}
		if store != nil && !isGuest {
			tokenHash := auth.TokenHash(token)
			sess, err := store.GetSessionByTokenHash(r.Context(), tokenHash)
			if err != nil || sess == nil || sess.ID != sessionID || sess.UserID != userID {
				http.Error(w, "session invalid or expired", http.StatusUnauthorized)
				return
			}
			// Device-revoke enforcement: refuse the upgrade if this
			// token's device has been revoked.
			if deviceID != "" {
				active, err := store.IsDeviceActive(r.Context(), userID, deviceID)
				if err != nil || !active {
					http.Error(w, "device revoked", http.StatusUnauthorized)
					return
				}
			}
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		var fedID string
		if isFederated {
			fedID = federatedIdentityID
		}
		msgHandler := NewMessageHandler(store, hub)
		client := NewClient(conn, hub, userID, deviceID, fedID, msgHandler)
		hub.Register(client)
		client.Run()
	}
}

func authFromFirstMessage(conn *websocket.Conn, jwtSecret string, store db.Store, r *http.Request) (userID string, deviceID string, federatedIdentityID string, err error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return "", "", "", err
	}
	var msg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", "", "", err
	}
	if msg.Type != "auth" || msg.Token == "" {
		return "", "", "", errors.New("invalid auth message: expected type 'auth' with non-empty token")
	}
	uid, sessionID, did, isGuest, isFederated, _, err := auth.ValidateJWT(msg.Token, jwtSecret)
	if err != nil {
		return "", "", "", err
	}
	if isFederated {
		return "", "", "", errFederationUnsupported
	}
	// Guest sessions are ephemeral - no DB session record exists.
	if store != nil && !isGuest {
		tokenHash := auth.TokenHash(msg.Token)
		sess, err := store.GetSessionByTokenHash(r.Context(), tokenHash)
		if err != nil {
			return "", "", "", err
		}
		if sess == nil || sess.ID != sessionID || sess.UserID != uid {
			return "", "", "", errors.New("session invalid or expired")
		}
		// Device-revoke enforcement.
		if did != "" {
			active, err := store.IsDeviceActive(r.Context(), uid, did)
			if err != nil || !active {
				return "", "", "", errors.New("device revoked")
			}
		}
	}
	return uid, did, "", nil
}
