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
	messageHistoryLimitMax  = 50
	handlerTimeout          = 10 * time.Second
	maxFanoutRecipients     = 200
	maxCiphertextBytes      = 64 * 1024 // 64 KiB per ciphertext blob
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
// Muted users are blocked from message.send, typing.start, and typing.stop;
// other message types (message.history, subscribe, unsubscribe) pass through.
func (h *MessageHandler) Handle(c *Client, msgType string, raw []byte) {
	switch msgType {
	case "message.send", "typing.start", "typing.stop":
		serverID := h.resolveServerIDFromPayload(raw)
		if h.isMuted(c, serverID) {
			sendError(c, "muted", "You are muted and cannot send messages.")
			return
		}
		if msgType == "message.send" {
			h.handleMessageSend(c, raw)
		} else {
			h.handleTyping(c, msgType, raw)
		}
	case "message.history":
		h.handleMessageHistory(c, raw)
	case "mls.commit":
		h.handleMLSCommit(c, raw)
	case "mls.leave_proposal":
		h.handleMLSLeaveProposal(c, raw)
	case "mls.add_request":
		h.handleMLSAddRequest(c, raw)
	default:
		// subscribe/unsubscribe handled in readPump
		return
	}
}

// resolveServerIDFromPayload extracts channel_id from the payload and looks up
// the channel's server_id. Returns empty string on any error (fail-open).
func (h *MessageHandler) resolveServerIDFromPayload(raw []byte) string {
	if h.store == nil {
		return ""
	}
	var payload struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	ch, err := h.store.GetChannelByID(ctx, payload.ChannelID)
	if err != nil || ch == nil || ch.ServerID == nil {
		return ""
	}
	return *ch.ServerID
}

// isMuted checks whether the client's user has an active mute record in the given guild.
// Returns false on DB error (fail-open for availability).
func (h *MessageHandler) isMuted(c *Client, serverID string) bool {
	if h.store == nil || serverID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	mute, err := h.store.GetActiveMute(ctx, serverID, c.userID)
	if err != nil {
		slog.Warn("ws mute check failed", "err", err, "userID", c.userID)
		return false
	}
	return mute != nil
}

func (h *MessageHandler) handleMessageSend(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID             string            `json:"channel_id"`
		Ciphertext            string            `json:"ciphertext"`
		CiphertextByRecipient map[string]string `json:"ciphertext_by_recipient"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		sendError(c, "bad_request", "channel_id required")
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
	// Block message sends to system channels.
	ch, chErr := h.store.GetChannelByID(ctx, payload.ChannelID)
	if chErr != nil {
		slog.Warn("ws GetChannelByID failed", "err", chErr, "channelID", payload.ChannelID)
	}
	if ch != nil && ch.Type == "system" {
		sendError(c, "forbidden", "cannot send messages to system channel")
		return
	}
	if len(payload.CiphertextByRecipient) > 0 {
		if len(payload.CiphertextByRecipient) > maxFanoutRecipients {
			sendError(c, "bad_request", "too many fan-out recipients")
			return
		}
		h.handleMessageSendFanout(c, payload.ChannelID, payload.CiphertextByRecipient, ctx)
		return
	}
	if payload.Ciphertext == "" {
		sendError(c, "bad_request", "ciphertext or ciphertext_by_recipient required")
		return
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		sendError(c, "bad_request", "invalid ciphertext base64")
		return
	}
	if len(ciphertext) > maxCiphertextBytes {
		sendError(c, "bad_request", "ciphertext too large")
		return
	}
	msg, err := h.store.InsertMessage(ctx, payload.ChannelID, c.userID, nil, ciphertext)
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
	h.hub.Broadcast(payload.ChannelID, b, "")
}

func (h *MessageHandler) handleMessageSendFanout(c *Client, channelID string, ciphertextByRecipient map[string]string, ctx context.Context) {
	for recipientID, b64 := range ciphertextByRecipient {
		if recipientID == "" || b64 == "" {
			continue
		}
		ct, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			slog.Warn("ws fan-out invalid base64", "recipientID", recipientID)
			continue
		}
		if len(ct) > maxCiphertextBytes {
			slog.Warn("ws fan-out ciphertext too large", "recipientID", recipientID, "size", len(ct))
			continue
		}
		msg, err := h.store.InsertMessage(ctx, channelID, c.userID, &recipientID, ct)
		if err != nil {
			slog.Warn("ws InsertMessage fan-out failed", "err", err, "recipientID", recipientID)
			continue
		}
		out := map[string]interface{}{
			"type":       "message.new",
			"id":         msg.ID,
			"channel_id": msg.ChannelID,
			"sender_id":  msg.SenderID,
			"ciphertext": b64,
			"timestamp":  msg.Timestamp.Format(time.RFC3339Nano),
		}
		b, _ := json.Marshal(out)
		h.hub.BroadcastToUserInChannel(channelID, recipientID, b)
	}
	// Insert sender's own copy so their history includes fan-out messages.
	// Ciphertext is empty - the client caches plaintext at send time.
	senderID := c.userID
	senderCopy, err := h.store.InsertMessage(ctx, channelID, c.userID, &senderID, []byte{})
	if err != nil {
		slog.Warn("ws InsertMessage sender-copy failed", "err", err)
		return
	}
	echo := map[string]interface{}{
		"type":       "message.new",
		"id":         senderCopy.ID,
		"channel_id": channelID,
		"sender_id":  c.userID,
		"timestamp":  senderCopy.Timestamp.Format(time.RFC3339Nano),
	}
	echoBytes, _ := json.Marshal(echo)
	h.hub.BroadcastToUserInChannel(channelID, c.userID, echoBytes)
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
			before, err = time.Parse(time.RFC3339, payload.Before)
		}
		if err != nil {
			sendError(c, "bad_request", "invalid before timestamp")
			return
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
	messages, err := h.store.GetMessages(ctx, payload.ChannelID, c.userID, before, limit)
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

// handleMLSCommit processes an mls.commit WS message.
// Verifies channel membership, updates GroupInfo, queues the commit, and broadcasts
// mls.commit to all channel subscribers. This is the WS-only path (alternative to
// POST /api/mls/groups/:channelId/commit).
func (h *MessageHandler) handleMLSCommit(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID   string `json:"channel_id"`
		CommitBytes string `json:"commit_bytes"`
		GroupInfo   string `json:"group_info"`
		Epoch       int64  `json:"epoch"`
		GroupType   string `json:"group_type"` // "text" or "voice"; defaults to "text"
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		sendError(c, "bad_request", "channel_id required")
		return
	}
	groupType := payload.GroupType
	if groupType != "voice" {
		groupType = "text"
	}

	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()

	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil {
		slog.Warn("ws mls.commit IsChannelMember failed", "err", err)
		sendError(c, "internal", "check membership failed")
		return
	}
	if !ok {
		sendError(c, "forbidden", "not a channel member")
		return
	}

	commitBytes, err := base64.StdEncoding.DecodeString(payload.CommitBytes)
	if err != nil || len(commitBytes) == 0 {
		sendError(c, "bad_request", "invalid commit_bytes base64")
		return
	}
	groupInfoBytes, err := base64.StdEncoding.DecodeString(payload.GroupInfo)
	if err != nil || len(groupInfoBytes) == 0 {
		sendError(c, "bad_request", "invalid group_info base64")
		return
	}

	if err := h.store.UpsertMLSGroupInfo(ctx, payload.ChannelID, groupType, groupInfoBytes, payload.Epoch); err != nil {
		slog.Error("ws mls.commit UpsertMLSGroupInfo failed", "err", err)
		sendError(c, "internal", "failed to store group info")
		return
	}
	if err := h.store.AppendMLSCommit(ctx, payload.ChannelID, payload.Epoch, commitBytes, c.userID); err != nil {
		slog.Error("ws mls.commit AppendMLSCommit failed", "err", err)
		sendError(c, "internal", "failed to queue commit")
		return
	}

	// group_type is included so Plan 03's handleVoiceCommit can filter voice vs text commits.
	out, _ := json.Marshal(map[string]interface{}{
		"type":         "mls.commit",
		"channel_id":   payload.ChannelID,
		"epoch":        payload.Epoch,
		"commit_bytes": payload.CommitBytes,
		"sender_id":    c.userID,
		"group_type":   groupType,
	})
	h.hub.Broadcast(payload.ChannelID, out, "")
}

// handleMLSLeaveProposal processes an mls.leave_proposal WS message.
// The leaving member sends their leave proposal bytes; this handler broadcasts
// mls.add_request to all channel subscribers so an online member can commit the removal.
// The leaving member has already deleted their local group state before sending this.
func (h *MessageHandler) handleMLSLeaveProposal(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID     string `json:"channel_id"`
		ProposalBytes string `json:"proposal_bytes"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		sendError(c, "bad_request", "channel_id required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()

	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil {
		slog.Warn("ws mls.leave_proposal IsChannelMember failed", "err", err)
		sendError(c, "internal", "check membership failed")
		return
	}
	if !ok {
		sendError(c, "forbidden", "not a channel member")
		return
	}

	// Broadcast mls.add_request to channel so an online member can commit the removal.
	out, _ := json.Marshal(map[string]interface{}{
		"type":          "mls.add_request",
		"channel_id":    payload.ChannelID,
		"action":        "remove",
		"proposal_bytes": payload.ProposalBytes,
		"requester_id":  c.userID,
	})
	h.hub.Broadcast(payload.ChannelID, out, "")
}

// handleMLSAddRequest processes an mls.add_request WS message.
// Verifies channel membership and relays the request to all channel subscribers.
// Clients use this for add flows (e.g., inviting a new member by posting their
// key package add request so an online member can commit the Add).
func (h *MessageHandler) handleMLSAddRequest(c *Client, raw []byte) {
	if h.store == nil {
		sendError(c, "forbidden", "store unavailable")
		return
	}
	var payload struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ChannelID == "" {
		sendError(c, "bad_request", "channel_id required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()

	ok, err := h.store.IsChannelMember(ctx, payload.ChannelID, c.userID)
	if err != nil {
		slog.Warn("ws mls.add_request IsChannelMember failed", "err", err)
		sendError(c, "internal", "check membership failed")
		return
	}
	if !ok {
		sendError(c, "forbidden", "not a channel member")
		return
	}

	h.hub.Broadcast(payload.ChannelID, raw, c.id)
}
