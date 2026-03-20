package ws

import (
	"encoding/json"
)

// LocationMessage represents a coordinate update sent by the driver
type LocationMessage struct {
	BookingID string  `json:"booking_id"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Timestamp int64   `json:"timestamp"`
}

// Hub maintains the set of active clients (grouped by bookingID)
// and broadcasts messages to the clients.
type Hub struct {
	// Registered clients -> map[bookingID]map[*Client]bool
	rooms map[string]map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan LocationMessage

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client
}

// NewHub creates a new Hub pointer
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan LocationMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rooms:      make(map[string]map[*Client]bool),
	}
}

// Run executes the hub's main loop routing messages and managing connections
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			if h.rooms[client.bookingID] == nil {
				h.rooms[client.bookingID] = make(map[*Client]bool)
			}
			h.rooms[client.bookingID][client] = true

		case client := <-h.unregister:
			if room, ok := h.rooms[client.bookingID]; ok {
				if _, exists := room[client]; exists {
					delete(room, client)
					close(client.send)
					if len(room) == 0 {
						delete(h.rooms, client.bookingID)
					}
				}
			}

		case msg := <-h.broadcast:
			if room, ok := h.rooms[msg.BookingID]; ok {
				// Encode payload once
				data, err := json.Marshal(msg)
				if err != nil {
					continue
				}

				for client := range room {
					// Only send location updates to riders
					if client.role == "driver" {
						continue
					}

					select {
					case client.send <- data:
					default:
						// Rider's buffer is full — connection is stalling, disconnect them
						close(client.send)
						delete(room, client)
					}
				}

				// Clean up room if it became empty following disconnects
				if len(room) == 0 {
					delete(h.rooms, msg.BookingID)
				}
			}
		}
	}
}
