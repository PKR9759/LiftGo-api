// internal/ws/client.go
package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 54 * time.Second
	maxMessageSize = 4096
)

var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	bookingID string
	role      string
	userID    string
}

func (c *Client) ReadPump() {
	defer func() {
		slog.Info("websocket client disconnected (read pump)", "booking_id", c.bookingID, "user_id", c.userID, "role", c.role)
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
				slog.Error("websocket read error", "error", err, "booking_id", c.bookingID, "user_id", c.userID)
			}
			break
		}

		slog.Debug("raw message parsed", "booking_id", c.bookingID, "user_id", c.userID, "message", string(message))

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &baseMsg); err != nil {
			slog.Warn("malformed websocket message", "error", err, "booking_id", c.bookingID, "user_id", c.userID)
			continue
		}

		if baseMsg.Type == "message" {
			var chatMsg struct {
				Type string `json:"type"`
				Text string `json:"text"`
				From string `json:"from"`
			}
			if err := json.Unmarshal(message, &chatMsg); err == nil {
				chatMsg.From = c.role
				msgBytes, _ := json.Marshal(chatMsg)

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
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				slog.Error("websocket write error", "error", err, "booking_id", c.bookingID, "user_id", c.userID)
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
