package ws

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
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

func (h *Handler) getUserID(r *http.Request) (string, error) {
	cookie, err := r.Cookie("access_token")
	if err != nil {
		return "", errors.New("missing access_token cookie")
	}
	tokenStr := cookie.Value

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

func (h *Handler) DriverWS(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	bookingID := chi.URLParam(r, "bookingID")
	userID, err := h.getUserID(r)
	if err != nil {
		slog.Warn("DriverWS unauthorized", "error", err, "request_id", reqID)
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	slog.Info("DriverWS connection attempt", "booking_id", bookingID, "user_id", userID, "request_id", reqID)

	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM bookings b
			JOIN rides ride ON b.ride_id = ride.id
			WHERE b.id = $1 AND ride.driver_id = $2 AND b.status IN ('confirmed', 'rider_ready', 'picked_up')
		)`
	err = h.db.QueryRow(r.Context(), query, bookingID, userID).Scan(&exists)
	if err != nil {
		slog.Error("DriverWS db query failed", "error", err, "request_id", reqID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !exists {
		slog.Warn("DriverWS forbidden: invalid booking or driver mismatch", "booking_id", bookingID, "user_id", userID, "request_id", reqID)
		http.Error(w, "Forbidden: Invalid booking or driver mismatch", http.StatusForbidden)
		return
	}

	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS upgrade error", "error", err, "booking_id", bookingID, "request_id", reqID)
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
	slog.Info("websocket driver client successfully connected", "booking_id", bookingID, "user_id", userID)

	go client.WritePump()
	client.ReadPump()
}

func (h *Handler) RiderWS(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	bookingID := chi.URLParam(r, "bookingID")
	userID, err := h.getUserID(r)
	if err != nil {
		slog.Warn("RiderWS unauthorized", "error", err, "request_id", reqID)
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return
	}

	slog.Info("RiderWS connection attempt", "booking_id", bookingID, "user_id", userID, "request_id", reqID)

	var exists bool
	query := `
		SELECT EXISTS(
			SELECT 1 FROM bookings b
			WHERE b.id = $1 AND b.rider_id = $2 AND b.status IN ('confirmed', 'rider_ready', 'picked_up')
		)`
	err = h.db.QueryRow(r.Context(), query, bookingID, userID).Scan(&exists)
	if err != nil {
		slog.Error("RiderWS db query failed", "error", err, "request_id", reqID)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !exists {
		slog.Warn("RiderWS forbidden: invalid booking or rider mismatch", "booking_id", bookingID, "user_id", userID, "request_id", reqID)
		http.Error(w, "Forbidden: Invalid booking or rider mismatch", http.StatusForbidden)
		return
	}

	conn, err := Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS upgrade error", "error", err, "booking_id", bookingID, "request_id", reqID)
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
	slog.Info("websocket rider client successfully connected", "booking_id", bookingID, "user_id", userID)

	go client.WritePump()
	client.ReadPump()
}
