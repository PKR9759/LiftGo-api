package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 54 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// Upgrader configures the WebSocket connection constraints
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// CORS is handled at the HTTP router middleware layer
		return true
	},
}

// Client acts as the middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	// Connection context
	bookingID string
	role      string
	userID    string // Set as string (UUID) per the application's actual data model, despite prompt typo
}

// ReadPump pumps messages from the websocket connection to the hub.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &baseMsg); err != nil {
			continue // Ignore malformed messages
		}

		if baseMsg.Type == "message" {
			var chatMsg struct {
				Type string `json:"type"`
				Text string `json:"text"`
				From string `json:"from"`
			}
			if err := json.Unmarshal(message, &chatMsg); err == nil {
				chatMsg.From = c.role // force the 'from' field to be the client's actual role
				msgBytes, _ := json.Marshal(chatMsg)

				// Broadcast to all clients in this booking room
				c.hub.broadcastToRoom <- RoomMessage{
					BookingID: c.bookingID,
					Data:      msgBytes,
				}
			}
		} else if baseMsg.Type == "location" && c.role == "driver" {
			var loc struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			}
			if err := json.Unmarshal(message, &loc); err == nil {
				// Construct LocationMessage and push to hub broadcast
				msg := LocationMessage{
					BookingID: c.bookingID,
					Lat:       loc.Lat,
					Lng:       loc.Lng,
					Timestamp: time.Now().UnixMilli(),
				}
				c.hub.broadcast <- msg
			}
		}
	}
}

// WritePump pumps messages from the hub to the websocket connection.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Write each message as its own frame — no batching
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
