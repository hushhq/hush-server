package ws

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait         = 10 * time.Second
	pongWait          = 60 * time.Second
	pingPeriod        = (pongWait * 9) / 10
	maxMessageSize    = 512 * 1024
	maxMediaKeyPayload = 4096
)

// Client is a single WebSocket connection.
type Client struct {
	id      string
	userID  string
	hub     *Hub
	conn    *websocket.Conn
	send    chan []byte
	handler *MessageHandler
}

// Run runs the read and write pumps. Call after Register. Blocks until connection closes.
func (c *Client) Run() {
	defer func() {
		c.hub.Unregister(c)
		_ = c.conn.Close()
	}()
	go c.writePump()
	c.readPump()
}

func (c *Client) readPump() {
	defer close(c.send)
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				slog.Debug("ws read error", "err", err)
			}
			return
		}
		c.handleMessage(raw)
	}
}

// handleMessage routes a single incoming JSON message for this client.
func (c *Client) handleMessage(raw []byte) {
	var msg struct {
		Type         string `json:"type"`
		ChannelID    string `json:"channel_id"`
		TargetUserID string `json:"target_user_id"`
		Token        string `json:"token"`
		Payload      string `json:"payload"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	switch msg.Type {
	case "subscribe":
		if msg.ChannelID != "" {
			c.hub.Subscribe(c, msg.ChannelID)
		}
	case "unsubscribe":
		if msg.ChannelID != "" {
			c.hub.Unsubscribe(c, msg.ChannelID)
		}
	case "message.send", "message.history", "typing.start", "typing.stop":
		if c.handler != nil {
			c.handler.Handle(c, msg.Type, raw)
		}
	case "media.key":
		if msg.TargetUserID == "" || msg.TargetUserID == c.userID {
			return
		}
		if len(msg.Payload) > maxMediaKeyPayload {
			return
		}
		out, err := json.Marshal(map[string]interface{}{
			"type":           "media.key",
			"sender_user_id": c.userID,
			"payload":        msg.Payload,
		})
		if err != nil {
			return
		}
		c.hub.BroadcastToUser(msg.TargetUserID, out)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case data, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// NewClient creates a client. id and userID are set by the handler after auth.
// msgHandler may be nil; when set, it handles message.send, message.history, typing.*.
func NewClient(conn *websocket.Conn, hub *Hub, userID string, msgHandler *MessageHandler) *Client {
	return &Client{
		id:      uuid.New().String(),
		userID:  userID,
		hub:     hub,
		conn:    conn,
		send:    make(chan []byte, 256),
		handler: msgHandler,
	}
}
