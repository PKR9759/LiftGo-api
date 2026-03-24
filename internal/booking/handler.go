// internal/booking/handler.go
package booking

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
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
	msg := map[string]string{"type": "status_update"}
	data, _ := json.Marshal(msg)
	h.hub.BroadcastToRoom(bookingID, data)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	booking, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		var driverEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.DriverID).Scan(&driverEmail)
		if err != nil {
			log.Printf("Failed to fetch driver email for new booking email: %v", err)
			return
		}
		h.emailClient.SendNewBookingRequestToDriver(
			driverEmail,
			booking.DriverName,
			booking.RiderName,
			booking.OriginLabel,
			booking.DestLabel,
			booking.DepartureAt,
			booking.Seats,
		)
		h.pushClient.PushNewBookingRequest(
			booking.DriverID,
			booking.RiderName,
			booking.OriginLabel,
			booking.DestLabel,
		)
	}()

	utils.WriteJSON(w, http.StatusCreated, booking)
}

func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	bookings, err := h.service.GetMine(r.Context(), claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetIncoming(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	bookings, err := h.service.GetIncoming(r.Context(), claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	booking, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "booking not found")
		return
	}

	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	booking, err := h.service.Confirm(r.Context(), id, claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		var riderEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.RiderID).Scan(&riderEmail)
		if err != nil {
			log.Printf("Failed to fetch rider email for booking confirmed email: %v", err)
			return
		}
		h.emailClient.SendBookingConfirmedToRider(
			riderEmail,
			booking.RiderName,
			booking.DriverName,
			booking.OriginLabel,
			booking.DestLabel,
			booking.DepartureAt,
		)
		h.pushClient.PushBookingConfirmed(
			booking.RiderID,
			booking.DriverName,
			booking.OriginLabel,
			booking.DestLabel,
		)
	}()

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	booking, err := h.service.Cancel(r.Context(), id, claims.UserID, claims.Role)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		isDriverCancelling := (claims.UserID == booking.DriverID)

		recipientID := booking.DriverID
		recipientName := booking.DriverName
		cancelledByName := booking.RiderName

		if isDriverCancelling {
			recipientID = booking.RiderID
			recipientName = booking.RiderName
			cancelledByName = booking.DriverName
		}

		var recipientEmail string
		err := h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", recipientID).Scan(&recipientEmail)
		if err != nil {
			log.Printf("Failed to fetch recipient email for booking cancelled email: %v", err)
			return
		}

		h.emailClient.SendBookingCancelled(
			recipientEmail,
			recipientName,
			cancelledByName,
			booking.OriginLabel,
			booking.DestLabel,
		)
		h.pushClient.PushBookingCancelled(
			recipientID,
			cancelledByName,
		)
	}()

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) GetRideBookings(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	rideID := chi.URLParam(r, "id")

	bookings, err := h.service.GetRideBookingsWithRiderInfo(r.Context(), rideID, claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, bookings)
}

func (h *Handler) RiderReady(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

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
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go h.pushClient.PushRiderReady(booking.DriverID, booking.RiderName)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) StartRide(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	rideID := chi.URLParam(r, "id") // Using rideID here

	// Verify driver
	var driverID string
	var currentStatus string
	var departure time.Time
	err := h.db.QueryRow(r.Context(), "SELECT driver_id, status, departure_at FROM rides WHERE id = $1", rideID).Scan(&driverID, &currentStatus, &departure)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "ride not found")
		return
	}
	if driverID != claims.UserID {
		utils.WriteError(w, http.StatusUnauthorized, "only driver can start the ride")
		return
	}
	if currentStatus != "scheduled" {
		utils.WriteError(w, http.StatusBadRequest, "ride must be scheduled")
		return
	}
	if time.Now().Before(departure.Add(-30 * time.Minute)) {
		utils.WriteError(w, http.StatusBadRequest, "Too early — you can start the ride 30 minutes before scheduled time")
		return
	}

	_, err = h.db.Exec(r.Context(), "UPDATE rides SET status = 'active', updated_at = now() WHERE id = $1", rideID)
	if err != nil {
		utils.WriteError(w, http.StatusInternalServerError, "failed to start ride")
		return
	}

	go func() {
		rows, _ := h.db.Query(context.Background(), "SELECT b.id, u.id, u.name FROM bookings b JOIN users u ON u.id = b.rider_id WHERE b.ride_id = $1 AND b.status IN ('confirmed', 'rider_ready')", rideID)
		defer rows.Close()
		var driverName string
		h.db.QueryRow(context.Background(), "SELECT name FROM users WHERE id = $1", driverID).Scan(&driverName)

		for rows.Next() {
			var bID, rID, rName string
			rows.Scan(&bID, &rID, &rName)
			h.pushClient.PushDriverStartedRide(rID, driverName)
			h.broadcastStatusUpdate(bID)
		}
	}()

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "ride started"})
}

func (h *Handler) PickedUp(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

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
		utils.WriteError(w, http.StatusBadRequest, "You must be within 200 meters of the rider's pickup point")
		return
	}

	booking, err := h.service.MarkPickedUp(r.Context(), id, claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go h.pushClient.PushDriverPickedUp(booking.RiderID)

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) Dropped(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	b, err := h.service.GetByID(r.Context(), id)
	if err != nil || b.DriverID != claims.UserID || b.Status != "picked_up" {
		utils.WriteError(w, http.StatusBadRequest, "invalid booking or ride state")
		return
	}

	booking, completed, err := h.service.MarkDropped(r.Context(), id, claims.UserID)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		h.pushClient.PushRideCompleted(booking.RiderID)
		var riderEmail, driverEmail string
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.RiderID).Scan(&riderEmail)
		h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.DriverID).Scan(&driverEmail)

		h.emailClient.SendRideCompletedToRider(riderEmail, booking.RiderName, booking.DriverName)

		if completed {
			h.emailClient.SendRideCompletedToDriver(driverEmail, booking.DriverName, booking.RiderName)
			h.pushClient.PushRideCompleted(booking.DriverID)
		}
	}()

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}

func (h *Handler) NoShow(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

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
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		h.pushClient.PushNoShow(booking.RiderID)
		if completed {
			var driverEmail string
			h.db.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", booking.DriverID).Scan(&driverEmail)
			h.emailClient.SendRideCompletedToDriver(driverEmail, booking.DriverName, booking.RiderName)
			h.pushClient.PushRideCompleted(booking.DriverID)
		}
	}()

	h.broadcastStatusUpdate(id)
	utils.WriteJSON(w, http.StatusOK, booking)
}
