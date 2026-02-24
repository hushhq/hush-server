package ws

import (
	"encoding/json"
	"errors"
	"net/http"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/db"

	"github.com/gorilla/websocket"
)

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
			return r.Header.Get("Origin") == corsOrigin
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			userID, err := authFromFirstMessage(conn, jwtSecret, store, r)
			if err != nil {
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "auth required"))
				_ = conn.Close()
				return
			}
			msgHandler := NewMessageHandler(store, hub)
			client := NewClient(conn, hub, userID, msgHandler)
			hub.Register(client)
			client.Run()
			return
		}
		userID, sessionID, err := auth.ValidateJWT(token, jwtSecret)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if store != nil {
			tokenHash := auth.TokenHash(token)
			sess, err := store.GetSessionByTokenHash(r.Context(), tokenHash)
			if err != nil || sess == nil || sess.ID != sessionID || sess.UserID != userID {
				http.Error(w, "session invalid or expired", http.StatusUnauthorized)
				return
			}
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		msgHandler := NewMessageHandler(store, hub)
		client := NewClient(conn, hub, userID, msgHandler)
		hub.Register(client)
		client.Run()
	}
}

func authFromFirstMessage(conn *websocket.Conn, jwtSecret string, store db.Store, r *http.Request) (userID string, err error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return "", err
	}
	var msg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", err
	}
	if msg.Type != "auth" || msg.Token == "" {
		return "", errors.New("invalid auth message: expected type 'auth' with non-empty token")
	}
	uid, sessionID, err := auth.ValidateJWT(msg.Token, jwtSecret)
	if err != nil {
		return "", err
	}
	if store != nil {
		tokenHash := auth.TokenHash(msg.Token)
		sess, err := store.GetSessionByTokenHash(r.Context(), tokenHash)
		if err != nil {
			return "", err
		}
		if sess == nil || sess.ID != sessionID || sess.UserID != uid {
			return "", errors.New("session invalid or expired")
		}
	}
	return uid, nil
}
