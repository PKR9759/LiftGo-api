// internal/ride/handler.go
package ride

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	service     *Service
	db          *pgxpool.Pool
	emailClient *notification.EmailClient
	pushClient  *notification.PushClient
}

func NewHandler(service *Service, db *pgxpool.Pool, emailClient *notification.EmailClient, pushClient *notification.PushClient) *Handler {
	return &Handler{
		service:     service,
		db:          db,
		emailClient: emailClient,
		pushClient:  pushClient,
	}
}

// GET /api/rides/nearby?origin_lat=&origin_lng=&dest_lat=&dest_lng=&radius=
func (h *Handler) FindNearby(w http.ResponseWriter, r *http.Request) {
	originLat, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lat"), 64)
	originLng, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lng"), 64)
	destLat, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lat"), 64)
	destLng, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lng"), 64)
	radius, _ := strconv.ParseFloat(r.URL.Query().Get("radius"), 64)

	log.Printf("[RideHandler] FindNearby: origin=(%f,%f), dest=(%f,%f), radius=%f", originLat, originLng, destLat, destLng, radius)

	var excludeUserID string
	if claims := auth.GetUserFromContext(r); claims != nil {
		excludeUserID = claims.UserID
	}

	rides, err := h.service.FindNearby(r.Context(), NearbyParams{
		OriginLat:     originLat,
		OriginLng:     originLng,
		DestLat:       destLat,
		DestLng:       destLng,
		RadiusMeters:  radius,
		ExcludeUserID: excludeUserID,
	})
	if err != nil {
		log.Printf("[RideHandler] FindNearby error: %v", err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// GET /api/rides/:id
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ride, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, ride)
}

// POST /api/rides
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	log.Printf("[RideHandler] Create request from user: %s", claims.UserID)

	ride, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		log.Printf("[RideHandler] Create error for user %s: %v", claims.UserID, err)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusCreated, ride)
}

// GET /api/rides/mine
func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)

	log.Printf("[RideHandler] GetMine request from user: %s", claims.UserID)

	rides, err := h.service.GetMyRides(r.Context(), claims.UserID)
	if err != nil {
		log.Printf("[RideHandler] GetMine error for user %s: %v", claims.UserID, err)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// PUT /api/rides/:id
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ride, err := h.service.Update(r.Context(), id, claims.UserID, req)
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, ride)
}

// PUT /api/rides/:id/status
func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.service.UpdateStatus(r.Context(), id, claims.UserID, req.Status); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	go func() {
		if req.Status != "active" && req.Status != "completed" {
			return
		}

		ctx := context.Background()

		var driverName, driverEmail string
		err := h.db.QueryRow(ctx, "SELECT name, email FROM users WHERE id = $1", claims.UserID).Scan(&driverName, &driverEmail)
		if err != nil {
			log.Printf("Failed to fetch driver details for ride notification: %v", err)
			return
		}

		rows, err := h.db.Query(ctx,
			`SELECT u.email, u.name, u.id
			 FROM bookings b
			 JOIN users u ON u.id = b.rider_id
			 WHERE b.ride_id = $1 AND b.status = 'confirmed'`,
			id,
		)
		if err != nil {
			log.Printf("Failed to query confirmed bookings for ride notification: %v", err)
			return
		}
		defer rows.Close()

		type RiderInfo struct {
			Email string
			Name  string
			ID    string
		}
		var riders []RiderInfo
		for rows.Next() {
			var ri RiderInfo
			if err := rows.Scan(&ri.Email, &ri.Name, &ri.ID); err == nil {
				riders = append(riders, ri)
			}
		}

		if req.Status == "active" {
			for _, ri := range riders {
				h.emailClient.SendDriverStartedRideToRider(ri.Email, ri.Name, driverName)
				h.pushClient.PushDriverStartedRide(ri.ID, driverName)
			}
		} else if req.Status == "completed" {
			for _, ri := range riders {
				h.emailClient.SendRideCompletedToRider(ri.Email, ri.Name, driverName)
				h.emailClient.SendRideCompletedToDriver(driverEmail, driverName, ri.Name)
				h.pushClient.PushRideCompleted(ri.ID)
			}
			h.pushClient.PushRideCompleted(claims.UserID)
		}
	}()

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "status updated to " + req.Status})
}

// DELETE /api/rides/:id
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	// Fetch ride for guards
	var rideStatus string
	var departureAt time.Time
	var rideDriverID string
	err := h.db.QueryRow(r.Context(),
		`SELECT status, departure_at, driver_id FROM rides WHERE id = $1`, id,
	).Scan(&rideStatus, &departureAt, &rideDriverID)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "ride not found")
		return
	}

	// Guard: ownership
	if rideDriverID != claims.UserID {
		utils.WriteError(w, http.StatusForbidden, "you are not the driver of this ride")
		return
	}

	// Guard: ride must be scheduled or full
	if rideStatus != "scheduled" && rideStatus != "full" {
		utils.WriteError(w, http.StatusBadRequest, "Cannot cancel a ride that has already started.")
		return
	}

	// Guard: time — only if there are active bookings
	var activeBookings int
	h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM bookings WHERE ride_id = $1 AND status IN ('pending', 'confirmed')`, id,
	).Scan(&activeBookings)

	if activeBookings > 0 && time.Until(departureAt) < time.Hour {
		utils.WriteError(w, http.StatusBadRequest, "Cancellations for rides with active bookings are not allowed within 1 hour of departure.")
		return
	}

	if err := h.service.Cancel(r.Context(), id, claims.UserID); err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Cascade: cancel all pending/confirmed bookings for this ride
	h.db.Exec(r.Context(),
		`UPDATE bookings SET status = 'cancelled', updated_at = now()
		 WHERE ride_id = $1 AND status IN ('pending', 'confirmed')`, id,
	)

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "ride cancelled"})
}

// GET /api/rides/:id/status-summary
func (h *Handler) GetStatusSummary(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	// Fetch ride info
	var rideID, rideStatus string
	var departureAt time.Time
	var availableSeats, totalSeats int
	err := h.db.QueryRow(r.Context(),
		`SELECT id, status, departure_at, available_seats, total_seats
		 FROM rides WHERE id = $1`, id,
	).Scan(&rideID, &rideStatus, &departureAt, &availableSeats, &totalSeats)
	if err != nil {
		utils.WriteError(w, http.StatusNotFound, "ride not found")
		return
	}

	minutesUntil := time.Until(departureAt).Minutes()
	cancellationDeadline := departureAt.Add(-1 * time.Hour).Format(time.RFC3339)

	// Fetch active booking count for cancellation logic
	var activeBookings int
	h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM bookings WHERE ride_id = $1 AND status IN ('pending', 'confirmed')`, id,
	).Scan(&activeBookings)

	rideSummary := map[string]any{
		"id":                      rideID,
		"status":                  rideStatus,
		"departure_at":            departureAt.Format(time.RFC3339),
		"available_seats":         availableSeats,
		"total_seats":             totalSeats,
		"minutes_until_departure": int(minutesUntil),
		"can_cancel":              (rideStatus == "scheduled" || rideStatus == "full") && (activeBookings == 0 || minutesUntil > 60),
		"can_start":               (rideStatus == "scheduled" || rideStatus == "full") && minutesUntil <= 30 && minutesUntil > -10,
		"cancellation_deadline":   cancellationDeadline,
	}

	// Fetch user's booking on this ride (if any)
	var bookingID, bookingStatus string
	var seats int
	err = h.db.QueryRow(r.Context(),
		`SELECT id, status, seats FROM bookings
		 WHERE ride_id = $1 AND rider_id = $2
		 ORDER BY created_at DESC LIMIT 1`, id, claims.UserID,
	).Scan(&bookingID, &bookingStatus, &seats)

	var userBooking any
	if err == nil {
		userBooking = map[string]any{
			"id":             bookingID,
			"status":         bookingStatus,
			"seats":          seats,
			"can_cancel":     (bookingStatus == "pending" && rideStatus == "scheduled") || (bookingStatus == "confirmed" && rideStatus == "scheduled" && minutesUntil > 60),
			"can_mark_ready": bookingStatus == "confirmed" && minutesUntil <= 15,
		}
	}

	utils.WriteJSON(w, http.StatusOK, map[string]any{
		"ride":         rideSummary,
		"user_booking": userBooking,
	})
}
