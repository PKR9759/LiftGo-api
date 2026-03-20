package ws

import (
	"errors"
	"log"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	hub       *Hub
	db        *pgxpool.Pool
	jwtSecret []byte
}

func NewHandler(hub *Hub, db *pgxpool.Pool, jwtSecret []byte) *Handler {
	return &Handler{
		hub:       hub,
		db:        db,
		jwtSecret: jwtSecret,
	}
}

// getUserID parses the JWT from the `token` query parameter
func (h *Handler) getUserID(r *http.Request) (string, error) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		return "", errors.New("missing token query parameter")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &auth.Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return h.jwtSecret, nil
	})

	if err != nil {
		return "", err
	}

	claims, ok := token.Claims.(*auth.Claims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token claims")
	}

	return claims.UserID, nil
}

// DriverWS handles WebSocket connections for drivers
func (h *Handler) DriverWS(w http.ResponseWriter, r *http.Request) {
	bookingID := chi.URLParam(r, "bookingID")
	userID, err := h.getUserID(r)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Verify booking exists, status is confirmed, and driver matches
	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM bookings b
			JOIN rides ride ON b.ride_id = ride.id
			WHERE b.id = $1 AND ride.driver_id = $2 AND b.status = 'confirmed'
		)`
	err = h.db.QueryRow(r.Context(), query, bookingID, userID).Scan(&exists)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Forbidden: Invalid booking or driver mismatch", http.StatusForbidden)
		return
	}

	// Upgrade connection
	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:       h.hub,
		conn:      conn,
		send:      make(chan []byte, 256),
		bookingID: bookingID,
		role:      "driver",
		userID:    userID,
	}

	h.hub.register <- client

	// Start WritePump in a goroutine
	go client.WritePump()
	// ReadPump blocks until the connection is closed
	client.ReadPump()
}

// RiderWS handles WebSocket connections for riders
func (h *Handler) RiderWS(w http.ResponseWriter, r *http.Request) {
	bookingID := chi.URLParam(r, "bookingID")
	userID, err := h.getUserID(r)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Verify booking exists, status is confirmed, and rider matches
	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM bookings b
			WHERE b.id = $1 AND b.rider_id = $2 AND b.status = 'confirmed'
		)`
	err = h.db.QueryRow(r.Context(), query, bookingID, userID).Scan(&exists)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Forbidden: Invalid booking or rider mismatch", http.StatusForbidden)
		return
	}

	// Upgrade connection
	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:       h.hub,
		conn:      conn,
		send:      make(chan []byte, 256),
		bookingID: bookingID,
		role:      "rider",
		userID:    userID,
	}

	h.hub.register <- client

	// Start WritePump in a goroutine
	go client.WritePump()
	// ReadPump blocks, dropping client's incoming messages silently
	client.ReadPump()
}
