package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"
)

const (
	messageHistoryLimitMax = 50
	handlerTimeout         = 10 * time.Second
)

// MessageHandler handles application WebSocket message types (message.send, message.history, typing.*).
type MessageHandler struct {
	store db.Store
	hub   *Hub
}

// NewMessageHandler returns a handler that uses store and hub.
func NewMessageHandler(store db.Store, hub *Hub) *MessageHandler {
	return &MessageHandler{store: store, hub: hub}
}

// Handle processes a single incoming message by type. Raw is the full JSON payload.
func (h *MessageHandler) Handle(c *Client, msgType string, raw []byte) {
	switch msgType {
	case "message.send":
		h.handleMessageSend(c, raw)
	case "message.history":
		h.handleMessageHistory(c, raw)
	case "typing.start", "typing.stop":
		h.handleTyping(c, msgType, raw)
	default:
		// subscribe/unsubscribe handled in readPump
		return
	}
}

func (h *MessageHandler) handleMessageSend(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID  string `json:"channel_id"`
		Ciphertext  string `json:"ciphertext"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" || payload.Ciphertext == "" {
		sendError(c, "bad_request", "channel_id and ciphertext required")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil {
		slog.Warn("ws IsChannelMember failed", "err", err, "channelID", payload.ChannelID)
		sendError(c, "internal", "check membership failed")
		return
	}
	if !ok {
		sendError(c, "forbidden", "not a channel member")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		sendError(c, "bad_request", "invalid ciphertext base64")
		return
	}
	msg, err := h.store.InsertMessage(ctx, payload.ChannelID, c.userID, ciphertext)
	if err != nil {
		slog.Warn("ws InsertMessage failed", "err", err)
		sendError(c, "internal", "failed to store message")
		return
	}
	out := map[string]interface{}{
		"type":       "message.new",
		"id":         msg.ID,
		"channel_id": msg.ChannelID,
		"sender_id":  msg.SenderID,
		"ciphertext": base64.StdEncoding.EncodeToString(msg.Ciphertext),
		"timestamp":  msg.Timestamp.Format(time.RFC3339Nano),
	}
	b, _ := json.Marshal(out)
	h.hub.Broadcast(payload.ChannelID, b, c.id)
}

func (h *MessageHandler) handleMessageHistory(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID string `json:"channel_id"`
		Before    string `json:"before"`
		Limit     int    `json:"limit"`
	}
	_ = json.Unmarshal(raw, &payload)
	if payload.ChannelID == "" {
		sendError(c, "bad_request", "channel_id required")
		return
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = messageHistoryLimitMax
	}
	if limit > messageHistoryLimitMax {
		limit = messageHistoryLimitMax
	}
	var before time.Time
	if payload.Before != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, payload.Before)
		if err != nil {
			before, _ = time.Parse(time.RFC3339, payload.Before)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil {
		slog.Warn("ws IsChannelMember failed", "err", err)
		sendError(c, "internal", "check membership failed")
		return
	}
	if !ok {
		sendError(c, "forbidden", "not a channel member")
		return
	}
	messages, err := h.store.GetMessages(ctx, payload.ChannelID, before, limit)
	if err != nil {
		slog.Warn("ws GetMessages failed", "err", err)
		sendError(c, "internal", "failed to load history")
		return
	}
	items := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		items = append(items, messageToMap(&m))
	}
	resp := map[string]interface{}{
		"type":     "message.history.response",
		"messages": items,
	}
	b, _ := json.Marshal(resp)
	select {
	case c.send <- b:
	default:
		slog.Warn("ws client send buffer full", "clientID", c.id)
	}
}

func (h *MessageHandler) handleTyping(c *Client, msgType string, raw []byte) {
	var payload struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		return
	}
	if h.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil || !ok {
		return
	}
	out := map[string]interface{}{
		"type":       msgType,
		"channel_id": payload.ChannelID,
		"user_id":    c.userID,
	}
	b, _ := json.Marshal(out)
	h.hub.Broadcast(payload.ChannelID, b, "")
}

func messageToMap(m *models.Message) map[string]interface{} {
	return map[string]interface{}{
		"id":         m.ID,
		"channelId":  m.ChannelID,
		"senderId":   m.SenderID,
		"ciphertext": base64.StdEncoding.EncodeToString(m.Ciphertext),
		"timestamp":  m.Timestamp.Format(time.RFC3339Nano),
	}
}

func sendError(c *Client, code, message string) {
	b, _ := json.Marshal(map[string]string{"type": "error", "code": code, "message": message})
	select {
	case c.send <- b:
	default:
		slog.Warn("ws client send buffer full", "clientID", c.id)
	}
}
