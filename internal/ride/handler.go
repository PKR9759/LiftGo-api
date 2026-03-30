// internal/ride/handler.go
package ride

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
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
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)

	originLat, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lat"), 64)
	originLng, _ := strconv.ParseFloat(r.URL.Query().Get("origin_lng"), 64)
	destLat, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lat"), 64)
	destLng, _ := strconv.ParseFloat(r.URL.Query().Get("dest_lng"), 64)
	radius, _ := strconv.ParseFloat(r.URL.Query().Get("radius"), 64)

	slog.Info("Handler.FindNearby entry",
		"request_id", reqID,
		"origin_lat", originLat, "origin_lng", originLng,
		"dest_lat", destLat, "dest_lng", destLng, "radius", radius)

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
		slog.Error("FindNearby failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// GET /api/rides/:id
func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.GetByID entry", "request_id", reqID, "ride_id", id)

	ride, err := h.service.GetByID(r.Context(), id)
	if err != nil {
		slog.Error("GetByID failed", "error", err, "request_id", reqID, "ride_id", id)
		utils.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	utils.WriteJSON(w, http.StatusOK, ride)
}

// POST /api/rides
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.Create entry", "request_id", reqID, "user_id", claims.UserID)

	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ride, err := h.service.Create(r.Context(), claims.UserID, req)
	if err != nil {
		slog.Error("Create failed", "error", err, "request_id", reqID, "user_id", claims.UserID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("ride created successfully", "ride_id", ride.ID, "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusCreated, ride)
}

// GET /api/rides/mine
func (h *Handler) GetMine(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)

	slog.Info("Handler.GetMine entry", "request_id", reqID, "user_id", claims.UserID)

	rides, err := h.service.GetMyRides(r.Context(), claims.UserID)
	if err != nil {
		slog.Error("GetMine failed", "error", err, "request_id", reqID, "user_id", claims.UserID)
		utils.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

// PUT /api/rides/:id
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Update entry", "request_id", reqID, "ride_id", id, "user_id", claims.UserID)

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ride, err := h.service.Update(r.Context(), id, claims.UserID, req)
	if err != nil {
		slog.Error("Update failed", "error", err, "request_id", reqID, "ride_id", id, "user_id", claims.UserID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("ride updated successfully", "ride_id", id, "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, ride)
}

// PUT /api/rides/:id/status
func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.UpdateStatus entry", "request_id", reqID, "ride_id", id, "user_id", claims.UserID)

	var req UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("invalid request body", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.service.UpdateStatus(r.Context(), id, claims.UserID, req.Status); err != nil {
		slog.Error("UpdateStatus failed", "error", err, "request_id", reqID, "ride_id", id, "user_id", claims.UserID, "status", req.Status)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("ride status updated successfully", "ride_id", id, "status", req.Status, "user_id", claims.UserID, "request_id", reqID)

	go func(status string, rID string, drvID string, rqID string) {
		if status != "active" && status != "completed" {
			return
		}

		ctx := context.Background()

		var driverName, driverEmail string
		err := h.db.QueryRow(ctx, "SELECT name, email FROM users WHERE id = $1", drvID).Scan(&driverName, &driverEmail)
		if err != nil {
			slog.Error("Failed to fetch driver details for ride notification", "error", err, "ride_id", rID, "request_id", rqID)
			return
		}

		rows, err := h.db.Query(ctx,
			`SELECT u.email, u.name, u.id
			 FROM bookings b
			 JOIN users u ON u.id = b.rider_id
			 WHERE b.ride_id = $1 AND b.status = 'confirmed'`,
			rID,
		)
		if err != nil {
			slog.Error("Failed to query confirmed bookings for ride notification", "error", err, "ride_id", rID, "request_id", rqID)
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

		if status == "active" {
			for _, ri := range riders {
				h.emailClient.SendDriverStartedRideToRider(ri.Email, ri.Name, driverName)
				h.pushClient.PushDriverStartedRide(ri.ID, driverName)
			}
		} else if status == "completed" {
			for _, ri := range riders {
				h.emailClient.SendRideCompletedToRider(ri.Email, ri.Name, driverName)
				h.emailClient.SendRideCompletedToDriver(driverEmail, driverName, ri.Name)
				h.pushClient.PushRideCompleted(ri.ID)
			}
			h.pushClient.PushRideCompleted(drvID)
		}
	}(req.Status, id, claims.UserID, reqID)

	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "status updated to " + req.Status})
}

// DELETE /api/rides/:id
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.Cancel entry", "request_id", reqID, "ride_id", id, "user_id", claims.UserID)

	// Fetch ride for guards
	var rideStatus string
	var departureAt time.Time
	var rideDriverID string
	err := h.db.QueryRow(r.Context(),
		`SELECT status, departure_at, driver_id FROM rides WHERE id = $1`, id,
	).Scan(&rideStatus, &departureAt, &rideDriverID)
	if err != nil {
		slog.Error("ride not found during cancel", "error", err, "request_id", reqID, "ride_id", id)
		utils.WriteError(w, http.StatusNotFound, "ride not found")
		return
	}

	// Guard: ownership
	if rideDriverID != claims.UserID {
		slog.Warn("cancel forbidden - not driver", "request_id", reqID, "ride_id", id, "user_id", claims.UserID)
		utils.WriteError(w, http.StatusForbidden, "you are not the driver of this ride")
		return
	}

	// Guard: ride must be scheduled or full
	if rideStatus != "scheduled" && rideStatus != "full" {
		slog.Warn("cancel failed - invalid status", "request_id", reqID, "ride_id", id, "status", rideStatus)
		utils.WriteError(w, http.StatusBadRequest, "Cannot cancel a ride that has already started.")
		return
	}

	// Guard: time — only if there are active bookings
	var activeBookings int
	h.db.QueryRow(r.Context(),
		`SELECT count(*) FROM bookings WHERE ride_id = $1 AND status IN ('pending', 'confirmed')`, id,
	).Scan(&activeBookings)

	if activeBookings > 0 && time.Until(departureAt) < time.Hour {
		slog.Warn("cancel failed - within 1 hour deadline", "request_id", reqID, "ride_id", id)
		utils.WriteError(w, http.StatusBadRequest, "Cancellations for rides with active bookings are not allowed within 1 hour of departure.")
		return
	}

	if err := h.service.Cancel(r.Context(), id, claims.UserID); err != nil {
		slog.Error("cancel failed in service", "error", err, "request_id", reqID, "ride_id", id)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Cascade: cancel all pending/confirmed bookings for this ride
	h.db.Exec(r.Context(),
		`UPDATE bookings SET status = 'cancelled', updated_at = now()
		 WHERE ride_id = $1 AND status IN ('pending', 'confirmed')`, id,
	)

	slog.Info("ride cancelled successfully", "ride_id", id, "user_id", claims.UserID, "request_id", reqID)
	utils.WriteJSON(w, http.StatusOK, map[string]string{"message": "ride cancelled"})
}

// GET /api/rides/:id/status-summary
func (h *Handler) GetStatusSummary(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)
	claims := auth.GetUserFromContext(r)
	id := chi.URLParam(r, "id")

	slog.Info("Handler.GetStatusSummary entry", "request_id", reqID, "ride_id", id, "user_id", claims.UserID)

	// Fetch ride info
	var rideID, rideStatus string
	var departureAt time.Time
	var availableSeats, totalSeats int
	err := h.db.QueryRow(r.Context(),
		`SELECT id, status, departure_at, available_seats, total_seats
		 FROM rides WHERE id = $1`, id,
	).Scan(&rideID, &rideStatus, &departureAt, &availableSeats, &totalSeats)
	if err != nil {
		slog.Error("GetStatusSummary failed - ride not found", "error", err, "request_id", reqID, "ride_id", id)
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
