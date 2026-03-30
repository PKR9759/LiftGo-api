package ws

import (
	"encoding/json"
	"log/slog"
)

type LocationMessage struct {
	BookingID string  `json:"booking_id"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Timestamp int64   `json:"timestamp"`
}

type RoomMessage struct {
	BookingID string
	Data      []byte
}

type Hub struct {
	rooms map[string]map[*Client]bool

	broadcast       chan LocationMessage
	broadcastToRoom chan RoomMessage

	register chan *Client

	unregister chan *Client
}

func NewHub() *Hub {
	return &Hub{
		broadcast:       make(chan LocationMessage),
		broadcastToRoom: make(chan RoomMessage),
		register:        make(chan *Client),
		unregister:      make(chan *Client),
		rooms:           make(map[string]map[*Client]bool),
	}
}

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
				data, err := json.Marshal(msg)
				if err != nil {
					slog.Error("failed to marshal location broadcast", "error", err, "booking_id", msg.BookingID)
					continue
				}

				for client := range room {
					if client.role == "driver" {
						continue
					}

					select {
					case client.send <- data:
					default:
						slog.Warn("rider client buffer full, dropping connection", "user_id", client.userID, "booking_id", msg.BookingID)
						close(client.send)
						delete(room, client)
					}
				}

				if len(room) == 0 {
					delete(h.rooms, msg.BookingID)
				}
			}

		case msg := <-h.broadcastToRoom:
			if room, ok := h.rooms[msg.BookingID]; ok {
				slog.Debug("WS Hub: Broadcasting room message", "clients", len(room), "booking_id", msg.BookingID)
				for client := range room {
					select {
					case client.send <- msg.Data:
					default:
						slog.Warn("client buffer full, dropping connection", "user_id", client.userID, "booking_id", msg.BookingID)
						close(client.send)
						delete(room, client)
					}
				}
				if len(room) == 0 {
					delete(h.rooms, msg.BookingID)
				}
			}
		}
	}
}

func (h *Hub) BroadcastToRoom(bookingID string, data []byte) {
	h.broadcastToRoom <- RoomMessage{
		BookingID: bookingID,
		Data:      data,
	}
}
