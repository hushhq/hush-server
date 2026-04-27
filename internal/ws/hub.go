package ws

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

// Hub holds all connected clients, channel subscriptions, and guild subscriptions.
type Hub struct {
	mu       sync.RWMutex
	clients  map[string]*Client              // clientID -> client
	channels map[string]map[string]*Client   // channelID -> clientID -> client
	servers  map[string]map[string]*Client   // serverID -> clientID -> client
	presence map[string]struct{}             // userID -> present
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients:  make(map[string]*Client),
		channels: make(map[string]map[string]*Client),
		servers:  make(map[string]map[string]*Client),
		presence: make(map[string]struct{}),
	}
}

// Register adds a client. Caller must ensure client has valid userID.
// Federated clients use "fed:<federatedIdentityID>" as their presence key to
// avoid collisions with local user IDs.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c.id] = c
	if c.federatedIdentityID != "" {
		h.presence["fed:"+c.federatedIdentityID] = struct{}{}
	} else {
		h.presence[c.userID] = struct{}{}
	}
	h.broadcastPresenceLocked()
}

// Unregister removes a client and broadcasts presence update.
// Only removes from presence if no other client shares the same identity.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c.id)
	for _, m := range h.channels {
		delete(m, c.id)
	}
	for _, m := range h.servers {
		delete(m, c.id)
	}
	if c.federatedIdentityID != "" {
		if !h.hasOtherClientForFederatedUser(c.federatedIdentityID) {
			delete(h.presence, "fed:"+c.federatedIdentityID)
		}
	} else {
		if !h.hasOtherClientForUser(c.userID) {
			delete(h.presence, c.userID)
		}
	}
	h.broadcastPresenceLocked()
}

// hasOtherClientForUser returns true if any remaining client belongs to the given userID.
// Must be called with h.mu held.
func (h *Hub) hasOtherClientForUser(userID string) bool {
	for _, client := range h.clients {
		if client.userID == userID {
			return true
		}
	}
	return false
}

// hasOtherClientForFederatedUser returns true if any remaining client belongs
// to the given federated identity ID. Must be called with h.mu held.
func (h *Hub) hasOtherClientForFederatedUser(federatedID string) bool {
	for _, client := range h.clients {
		if client.federatedIdentityID == federatedID {
			return true
		}
	}
	return false
}

// Subscribe adds the client to the channel.
func (h *Hub) Subscribe(c *Client, channelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.channels[channelID] == nil {
		h.channels[channelID] = make(map[string]*Client)
	}
	h.channels[channelID][c.id] = c
}

// Unsubscribe removes the client from the channel.
func (h *Hub) Unsubscribe(c *Client, channelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.channels[channelID]; m != nil {
		delete(m, c.id)
		if len(m) == 0 {
			delete(h.channels, channelID)
		}
	}
}

// SubscribeToServer adds the client to the server's subscriber set.
func (h *Hub) SubscribeToServer(c *Client, serverID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.servers[serverID] == nil {
		h.servers[serverID] = make(map[string]*Client)
	}
	h.servers[serverID][c.id] = c
}

// UnsubscribeFromServer removes the client from the server's subscriber set.
func (h *Hub) UnsubscribeFromServer(c *Client, serverID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.servers[serverID]; m != nil {
		delete(m, c.id)
		if len(m) == 0 {
			delete(h.servers, serverID)
		}
	}
}

// BroadcastToServer sends the message to all clients subscribed to the given server.
func (h *Hub) BroadcastToServer(serverID string, message []byte) {
	h.mu.RLock()
	m := h.servers[serverID]
	if m == nil {
		h.mu.RUnlock()
		return
	}
	clients := make([]*Client, 0, len(m))
	for _, c := range m {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// BroadcastToAll sends the message to all connected clients.
func (h *Hub) BroadcastToAll(message []byte) {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// BroadcastToUser sends the message to all connected clients for the given userID.
func (h *Hub) BroadcastToUser(userID string, message []byte) {
	h.mu.RLock()
	clients := make([]*Client, 0)
	for _, c := range h.clients {
		if c.userID == userID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// BroadcastToFederatedUser sends the message to all connected clients
// for the given federated identity ID.
func (h *Hub) BroadcastToFederatedUser(federatedIdentityID string, message []byte) {
	h.mu.RLock()
	clients := make([]*Client, 0)
	for _, c := range h.clients {
		if c.federatedIdentityID == federatedIdentityID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// BroadcastToUserInChannel sends the message to all clients for userID that are subscribed to channelID.
func (h *Hub) BroadcastToUserInChannel(channelID, userID string, message []byte) {
	h.mu.RLock()
	m := h.channels[channelID]
	if m == nil {
		h.mu.RUnlock()
		return
	}
	clients := make([]*Client, 0)
	for _, c := range m {
		if c.userID == userID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// Broadcast sends the message to all clients subscribed to the channel (except excludeClientID if non-empty).
func (h *Hub) Broadcast(channelID string, message []byte, excludeClientID string) {
	h.mu.RLock()
	m := h.channels[channelID]
	if m == nil {
		h.mu.RUnlock()
		return
	}
	clients := make([]*Client, 0, len(m))
	for id, c := range m {
		if id != excludeClientID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		select {
		case c.send <- message:
		default:
			slog.Warn("ws client send buffer full", "clientID", c.id)
		}
	}
}

// DisconnectDevice closes any active WebSocket connection bound to the
// given (userID, deviceID) pair. Called by the device-revoke handler
// so the revoked device's currently-open WS session is dropped right
// after its device key is deleted, instead of waiting for its next
// reconnect to fail authentication.
func (h *Hub) DisconnectDevice(userID, deviceID string) {
	if userID == "" || deviceID == "" {
		return
	}
	h.mu.RLock()
	targets := make([]*Client, 0)
	for _, c := range h.clients {
		if c.userID == userID && c.deviceID == deviceID {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		_ = c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "device revoked"))
		_ = c.conn.Close()
	}
}

// DisconnectUser closes all WebSocket connections for the given user ID.
// Used by kick/ban handlers to force the user offline.
func (h *Hub) DisconnectUser(userID string) {
	h.mu.RLock()
	targets := make([]*Client, 0)
	for _, c := range h.clients {
		if c.userID == userID {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		_ = c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "removed from instance"))
		_ = c.conn.Close()
	}
}

// HubStats is a point-in-time snapshot of Hub population counters used by
// operator-facing metrics endpoints. All fields are non-negative.
type HubStats struct {
	Clients          int `json:"clients"`
	PresentIdentities int `json:"presentIdentities"`
	SubscribedChannels int `json:"subscribedChannels"`
	SubscribedServers  int `json:"subscribedServers"`
}

// Stats returns the current Hub population. Cheap; takes only the read lock.
func (h *Hub) Stats() HubStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return HubStats{
		Clients:            len(h.clients),
		PresentIdentities:  len(h.presence),
		SubscribedChannels: len(h.channels),
		SubscribedServers:  len(h.servers),
	}
}

func (h *Hub) broadcastPresenceLocked() {
	userIDs := make([]string, 0, len(h.presence))
	for uid := range h.presence {
		userIDs = append(userIDs, uid)
	}
	msg, _ := json.Marshal(map[string]interface{}{
		"type":    "presence.update",
		"user_ids": userIDs,
	})
	for _, c := range h.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}
