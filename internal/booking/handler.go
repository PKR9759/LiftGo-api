// internal/booking/handler.go
package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/cache"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/PKR9759/LiftGo-api/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	service     *Service
	db          *pgxpool.Pool
	emailClient *notification.EmailClient
	pushClient  *notification.PushClient
	hub         *ws.Hub
	rdb         *redis.Client
}

func NewHandler(service *Service, db *pgxpool.Pool, emailClient *notification.EmailClient, pushClient *notification.PushClient, hub *ws.Hub, rdb *redis.Client) *Handler {
	return &Handler{
		service:     service,
		db:          db,
		emailClient: emailClient,
		pushClient:  pushClient,
		hub:         hub,
		rdb:         rdb,
	}
}

func (h *Handler) invalidateMatchCache(ctx context.Context) {
	if h.rdb == nil || os.Getenv("USE_CACHE") != "true" {
		return
	}
	n, err := cache.InvalidatePattern(ctx, h.rdb, "match:*")
	if err != nil {
		slog.Warn("match cache invalidation error", "error", err)
		return
	}
	slog.Info("match cache invalidated", "keys_deleted", n)
}

func (h *Handler) broadcastStatusUpdate(bookingID string) {
	if h.hub == nil {
		return
	}
	slog.Debug("broadcasting status update", "booking_id", bookingID)
	msg := map[string]string{"type": "status_update"}
	data, _ := json.Marshal(msg)
	h.hub.BroadcastToRoom(bookingID, data)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.Create booking entry", "request_id", reqID, "user_id", claims.UserID)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	booking, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		slog.Error("Create booking failed", "error", err, "request_id", reqID, "user_id", claims.UserID)
		if errors.Is(err, ErrSegmentCapacity) {
			utils.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ErrPointNotOnRoute) || errors.Is(err, ErrInvalidSegmentOrder) {
			utils.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		msg := err.Error()
		switch {
		case msg == "you cannot book your own ride":
			utils.WriteError(w, http.StatusForbidden, msg)
		case msg == "you already have an active booking on this ride" || msg == "you already have a booking on this ride":
			utils.WriteError(w, http.StatusConflict, msg)
		case len(msg) > 16 && msg[:16] == "ride is already ":
			utils.WriteError(w, http.StatusConflict, msg)
		default:
			utils.WriteError(w, http.StatusBadRequest, msg)
		}
		return
	}

	slog.Info("booking request created successfully", "booking_id", booking.ID, "ride_id", req.RideID, "request_id", reqID)

	go func(b *Booking, reqID string) {
		var driverEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.DriverID).Scan(&driverEmail)
		if err != nil {
			slog.Error("Failed to fetch driver email for new booking notification", "error", err, "booking_id", b.ID, "request_id", reqID)
			return
		}

		departure := b.DepartureAt.Format("02 Jan 2006 03:04 PM")
		h.emailClient.Send(
			driverEmail,
			"New booking request — LiftGo",
			fmt.Sprintf("<p>Hi %s,</p><p><strong>%s</strong> requested <strong>%d</strong> seat(s).</p><p>Pickup: %s</p><p>Departure: %s</p><p>Ride ID: %s</p><p>Open LiftGo to accept or reject.</p>", b.DriverName, b.RiderName, b.Seats, b.OriginLabel, departure, b.RideID),
		)
		h.pushClient.SendToUser(
			b.DriverID,
			"New booking request",
			fmt.Sprintf("%s requested %d seat(s). Pickup %s", b.RiderName, b.Seats, b.OriginLabel),
			"/rides/"+b.RideID+"/manage",
		)
	}(booking, reqID)

	utils.WriteJSON(w, http.StatusCreated, booking)
}

func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.GetMine bookings entry", "request_id", reqID, "user_id", claims.UserID)

	bookings, err := h.service.GetMine(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("GetMine bookings failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetIncoming(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.GetIncoming bookings entry", "request_id", reqID, "user_id", claims.UserID)

	bookings, err := h.service.GetIncoming(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("GetIncoming bookings failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.GetByID booking entry", "request_id", reqID, "booking_id", id)

	booking, err := h.service.GetByIDForUser(r.Context(), id, auth.GetUserFromContext(r).UserID)
	if err != nil {
		slog.Error("GetByID booking failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}

	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Confirm booking entry", "request_id", reqID, "booking_id", id)

	booking, autoCancelled, err := h.service.Confirm(r.Context(), id, claims.UserID)
	if err != nil {
		slog.Error("Confirm booking failed", "error", err, "request_id", reqID)
		msg := err.Error()
		switch {
		case msg == "booking not found or not authorised":
			utils.WriteError(w, http.StatusForbidden, msg)
		case len(msg) > 19 && msg[:19] == "booking is already ":
			utils.WriteError(w, http.StatusConflict, msg)
		case msg == "not enough seats to confirm this booking" || msg == "booking is no longer pending":
			utils.WriteError(w, http.StatusConflict, msg)
		default:
			utils.WriteError(w, http.StatusBadRequest, msg)
		}
		return
	}

	slog.Info("booking confirmed successfully", "booking_id", id, "user_id", claims.UserID, "request_id", reqID)

	go func(b *Booking, cancelled []*Booking, reqID string) {
		var riderEmail string
		var driverRating float64
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.RiderID).Scan(&riderEmail)
		if err != nil {
			slog.Error("Failed to fetch rider email for confirmed notification", "error", err, "booking_id", b.ID, "request_id", reqID)
			return
		}

		_ = h.db.QueryRow(context.Background(), "SELECT avg_rating FROM users WHERE id = $1", b.DriverID).Scan(&driverRating)
		departure := b.DepartureAt.Format("02 Jan 2006 03:04 PM")
		h.emailClient.Send(
			riderEmail,
			"Your booking is confirmed — LiftGo",
			fmt.Sprintf("<p>Hi %s,</p><p>Your booking is confirmed.</p><p>Driver: %s (rating %.1f)</p><p>Route: %s to %s</p><p>Departure: %s</p><p>Pickup: %s</p><p>Total: ₹%.2f</p><p><a href=\"/rides/%s\">Open ride details</a></p>", b.RiderName, b.DriverName, driverRating, b.OriginLabel, b.DestLabel, departure, b.OriginLabel, b.TotalPrice, b.RideID),
		)
		h.pushClient.SendToUser(
			b.RiderID,
			"Your booking is confirmed",
			fmt.Sprintf("%s confirmed your booking. Pickup %s", b.DriverName, b.OriginLabel),
			"/bookings/"+b.ID,
		)

		for _, cb := range cancelled {
			var cancelEmail string
			if err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", cb.RiderID).Scan(&cancelEmail); err != nil {
				continue
			}
			h.emailClient.Send(
				cancelEmail,
				"Booking not accepted — ride is full",
				fmt.Sprintf("<p>Hi %s,</p><p>Your pending booking for %s to %s was cancelled because the ride is now full.</p><p>Please search for another ride in LiftGo.</p>", cb.RiderName, cb.OriginLabel, cb.DestLabel),
			)
			h.pushClient.SendToUser(
				cb.RiderID,
				"Ride is full",
				"Your pending booking was not accepted because all seats are taken.",
				"/dashboard",
			)
		}
	}(booking, autoCancelled, reqID)

	h.invalidateMatchCache(r.Context())

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Cancel booking entry", "request_id", reqID, "booking_id", id)

	// Fetch booking + ride info for guards
	var bookingStatus, rideDriverID, riderID string
	var departureAt time.Time
	err := h.db.QueryRow(r.Context(),
		`SELECT b.status, b.rider_id, ri.driver_id, ri.departure_at
		 FROM bookings b JOIN rides ri ON ri.id = b.ride_id
		 WHERE b.id = $1`, id,
	).Scan(&bookingStatus, &riderID, &rideDriverID, &departureAt)
	if err != nil {
		slog.Error("booking not found for cancelation", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}

	if claims.UserID != rideDriverID && claims.UserID != riderID {
		utils.WriteError(w, http.StatusForbidden, "only the rider or ride driver can cancel this booking")
		return
	}

	role := "rider"
	if rideDriverID == claims.UserID {
		role = "driver"
	}

	// Guard 1: booking status must allow cancellation
	if bookingStatus != "pending" && bookingStatus != "confirmed" && bookingStatus != "rider_ready" {
		utils.WriteError(w, http.StatusConflict, "This booking cannot be cancelled at this stage.")
		return
	}

	// Guard 2: time guard — only for confirmed/rider_ready bookings
	if (bookingStatus == "confirmed" || bookingStatus == "rider_ready") && time.Until(departureAt) < time.Hour {
		utils.WriteError(w, http.StatusConflict, "Cannot cancel within 1 hour of departure")
		return
	}

	booking, err := h.service.Cancel(r.Context(), id, claims.UserID, role)
	if err != nil {
		slog.Error("Cancel booking execution failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("booking cancelled successfully", "booking_id", id, "request_id", reqID)

	go func(b *Booking, reqID string, cancellerID string) {
		isDriverCancelling := (cancellerID == b.DriverID)

		recipientID := b.DriverID
		recipientName := b.DriverName
		cancelledByName := b.RiderName

		if isDriverCancelling {
			recipientID = b.RiderID
			recipientName = b.RiderName
			cancelledByName = b.DriverName
		}

		var recipientEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", recipientID).Scan(&recipientEmail)
		if err != nil {
			slog.Error("Failed to fetch recipient email for booking cancelled notification", "error", err, "booking_id", b.ID, "request_id", reqID)
			return
		}

		subject := "Booking cancelled — LiftGo"
		body := fmt.Sprintf("<p>Hi %s,</p><p>%s cancelled the booking for %s to %s.</p><p>Please open LiftGo for alternatives.</p>", recipientName, cancelledByName, b.OriginLabel, b.DestLabel)
		if isDriverCancelling {
			subject = "Driver cancelled your booking — LiftGo"
			body = fmt.Sprintf("<p>Hi %s,</p><p>Your driver %s cancelled the booking for %s to %s.</p><p>Sorry for the inconvenience. Please search for another ride.</p>", recipientName, cancelledByName, b.OriginLabel, b.DestLabel)
		}

		h.emailClient.Send(recipientEmail, subject, body)
		h.pushClient.SendToUser(recipientID, "Booking cancelled", fmt.Sprintf("%s cancelled the booking", cancelledByName), "/bookings")
	}(booking, reqID, claims.UserID)

	h.invalidateMatchCache(r.Context())

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) GetRideBookings(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	rideID := chi.URLParam(r, "id")

	slog.Info("Handler.GetRideBookings entry", "request_id", reqID, "ride_id", rideID)

	bookings, err := h.service.GetRideBookingsWithRiderInfo(r.Context(), rideID, claims.UserID)
	if err != nil {
		slog.Error("GetRideBookings failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) RiderReady(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.RiderReady entry", "request_id", reqID, "booking_id", id)

	var body struct {
		RiderLat *float64 `json:"rider_lat"`
		RiderLng *float64 `json:"rider_lng"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if b.RiderID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "only rider can mark ready")
		return
	}
	if b.Status != "confirmed" {
		utils.WriteError(w, http.StatusConflict, fmt.Sprintf("Booking is already %s", b.Status))
		return
	}
	now := time.Now()
	guardTime := b.DepartureAt.Add(-30 * time.Minute)
	if now.Before(guardTime) {
		utils.WriteError(w, http.StatusConflict, "Too early — you can mark ready within 30 minutes of departure")
		return
	}

	if body.RiderLat == nil || body.RiderLng == nil {
		utils.WriteError(w, http.StatusBadRequest, "rider_lat and rider_lng are required")
		return
	}
	if *body.RiderLat < -90 || *body.RiderLat > 90 || *body.RiderLng < -180 || *body.RiderLng > 180 {
		utils.WriteError(w, http.StatusBadRequest, "invalid coordinates")
		return
	}

	booking, err := h.service.MarkRiderReady(r.Context(), id, claims.UserID, body.RiderLat, body.RiderLng)
	if err != nil {
		slog.Error("RiderReady mark failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("rider successfully marked ready", "booking_id", id, "request_id", reqID)

	go h.pushClient.SendToUser(
		booking.DriverID,
		"Passenger is ready",
		fmt.Sprintf("%s is ready at %.5f, %.5f", booking.RiderName, *booking.RiderReadyLat, *booking.RiderReadyLng),
		"/rides/"+booking.RideID+"/manage",
	)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) StartRide(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	rideID := chi.URLParam(r, "id")

	slog.Info("Handler.StartRide entry", "request_id", reqID, "ride_id", rideID)

	// Verify driver
	var driverID string
	var currentStatus string
	var departure time.Time
	err := h.db.QueryRow(r.Context(), "SELECT driver_id, status, departure_at FROM rides WHERE id = $1", rideID).Scan(&driverID, &currentStatus, &departure)
	if err != nil {
		slog.Error("StartRide ride not found", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, "ride not found")
		return
	}
	if driverID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "only driver can start the ride")
		return
	}
	if currentStatus != "scheduled" && currentStatus != "full" {
		utils.WriteError(w, http.StatusConflict, fmt.Sprintf("ride is already %s", currentStatus))
		return
	}
	if time.Now().Before(departure.Add(-30 * time.Minute)) {
		utils.WriteError(w, http.StatusConflict, "Too early — you can start the ride 30 minutes before scheduled time")
		return
	}
	if time.Now().After(departure.Add(60 * time.Minute)) {
		utils.WriteError(w, http.StatusConflict, "Cannot start a ride more than 60 minutes after departure")
		return
	}

	_, err = h.db.Exec(r.Context(), "UPDATE rides SET status = 'active', updated_at = now() WHERE id = $1", rideID)
	if err != nil {
		slog.Error("StartRide state DB update failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, "failed to start ride")
		return
	}

	slog.Info("ride officially started", "ride_id", rideID, "request_id", reqID)

	go func(rID string, driverID string, rqID string) {
		rows, err := h.db.Query(context.Background(), "SELECT b.id, u.id, u.name FROM bookings b JOIN users u ON u.id = b.rider_id WHERE b.ride_id = $1 AND b.status IN ('confirmed', 'rider_ready')", rID)
		if err != nil {
			slog.Error("Failed to fetch confirmed riders for start notification", "error", err, "request_id", rqID)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var bID, riderUID, rName string
			rows.Scan(&bID, &riderUID, &rName)
			h.pushClient.SendToUser(riderUID, "Ride started", "Your driver has started the ride — get ready!", "/bookings/"+bID)
			h.broadcastStatusUpdate(bID)
		}
	}(rideID, claims.UserID, reqID)

	h.invalidateMatchCache(r.Context())

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "ride started"})
}

func (h *Handler) PickedUp(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.PickedUp entry", "request_id", reqID, "booking_id", id)

	var req PickedUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if b.DriverID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "only the ride driver can mark picked up")
		return
	}
	if b.RideStatus != "active" {
		utils.WriteError(w, http.StatusConflict, "Start the ride before marking passengers as picked up")
		return
	}
	if b.Status != "confirmed" && b.Status != "rider_ready" {
		utils.WriteError(w, http.StatusConflict, fmt.Sprintf("Booking is already %s", b.Status))
		return
	}

	isWithin, err := h.service.CheckDriverLocation(r.Context(), id, req.DriverLat, req.DriverLng)
	if (err != nil || !isWithin) && os.Getenv("APP_ENV") != "development" {
		slog.Warn("driver not close enough for pickup", "request_id", reqID, "booking_id", id)
		utils.WriteError(w, http.StatusBadRequest, "You must be within 200 meters of the rider's pickup point")
		return
	}

	booking, err := h.service.MarkPickedUp(r.Context(), id, claims.UserID)
	if err != nil {
		slog.Error("PickedUp db operation failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("rider marked as picked up", "booking_id", id, "request_id", reqID)

	go h.pushClient.SendToUser(booking.RiderID, "Picked up", "You've been picked up — enjoy your ride!", "/bookings/"+booking.ID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Dropped(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Dropped entry", "request_id", reqID, "booking_id", id)

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if b.DriverID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "only the ride driver can mark dropped off")
		return
	}
	if b.Status != "picked_up" {
		utils.WriteError(w, http.StatusConflict, fmt.Sprintf("Booking is already %s", b.Status))
		return
	}

	booking, completed, err := h.service.MarkDropped(r.Context(), id, claims.UserID)
	if err != nil {
		slog.Error("Dropped db operation failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("rider successfully dropped", "booking_id", id, "overall_ride_completed", completed, "request_id", reqID)

	go func(b *Booking, isCompleted bool, rqID string) {
		h.pushClient.SendToUser(b.RiderID, "Arrived", "You've arrived at your destination. Please rate your driver.", "/bookings/"+b.ID)
		var riderEmail, driverEmail string
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.RiderID).Scan(&riderEmail)
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.DriverID).Scan(&driverEmail)
		h.emailClient.Send(riderEmail, "Trip completed — LiftGo", fmt.Sprintf("<p>Hi %s,</p><p>You have arrived at your destination.</p><p><a href=\"/bookings/%s\">Please rate your driver</a>.</p>", b.RiderName, b.ID))

		if isCompleted {
			h.emailClient.Send(driverEmail, "Ride completed — Leave a review", fmt.Sprintf("<p>Hi %s,</p><p>Your ride is completed.</p><p><a href=\"/bookings/%s\">Please rate your passenger</a>.</p>", b.DriverName, b.ID))
			h.pushClient.SendToUser(b.DriverID, "Ride completed", "Ride completed. Please leave a review for your passenger.", "/bookings/"+b.ID)
		}
	}(booking, completed, reqID)

	h.invalidateMatchCache(r.Context())

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) NoShow(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.NoShow entry", "request_id", reqID, "booking_id", id)

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}
	if b.DriverID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "only the ride driver can mark no-show")
		return
	}
	if b.RideStatus != "active" {
		utils.WriteError(w, http.StatusConflict, "ride must be active to mark no-show")
		return
	}
	if b.Status != "confirmed" && b.Status != "rider_ready" {
		utils.WriteError(w, http.StatusConflict, fmt.Sprintf("Booking is already %s", b.Status))
		return
	}

	if time.Now().Before(b.DepartureAt.Add(10 * time.Minute)) {
		utils.WriteError(w, http.StatusConflict, "Cannot mark no-show until 10 minutes after departure time")
		return
	}

	booking, completed, err := h.service.MarkNoShow(r.Context(), id, claims.UserID)
	if err != nil {
		slog.Error("NoShow db operation failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("rider marked as no_show", "booking_id", id, "overall_ride_completed", completed, "request_id", reqID)

	go func(b *Booking, isCompleted bool, rqID string) {
		h.pushClient.SendToUser(b.RiderID, "Marked no-show", "You were marked as a no-show for your ride", "/bookings/"+b.ID)
		var riderEmail string
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.RiderID).Scan(&riderEmail)
		h.emailClient.Send(riderEmail, "No-show marked — LiftGo", "<p>You were marked as a no-show for your ride.</p>")
		if isCompleted {
			var driverEmail string
			h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.DriverID).Scan(&driverEmail)
			h.emailClient.Send(driverEmail, "Ride completed — Leave a review", fmt.Sprintf("<p>Hi %s,</p><p>Your ride is completed.</p><p><a href=\"/bookings/%s\">Leave a review</a>.</p>", b.DriverName, b.ID))
			h.pushClient.SendToUser(b.DriverID, "Ride completed", "Ride completed. Please leave a review.", "/bookings/"+b.ID)
		}
	}(booking, completed, reqID)

	h.invalidateMatchCache(r.Context())

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}
