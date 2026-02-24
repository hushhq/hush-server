package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// Hub holds all connected clients and channel subscriptions.
type Hub struct {
	mu       sync.RWMutex
	clients  map[string]*Client // clientID -> client
	channels map[string]map[string]*Client // channelID -> clientID -> client
	presence map[string]struct{} // userID -> present
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients:  make(map[string]*Client),
		channels: make(map[string]map[string]*Client),
		presence: make(map[string]struct{}),
	}
}

// Register adds a client. Caller must ensure client has valid userID.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c.id] = c
	h.presence[c.userID] = struct{}{}
	h.broadcastPresenceLocked()
}

// Unregister removes a client and broadcasts presence update.
// Only removes from presence if no other client shares the same userID.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c.id)
	for _, m := range h.channels {
		delete(m, c.id)
	}
	if !h.hasOtherClientForUser(c.userID) {
		delete(h.presence, c.userID)
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
