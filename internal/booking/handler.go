// internal/booking/handler.go
package booking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/PKR9759/LiftGo-api/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	service     *Service
	db          *pgxpool.Pool
	emailClient *notification.EmailClient
	pushClient  *notification.PushClient
	hub         *ws.Hub
}

func NewHandler(service *Service, db *pgxpool.Pool, emailClient *notification.EmailClient, pushClient *notification.PushClient, hub *ws.Hub) *Handler {
	return &Handler{
		service:     service,
		db:          db,
		emailClient: emailClient,
		pushClient:  pushClient,
		hub:         hub,
	}
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
		utils.WriteError(w, http.StatusBadRequest, err.Error())
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
		h.emailClient.SendNewBookingRequestToDriver(
			driverEmail,
			b.DriverName,
			b.RiderName,
			b.OriginLabel,
			b.DestLabel,
			b.DepartureAt,
			b.Seats,
		)
		h.pushClient.PushNewBookingRequest(
			b.DriverID,
			b.RiderName,
			b.OriginLabel,
			b.DestLabel,
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

	booking, err := h.service.GetByID(r.Context(), id)
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

	booking, err := h.service.Confirm(r.Context(), id, claims.UserID)
	if err != nil {
		slog.Error("Confirm booking failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("booking confirmed successfully", "booking_id", id, "user_id", claims.UserID, "request_id", reqID)

	go func(b *Booking, reqID string) {
		var riderEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.RiderID).Scan(&riderEmail)
		if err != nil {
			slog.Error("Failed to fetch rider email for confirmed notification", "error", err, "booking_id", b.ID, "request_id", reqID)
			return
		}
		h.emailClient.SendBookingConfirmedToRider(
			riderEmail,
			b.RiderName,
			b.DriverName,
			b.OriginLabel,
			b.DestLabel,
			b.DepartureAt,
		)
		h.pushClient.PushBookingConfirmed(
			b.RiderID,
			b.DriverName,
			b.OriginLabel,
			b.DestLabel,
		)
	}(booking, reqID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Cancel booking entry", "request_id", reqID, "booking_id", id)

	// Fetch booking + ride info for guards
	var bookingStatus, rideStatus, rideDriverID string
	var departureAt time.Time
	err := h.db.QueryRow(r.Context(),
		`SELECT b.status, ri.status, ri.driver_id, ri.departure_at
		 FROM bookings b JOIN rides ri ON ri.id = b.ride_id
		 WHERE b.id = $1`, id,
	).Scan(&bookingStatus, &rideStatus, &rideDriverID, &departureAt)
	if err != nil {
		slog.Error("booking not found for cancelation", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}

	// Determine actual role
	role := "rider"
	if rideDriverID == claims.UserID {
		role = "driver"
	}

	// Guard 1: booking status must allow cancellation
	if bookingStatus != "pending" && bookingStatus != "confirmed" {
		utils.WriteError(w, http.StatusBadRequest, "This booking cannot be cancelled at this stage.")
		return
	}

	// Guard 2: ride must not be active or completed
	if rideStatus == "active" || rideStatus == "completed" {
		utils.WriteError(w, http.StatusBadRequest, "Cannot cancel a ride that is already in progress.")
		return
	}

	// Guard 3: time guard — only for confirmed bookings
	if bookingStatus == "confirmed" && time.Until(departureAt) < time.Hour {
		utils.WriteError(w, http.StatusBadRequest, "Confirmed bookings cannot be cancelled within 1 hour of departure.")
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

		h.emailClient.SendBookingCancelled(
			recipientEmail,
			recipientName,
			cancelledByName,
			b.OriginLabel,
			b.DestLabel,
		)
		h.pushClient.PushBookingCancelled(
			recipientID,
			cancelledByName,
		)
	}(booking, reqID, claims.UserID)

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
		utils.WriteError(w, http.StatusUnauthorized, "only rider can mark ready")
		return
	}
	if b.Status != "confirmed" {
		utils.WriteError(w, http.StatusBadRequest, "booking must be confirmed")
		return
	}
	importTime := time.Now()
	guardTime := b.DepartureAt.Add(-15 * time.Minute)
	if importTime.Before(guardTime) {
		utils.WriteError(w, http.StatusBadRequest, "Too early — you can mark ready 15 minutes before departure")
		return
	}

	booking, err := h.service.MarkRiderReady(r.Context(), id, claims.UserID, body.RiderLat, body.RiderLng)
	if err != nil {
		slog.Error("RiderReady mark failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("rider successfully marked ready", "booking_id", id, "request_id", reqID)

	go h.pushClient.PushRiderReady(booking.DriverID, booking.RiderName)

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
		utils.WriteError(w, http.StatusUnauthorized, "only driver can start the ride")
		return
	}
	if currentStatus != "scheduled" && currentStatus != "full" {
		utils.WriteError(w, http.StatusBadRequest, "ride must be scheduled or full")
		return
	}
	if time.Now().Before(departure.Add(-30 * time.Minute)) {
		utils.WriteError(w, http.StatusBadRequest, "Too early — you can start the ride 30 minutes before scheduled time")
		return
	}

	_, err = h.db.Exec(r.Context(), "UPDATE rides SET status = 'active', updated_at = now() WHERE id = $1", rideID)
	if err != nil {
		slog.Error("StartRide state DB update failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusInternalServerError, "failed to start ride")
		return
	}

	slog.Info("ride officially started", "ride_id", rideID, "request_id", reqID)

	go func(rID string, dName string, rqID string) {
		rows, err := h.db.Query(context.Background(), "SELECT b.id, u.id, u.name FROM bookings b JOIN users u ON u.id = b.rider_id WHERE b.ride_id = $1 AND b.status IN ('confirmed', 'rider_ready')", rID)
		if err != nil {
			slog.Error("Failed to fetch confirmed riders for start notification", "error", err, "request_id", rqID)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var bID, riderUID, rName string
			rows.Scan(&bID, &riderUID, &rName)
			h.pushClient.PushDriverStartedRide(riderUID, dName)
			h.broadcastStatusUpdate(bID)
		}
	}(rideID, claims.UserID, reqID)

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
	if err != nil || b.DriverID != claims.UserID || b.RideStatus != "active" || (b.Status != "confirmed" && b.Status != "rider_ready") {
		utils.WriteError(w, http.StatusBadRequest, "invalid booking or ride state")
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

	go h.pushClient.PushDriverPickedUp(booking.RiderID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Dropped(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Dropped entry", "request_id", reqID, "booking_id", id)

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil || b.DriverID != claims.UserID || b.Status != "picked_up" {
		utils.WriteError(w, http.StatusBadRequest, "invalid booking or ride state")
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
		h.pushClient.PushRideCompleted(b.RiderID)
		var riderEmail, driverEmail string
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.RiderID).Scan(&riderEmail)
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.DriverID).Scan(&driverEmail)

		h.emailClient.SendRideCompletedToRider(riderEmail, b.RiderName, b.DriverName)

		if isCompleted {
			h.emailClient.SendRideCompletedToDriver(driverEmail, b.DriverName, b.RiderName)
			h.pushClient.PushRideCompleted(b.DriverID)
		}
	}(booking, completed, reqID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) NoShow(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.NoShow entry", "request_id", reqID, "booking_id", id)

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil || b.DriverID != claims.UserID || b.RideStatus != "active" || (b.Status != "confirmed" && b.Status != "rider_ready") {
		utils.WriteError(w, http.StatusBadRequest, "invalid booking or ride state")
		return
	}

	if time.Now().Before(b.DepartureAt.Add(10 * time.Minute)) {
		utils.WriteError(w, http.StatusBadRequest, "You can only mark no-show 10 minutes after the scheduled departure time")
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
		h.pushClient.PushNoShow(b.RiderID)
		if isCompleted {
			var driverEmail string
			h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", b.DriverID).Scan(&driverEmail)
			h.emailClient.SendRideCompletedToDriver(driverEmail, b.DriverName, b.RiderName)
			h.pushClient.PushRideCompleted(b.DriverID)
		}
	}(booking, completed, reqID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}
