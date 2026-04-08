// internal/ride/handler.go
package ride

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/cache"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	service     *Service
	db          *pgxpool.Pool
	emailClient *notification.EmailClient
	pushClient  *notification.PushClient
	rdb         *redis.Client // nil when USE_CACHE=false or Redis unavailable
}

func NewHandler(service *Service, db *pgxpool.Pool, emailClient *notification.EmailClient, pushClient *notification.PushClient, rdb *redis.Client) *Handler {
	return &Handler{
		service:     service,
		db:          db,
		emailClient: emailClient,
		pushClient:  pushClient,
		rdb:         rdb,
	}
}

// nearbyKey builds a deterministic cache key for FindNearby results.
// Coordinates are rounded to 4 decimal places so near-identical searches
// (e.g. GPS jitter) still hit the same cache entry.
func nearbyKey(originLat, originLng, destLat, destLng, radius float64, seatsNeeded int) string {
	round4 := func(v float64) float64 { return math.Round(v*10000) / 10000 }
	if radius <= 0 {
		radius = 1000
	}
	return fmt.Sprintf("match:%.4f:%.4f:%.4f:%.4f:%.0f:%d",
		round4(originLat), round4(originLng),
		round4(destLat), round4(destLng),
		radius, seatsNeeded,
	)
}

// invalidateMatchCache deletes all "match:*" keys from Redis using SCAN.
// It is called after any mutation that affects searchable rides.
func (h *Handler) invalidateMatchCache(ctx context.Context) {
	if h.rdb == nil || os.Getenv("USE_CACHE") != "true" {
		return
	}
	n, err := cache.InvalidatePattern(ctx, h.rdb, "match:*")
	if err != nil {
		slog.Warn("match cache invalidation error", "error", err)
		return
	}
	slog.Info("cache invalidated", "pattern", "match:*", "keys_deleted", n)
}

// GET /api/rides/nearby?origin_lat=&origin_lng=&dest_lat=&dest_lng=&radius=&seats_needed=
func (h *Handler) FindNearby(w http.ResponseWriter, r *http.Request) {
	reqID, _ := r.Context().Value(customMiddleware.RequestIDKey).(string)

	originLat, err := parseRequiredFloatQuery(r, "origin_lat")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	originLng, err := parseRequiredFloatQuery(r, "origin_lng")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	destLat, err := parseRequiredFloatQuery(r, "dest_lat")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	destLng, err := parseRequiredFloatQuery(r, "dest_lng")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if originLat < -90 || originLat > 90 || destLat < -90 || destLat > 90 {
		utils.WriteError(w, http.StatusBadRequest, "latitude must be between -90 and 90")
		return
	}
	if originLng < -180 || originLng > 180 || destLng < -180 || destLng > 180 {
		utils.WriteError(w, http.StatusBadRequest, "longitude must be between -180 and 180")
		return
	}

	radius, err := parseOptionalFloatQuery(r, "radius")
	if err != nil {
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if radius < 0 {
		utils.WriteError(w, http.StatusBadRequest, "radius must be 0 or greater")
		return
	}
	seatsNeeded, _ := strconv.Atoi(r.URL.Query().Get("seats_needed"))
	if seatsNeeded <= 0 {
		seatsNeeded = 1
	}

	slog.Info("Handler.FindNearby entry",
		"request_id", reqID,
		"origin_lat", originLat, "origin_lng", originLng,
		"dest_lat", destLat, "dest_lng", destLng, "radius", radius,
		"seats_needed", seatsNeeded)

	var excludeUserID string
	if claims := auth.GetUserFromContext(r); claims != nil {
		excludeUserID = claims.UserID
	}

	ctx := r.Context()
	useCache := os.Getenv("USE_CACHE") == "true"

	// ── Cache lookup ─────────────────────────────────────────────────────────
	if useCache && h.rdb != nil {
		cacheKey := nearbyKey(originLat, originLng, destLat, destLng, radius, seatsNeeded)
		start := time.Now()

		val, err := h.rdb.Get(ctx, cacheKey).Bytes()
		if err == nil {
			// Cache HIT
			slog.Debug("FindNearby cache hit",
				"key", cacheKey,
				"latency_ms", time.Since(start).Milliseconds(),
				"request_id", reqID,
			)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(val)
			return
		}
	}

	// ── Cache MISS / cache disabled — run the real query ─────────────────────
	rides, err := h.service.FindNearby(ctx, NearbyParams{
		OriginLat:     originLat,
		OriginLng:     originLng,
		DestLat:       destLat,
		DestLng:       destLng,
		RadiusMeters:  radius,
		ExcludeUserID: excludeUserID,
		SeatsNeeded:   seatsNeeded,
	})
	if err != nil {
		slog.Error("FindNearby failed", "error", err, "request_id", reqID)
		utils.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// ── Store in cache ───────────────────────────────────────────────────────
	if useCache && h.rdb != nil {
		cacheKey := nearbyKey(originLat, originLng, destLat, destLng, radius, seatsNeeded)
		slog.Debug("cache miss", "key", cacheKey, "request_id", reqID)
		if data, err := json.Marshal(rides); err == nil {
			ttl := 30 * time.Second
			h.rdb.Set(ctx, cacheKey, data, ttl)
			slog.Debug("cache set",
				"key", cacheKey,
				"ttl_seconds", int(ttl.Seconds()),
				"request_id", reqID,
			)
		}
	}

	utils.WriteJSON(w, http.StatusOK, rides)
}

func parseRequiredFloatQuery(r *http.Request, key string) (float64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid number", key)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s must be a finite number", key)
	}
	return v, nil
}

func parseOptionalFloatQuery(r *http.Request, key string) (float64, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid number", key)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s must be a finite number", key)
	}
	return v, nil
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
	h.invalidateMatchCache(r.Context())
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
	h.invalidateMatchCache(r.Context())
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
	// Invalidate match cache whenever a ride's status changes — it may no
	// longer be searchable (e.g. completed, full, cancelled).
	h.invalidateMatchCache(r.Context())

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
			 WHERE b.ride_id = $1 AND b.status IN ('confirmed', 'rider_ready')`,
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
				h.pushClient.SendToUser(ri.ID, "Ride started", "Your driver has started the ride — get ready!", "/bookings")
			}
		} else if status == "completed" {
			for _, ri := range riders {
				h.emailClient.Send(ri.Email, "Ride completed — Leave a review", fmt.Sprintf("<p>Hi %s,</p><p>Your ride with %s is completed.</p><p>Open LiftGo to leave a review.</p>", ri.Name, driverName))
				h.pushClient.SendToUser(ri.ID, "Ride completed", "Please rate your driver.", "/bookings")
			}
			h.emailClient.Send(driverEmail, "Ride completed — Leave reviews", "<p>Your ride is completed. Please rate your passengers.</p>")
			h.pushClient.SendToUser(drvID, "Ride completed", "Please rate your passengers.", "/dashboard")
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
		utils.WriteError(w, http.StatusConflict, "Cannot cancel an active or completed ride")
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

	// Cascade: cancel all pending/confirmed/rider_ready bookings for this ride and notify each passenger.
	go func(rideID string, rqID string) {
		ctx := context.Background()
		rows, err := h.db.Query(ctx,
			`UPDATE bookings b
			 SET status = 'cancelled', updated_at = now()
			 FROM users u
			 WHERE b.ride_id = $1
			   AND b.status IN ('pending', 'confirmed', 'rider_ready')
			   AND u.id = b.rider_id
			 RETURNING b.id, b.rider_id, u.email, u.name`,
			rideID,
		)
		if err != nil {
			slog.Error("failed to cascade-cancel bookings after ride cancel", "error", err, "ride_id", rideID, "request_id", rqID)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var bookingID, riderID, riderEmail, riderName string
			if err := rows.Scan(&bookingID, &riderID, &riderEmail, &riderName); err != nil {
				continue
			}
			h.emailClient.Send(riderEmail, "Ride cancelled — LiftGo", fmt.Sprintf("<p>Hi %s,</p><p>Your driver cancelled this ride.</p><p>We are sorry for the inconvenience.</p>", riderName))
			h.pushClient.SendToUser(riderID, "Ride cancelled", "Your driver cancelled the ride. Sorry for the inconvenience.", "/dashboard")
		}
	}(id, reqID)

	slog.Info("ride cancelled successfully", "ride_id", id, "user_id", claims.UserID, "request_id", reqID)
	h.invalidateMatchCache(r.Context())
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
