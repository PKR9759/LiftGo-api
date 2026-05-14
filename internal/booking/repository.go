// internal/booking/repository.go
package booking

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/audit"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, riderID string, req CreateRequest) (*Booking, error) {
	if req.IdempotencyKey != "" {
		var existingID string
		err := r.db.QueryRow(ctx, "SELECT id FROM bookings WHERE idempotency_key = $1", req.IdempotencyKey).Scan(&existingID)
		if err == nil && existingID != "" {
			slog.Info("returning existing booking for idempotency key", "idempotency_key", req.IdempotencyKey, "booking_id", existingID)
			return r.GetByID(ctx, existingID)
		}
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		slog.Error("failed to start create booking tx", "error", err)
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Step A — Validate ride state and lock row for consistent capacity checks.
	var route []byte
	var totalSeats int
	var availableSeats int
	var pricePerSeat float64
	var rideDriverID string
	var rideStatus string
	var departureAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT route, total_seats, available_seats, price_per_seat, driver_id, status, departure_at
		 FROM rides
		 WHERE id = $1
		 FOR UPDATE`,
		req.RideID,
	).Scan(&route, &totalSeats, &availableSeats, &pricePerSeat, &rideDriverID, &rideStatus, &departureAt)
	if err != nil {
		slog.Warn("ride not found or unavailable for booking", "ride_id", req.RideID, "error", err)
		return nil, fmt.Errorf("ride not found")
	}

	if rideDriverID == riderID {
		return nil, fmt.Errorf("you cannot book your own ride")
	}

	if rideStatus != "scheduled" && rideStatus != "active" {
		return nil, fmt.Errorf("ride is already %s", rideStatus)
	}

	if departureAt.Before(time.Now().Add(-1 * time.Hour)) {
		return nil, fmt.Errorf("cannot book a ride that departed more than 1 hour ago")
	}

	if req.Seats > availableSeats {
		return nil, ErrSegmentCapacity
	}

	var existingCount int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM bookings
		 WHERE ride_id = $1 AND rider_id = $2 AND status IN ('pending','confirmed','rider_ready','picked_up')`,
		req.RideID, riderID,
	).Scan(&existingCount)
	if err != nil {
		return nil, fmt.Errorf("failed to validate existing bookings")
	}
	if existingCount > 0 {
		return nil, fmt.Errorf("you already have an active booking on this ride")
	}

	// Step B — Compute pickup/dropoff fractions. If route geometry is missing,
	// fallback to full-route occupancy checks.
	pickupFraction, dropoffFraction := 0.0, 1.0
	if len(route) > 0 {
		err = tx.QueryRow(ctx,
			`SELECT
				ST_LineLocatePoint(route, ST_ClosestPoint(route, ST_SetSRID(ST_MakePoint($1, $2), 4326))),
				ST_LineLocatePoint(route, ST_ClosestPoint(route, ST_SetSRID(ST_MakePoint($3, $4), 4326)))
			 FROM rides WHERE id = $5`,
			req.PickupLng, req.PickupLat, req.DropoffLng, req.DropoffLat, req.RideID,
		).Scan(&pickupFraction, &dropoffFraction)
		if err != nil {
			slog.Error("failed to calculate ride fractions", "error", err, "ride_id", req.RideID)
			return nil, fmt.Errorf("failed to calculate location on route")
		}
	}

	pickupFraction = clamp01(pickupFraction)
	dropoffFraction = clamp01(dropoffFraction)

	// Snap to whole route if pickup/dropoff are very close to start/end points
	if pickupFraction < 0.05 {
		pickupFraction = 0.0
	}
	if dropoffFraction > 0.95 {
		dropoffFraction = 1.0
	}

	if pickupFraction >= dropoffFraction {
		slog.Warn("invalid booking segment", "pickup", pickupFraction, "dropoff", dropoffFraction, "ride_id", req.RideID)
		return nil, ErrInvalidSegmentOrder
	}

	// Step C — Capacity check using interval overlap
	var occupiedSeats int
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(b.seats), 0) AS occupied_seats
		 FROM bookings b
		 WHERE b.ride_id = $1
		   AND b.status IN ('confirmed', 'rider_ready', 'picked_up')
		   AND b.pickup_fraction < $3
		   AND b.dropoff_fraction > $2`,
		req.RideID, pickupFraction, dropoffFraction,
	).Scan(&occupiedSeats)
	if err != nil {
		slog.Error("capacity check query failed", "error", err, "ride_id", req.RideID)
		return nil, fmt.Errorf("failed to check seat capacity")
	}

	if occupiedSeats+req.Seats > totalSeats {
		slog.Warn("insufficient segment capacity", "occupied", occupiedSeats, "requested", req.Seats, "total", totalSeats, "ride_id", req.RideID)
		return nil, ErrSegmentCapacity
	}

	coverage := clamp01(dropoffFraction - pickupFraction)
	segmentPricePerSeat := roundMoney(pricePerSeat * coverage)
	totalPrice := roundMoney(segmentPricePerSeat * float64(req.Seats))

	// Step D — Store fractions in the booking row
	var bookingID, status string
	var idempVal *string
	if req.IdempotencyKey != "" {
		idempVal = &req.IdempotencyKey
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO bookings (ride_id, rider_id, seats, total_price, pickup_fraction, dropoff_fraction, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, status`,
		req.RideID, riderID, req.Seats, totalPrice, pickupFraction, dropoffFraction, idempVal,
	).Scan(&bookingID, &status)
	if err != nil {
		var pgErr *pgconn.PgError
		if errorsAsPg(err, &pgErr) && (pgErr.ConstraintName == "unique_rider_per_ride" || pgErr.ConstraintName == "ux_bookings_active_ride_rider") {
			return nil, fmt.Errorf("you already have a booking on this ride")
		}
		slog.Error("insert booking query failed", "error", err)
		return nil, err
	}

	audit.Log(ctx, tx, "booking", bookingID, riderID, "created", nil, map[string]any{
		"ride_id":          req.RideID,
		"seats":            req.Seats,
		"total_price":      totalPrice,
		"pickup_fraction":  pickupFraction,
		"dropoff_fraction": dropoffFraction,
	})

	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit create booking failed", "error", err)
		return nil, err
	}

	slog.Info("booking recorded with segments", "booking_id", bookingID, "pickup_f", pickupFraction, "dropoff_f", dropoffFraction)
	return r.GetByID(ctx, bookingID)
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Booking, error) {
	b := &Booking{}
	var ridePricePerSeat float64
	err := r.db.QueryRow(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, ri.price_per_seat, b.created_at,
		        b.picked_up_at, b.dropped_at,
		        b.rider_ready_lat, b.rider_ready_lng,
		        COALESCE(b.pickup_fraction, 0) AS pickup_fraction,
		        COALESCE(b.dropoff_fraction, 0) AS dropoff_fraction
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 WHERE b.id = $1`, id,
	).Scan(
		&b.ID, &b.RideID, &b.RiderID, &b.RiderName,
		&b.DriverID, &b.DriverName,
		&b.OriginLabel, &b.DestLabel, &b.DepartureAt,
		&b.Seats, &b.Status, &b.RideStatus, &b.TotalPrice, &ridePricePerSeat, &b.CreatedAt,
		&b.PickedUpAt, &b.DroppedAt,
		&b.RiderReadyLat, &b.RiderReadyLng,
		&b.PickupFraction, &b.DropoffFraction,
	)
	if err != nil {
		slog.Error("GetByID booking failed", "error", err, "booking_id", id)
		return nil, err
	}
	applySegmentPricing(b, ridePricePerSeat)
	return b, nil
}

func (r *Repository) GetByRider(ctx context.Context, riderID string) ([]*Booking, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, ri.price_per_seat, b.created_at,
		        b.picked_up_at, b.dropped_at,
		        b.rider_ready_lat, b.rider_ready_lng,
		        COALESCE(b.pickup_fraction, 0) AS pickup_fraction,
		        COALESCE(b.dropoff_fraction, 0) AS dropoff_fraction
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 WHERE b.rider_id = $1
		 ORDER BY b.created_at DESC`, riderID,
	)
	if err != nil {
		slog.Error("GetByRider bookings db error", "error", err, "rider_id", riderID)
		return nil, err
	}
	defer rows.Close()
	return scanBookings(rows)
}

func (r *Repository) GetIncoming(ctx context.Context, driverID string) ([]*Booking, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, ri.price_per_seat, b.created_at,
		        b.picked_up_at, b.dropped_at,
		        b.rider_ready_lat, b.rider_ready_lng,
		        COALESCE(b.pickup_fraction, 0) AS pickup_fraction,
		        COALESCE(b.dropoff_fraction, 0) AS dropoff_fraction
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 WHERE ri.driver_id = $1
		 ORDER BY b.created_at DESC`, driverID,
	)
	if err != nil {
		slog.Error("GetIncoming bookings db error", "error", err, "driver_id", driverID)
		return nil, err
	}
	defer rows.Close()
	return scanBookings(rows)
}

func (r *Repository) UpdateStatus(ctx context.Context, id, actorID, newStatus, role string) (*Booking, error) {
	var query string
	if role == "driver" {
		query = `
			UPDATE bookings b
			SET status = $1, updated_at = now()
			FROM rides ri
			WHERE b.id = $2
			  AND b.ride_id = ri.id
			  AND ri.driver_id = $3
			RETURNING b.id`
	} else {
		query = `
			UPDATE bookings
			SET status = $1, updated_at = now()
			WHERE id = $2 AND rider_id = $3
			RETURNING id`
	}

	var returnedID string
	err := r.db.QueryRow(ctx, query, newStatus, id, actorID).Scan(&returnedID)
	if err != nil {
		slog.Error("UpdateStatus booking query failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	audit.Log(ctx, r.db, "booking", returnedID, actorID, "status_changed", nil, map[string]string{"status": newStatus})
	slog.Info("booking status updated in db", "booking_id", returnedID, "new_status", newStatus)
	return r.GetByID(ctx, returnedID)
}

// ConfirmBooking atomically confirms a booking and decrements seats
func (r *Repository) ConfirmBooking(ctx context.Context, id, driverID string) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		slog.Error("failed to start confirm booking tx", "error", err)
		return nil, err
	}
	defer tx.Rollback(ctx)

	var returnedID string
	var seats int
	var rideID string
	var bookingStatus string
	var rideStatus string
	err = tx.QueryRow(ctx,
		`SELECT b.id, b.seats, b.ride_id, b.status, ri.status
		 FROM bookings b
		 JOIN rides ri ON ri.id = b.ride_id
		 WHERE b.id = $1 AND ri.driver_id = $2
		 FOR UPDATE OF b, ri`,
		id, driverID,
	).Scan(&returnedID, &seats, &rideID, &bookingStatus, &rideStatus)
	if err != nil {
		slog.Error("booking confirm verification failed", "error", err, "booking_id", id, "driver_id", driverID)
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	if bookingStatus != "pending" {
		return nil, fmt.Errorf("booking is already %s", bookingStatus)
	}

	if rideStatus != "scheduled" && rideStatus != "active" {
		return nil, fmt.Errorf("ride is %s and cannot accept confirmations", rideStatus)
	}

	// now decrement seats atomically
	var updatedRideID string
	err = tx.QueryRow(ctx,
		`UPDATE rides
		 SET available_seats = available_seats - $1,
		     updated_at = now()
		 WHERE id = $2
		   AND available_seats >= $1
		 RETURNING id`,
		seats, rideID,
	).Scan(&updatedRideID)
	if err != nil {
		slog.Error("ride seat decrement failed", "error", err, "ride_id", rideID, "deduct_seats", seats)
		return nil, fmt.Errorf("not enough seats to confirm this booking")
	}

	err = tx.QueryRow(ctx,
		`UPDATE bookings
		 SET status = 'confirmed', updated_at = now()
		 WHERE id = $1 AND status = 'pending'
		 RETURNING id`,
		returnedID,
	).Scan(&returnedID)
	if err != nil {
		return nil, fmt.Errorf("booking is no longer pending")
	}

	// mark ride full if seats hit 0
	_, err = tx.Exec(ctx,
		`UPDATE rides
		 SET status = CASE WHEN available_seats = 0 THEN 'full' ELSE status END,
		     updated_at = now()
		 WHERE id = $1`, rideID,
	)
	if err != nil {
		slog.Error("mark ride full failed", "error", err)
		return nil, err
	}

	audit.Log(ctx, tx, "booking", returnedID, driverID, "confirmed", nil, map[string]string{"status": "confirmed"})

	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit confirm booking failed", "error", err)
		return nil, err
	}

	slog.Info("booking confirmed successfully in db", "booking_id", returnedID, "ride_id", updatedRideID)
	return r.GetByID(ctx, returnedID)
}

// CancelBooking cancels a booking and restores seats if it was confirmed
func (r *Repository) CancelBooking(ctx context.Context, id, actorID, role string) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// first get the booking's current status before cancelling
	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM bookings WHERE id = $1 FOR UPDATE`, id,
	).Scan(&currentStatus)
	if err != nil {
		slog.Error("cancel booking - not found", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found")
	}

	// cancel the booking
	var query string
	if role == "driver" {
		query = `
			UPDATE bookings b
			SET status = 'cancelled', updated_at = now()
			FROM rides ri
			WHERE b.id = $1
			  AND b.ride_id = ri.id
			  AND ri.driver_id = $2
			RETURNING b.id`
	} else {
		query = `
			UPDATE bookings
			SET status = 'cancelled', updated_at = now()
			WHERE id = $1 AND rider_id = $2
			RETURNING id`
	}

	var returnedID string
	err = tx.QueryRow(ctx, query, id, actorID).Scan(&returnedID)
	if err != nil {
		slog.Error("cancel booking update failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	// only restore seats if booking was confirmed (seats were actually decremented)
	if currentStatus == "confirmed" || currentStatus == "rider_ready" {
		_, err = tx.Exec(ctx,
			`UPDATE rides r
			 SET available_seats = available_seats + b.seats,
			     status = CASE
			       WHEN status = 'full' AND (available_seats + b.seats) > 0 AND departure_at > now() THEN 'scheduled'
			       WHEN status = 'full' AND (available_seats + b.seats) > 0 THEN 'active'
			       ELSE status
			     END,
			     updated_at = now()
			 FROM bookings b
			 WHERE b.id = $1 AND r.id = b.ride_id`, id,
		)
		if err != nil {
			slog.Error("restore seats failed during cancel", "error", err, "booking_id", id)
			return nil, err
		}
	}

	audit.Log(ctx, tx, "booking", returnedID, actorID, "cancelled", map[string]string{"old_status": currentStatus}, map[string]string{"status": "cancelled"})

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	slog.Info("booking cancelled in db", "booking_id", returnedID)
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) GetRideBookingsWithRiderInfo(ctx context.Context, rideID, driverID string) ([]*BookingWithRiderInfo, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, ri.price_per_seat, b.created_at,
		        b.picked_up_at, b.dropped_at,
		        ur.avg_rating,
		        ri.origin_lat AS rider_origin_lat,
		        ri.origin_lng AS rider_origin_lng,
		        ri.dest_lat     AS rider_dest_lat,
		        ri.dest_lng     AS rider_dest_lng,
		        b.rider_ready_lat, b.rider_ready_lng,
		        COALESCE(b.pickup_fraction, 0) AS pickup_fraction,
		        COALESCE(b.dropoff_fraction, 0) AS dropoff_fraction
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 WHERE b.ride_id = $1 AND ri.driver_id = $2
		 ORDER BY b.created_at ASC`,
		rideID, driverID,
	)
	if err != nil {
		slog.Error("GetRideBookings query failed", "error", err, "ride_id", rideID)
		return nil, err
	}
	defer rows.Close()

	var list []*BookingWithRiderInfo
	for rows.Next() {
		var bi BookingWithRiderInfo
		var ridePricePerSeat float64
		err := rows.Scan(
			&bi.ID, &bi.RideID, &bi.RiderID, &bi.RiderName,
			&bi.DriverID, &bi.DriverName,
			&bi.OriginLabel, &bi.DestLabel, &bi.DepartureAt,
			&bi.Seats, &bi.Status, &bi.RideStatus, &bi.TotalPrice, &ridePricePerSeat, &bi.CreatedAt,
			&bi.PickedUpAt, &bi.DroppedAt,
			&bi.RiderRating,
			&bi.RiderOriginLat, &bi.RiderOriginLng,
			&bi.RiderDestLat, &bi.RiderDestLng,
			&bi.RiderReadyLat, &bi.RiderReadyLng,
			&bi.PickupFraction, &bi.DropoffFraction,
		)
		if err != nil {
			slog.Error("scan error GetRideBookings", "error", err)
			return nil, err
		}
		applySegmentPricing(&bi.Booking, ridePricePerSeat)
		list = append(list, &bi)
	}
	return list, nil
}

func (r *Repository) CheckDriverLocation(ctx context.Context, bookingID string, driverLat, driverLng float64) (bool, error) {
	var isWithin bool
	err := r.db.QueryRow(ctx,
		`SELECT ST_DWithin(
			ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography,
			ST_SetSRID(ST_MakePoint(
				ri.origin_lng,
				ri.origin_lat
			), 4326)::geography,
			200
		) 
		FROM bookings b
		JOIN rides ri ON ri.id = b.ride_id
		WHERE b.id = $3`,
		driverLng, driverLat, bookingID,
	).Scan(&isWithin)
	if err != nil {
		slog.Error("CheckDriverLocation query failed", "error", err, "booking_id", bookingID)
		return false, err
	}
	return isWithin, nil
}

func (r *Repository) CancelPendingBookingsOnRide(ctx context.Context, rideID, excludeBookingID string) ([]*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`UPDATE bookings
		 SET status = 'cancelled', updated_at = now()
		 WHERE ride_id = $1
		   AND status = 'pending'
		   AND id <> $2
		 RETURNING id`,
		rideID, excludeBookingID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var bookingID string
		if err := rows.Scan(&bookingID); err != nil {
			return nil, err
		}
		ids = append(ids, bookingID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	result := make([]*Booking, 0, len(ids))
	for _, bookingID := range ids {
		b, err := r.GetByID(ctx, bookingID)
		if err != nil {
			slog.Warn("failed to load auto-cancelled booking details", "booking_id", bookingID, "error", err)
			continue
		}
		result = append(result, b)
	}
	return result, nil
}

func (r *Repository) MarkRiderReady(ctx context.Context, id, riderID string, lat, lng *float64) (*Booking, error) {
	if lat == nil || lng == nil {
		return nil, fmt.Errorf("rider location is required")
	}
	var returnedID string
	err := r.db.QueryRow(ctx,
		`UPDATE bookings
		 SET status = 'rider_ready', rider_ready_lat = $1, rider_ready_lng = $2, updated_at = now()
		 WHERE id = $3 AND rider_id = $4 AND status = 'confirmed'
		 RETURNING id`,
		lat, lng, id, riderID,
	).Scan(&returnedID)
	if err != nil {
		slog.Error("MarkRiderReady failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}
	slog.Info("booking marked rider_ready in db", "booking_id", returnedID)
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) MarkPickedUp(ctx context.Context, id, driverID string) (*Booking, error) {
	var returnedID string
	err := r.db.QueryRow(ctx,
		`UPDATE bookings b
		 SET status = 'picked_up', picked_up_at = now(), updated_at = now()
		 FROM rides ri
		 WHERE b.id = $1
		   AND b.ride_id = ri.id
		   AND ri.driver_id = $2
		   AND ri.status = 'active'
		   AND b.status IN ('confirmed', 'rider_ready')
		 RETURNING b.id`,
		id, driverID,
	).Scan(&returnedID)
	if err != nil {
		slog.Error("MarkPickedUp failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}
	slog.Info("booking marked picked_up in db", "booking_id", returnedID)
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) MarkDropped(ctx context.Context, id, driverID string) (*Booking, error) {
	var returnedID string
	err := r.db.QueryRow(ctx,
		`UPDATE bookings b
		 SET status = 'completed', dropped_at = now(), updated_at = now()
		 FROM rides ri
		 WHERE b.id = $1
		   AND b.ride_id = ri.id
		   AND ri.driver_id = $2
		   AND b.status = 'picked_up'
		 RETURNING b.id`,
		id, driverID,
	).Scan(&returnedID)
	if err != nil {
		slog.Error("MarkDropped failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}
	slog.Info("booking marked completed in db", "booking_id", returnedID)
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) MarkNoShow(ctx context.Context, id, driverID string) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var returnedID, rideID string
	var seats int
	err = tx.QueryRow(ctx,
		`UPDATE bookings b
		 SET status = 'no_show', updated_at = now()
		 FROM rides ri
		 WHERE b.id = $1
		   AND b.ride_id = ri.id
		   AND ri.driver_id = $2
		   AND ri.status = 'active'
		   AND b.status IN ('confirmed', 'rider_ready')
		 RETURNING b.id, b.ride_id, b.seats`,
		id, driverID,
	).Scan(&returnedID, &rideID, &seats)
	if err != nil {
		slog.Error("MarkNoShow failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	_, err = tx.Exec(ctx,
		`UPDATE rides r
		 SET available_seats = available_seats + b.seats,
		     status = CASE
		       WHEN status = 'full' AND (available_seats + b.seats) > 0 AND departure_at > now() THEN 'scheduled'
		       WHEN status = 'full' AND (available_seats + b.seats) > 0 THEN 'active'
		       ELSE status
		     END,
		     updated_at = now()
		 FROM bookings b
		 WHERE b.id = $1 AND r.id = b.ride_id`,
		returnedID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	slog.Info("booking marked no_show in db", "booking_id", returnedID)
	_ = rideID
	_ = seats
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) CheckAndUpdateRideCompletion(ctx context.Context, rideID string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		slog.Error("CheckAndUpdateRideCompletion tx start failed", "error", err)
		return false, err
	}
	defer tx.Rollback(ctx)

	var pendingCount int
	err = tx.QueryRow(ctx,
		`SELECT count(*) FROM bookings 
		 WHERE ride_id = $1 AND status NOT IN ('completed', 'cancelled', 'no_show')`,
		rideID,
	).Scan(&pendingCount)
	if err != nil {
		slog.Error("pending bookings count query failed", "error", err, "ride_id", rideID)
		return false, err
	}

	completedRide := false
	if pendingCount == 0 {
		var activeStatus string
		err = tx.QueryRow(ctx, `SELECT status FROM rides WHERE id = $1`, rideID).Scan(&activeStatus)
		if err == nil && activeStatus == "active" {
			_, err = tx.Exec(ctx, `UPDATE rides SET status = 'completed', updated_at = now() WHERE id = $1`, rideID)
			if err != nil {
				slog.Error("update ride to completed failed", "error", err, "ride_id", rideID)
				return false, err
			}
			completedRide = true
			slog.Info("ride automatically completed as all bookings resolved", "ride_id", rideID)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit generic tx failed CheckAndUpdateRideCompletion", "error", err)
		return false, err
	}
	return completedRide, nil
}

func scanBookings(rows interface {
	Next() bool
	Scan(...any) error
}) ([]*Booking, error) {
	var bookings []*Booking
	for rows.Next() {
		b := &Booking{}
		var ridePricePerSeat float64
		err := rows.Scan(
			&b.ID, &b.RideID, &b.RiderID, &b.RiderName,
			&b.DriverID, &b.DriverName,
			&b.OriginLabel, &b.DestLabel, &b.DepartureAt,
			&b.Seats, &b.Status, &b.RideStatus, &b.TotalPrice, &ridePricePerSeat, &b.CreatedAt,
			&b.PickedUpAt, &b.DroppedAt,
			&b.RiderReadyLat, &b.RiderReadyLng,
			&b.PickupFraction, &b.DropoffFraction,
		)
		if err != nil {
			slog.Error("scanBookings failed", "error", err)
			return nil, err
		}
		applySegmentPricing(b, ridePricePerSeat)
		bookings = append(bookings, b)
	}
	return bookings, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func roundMoney(v float64) float64 {
	return math.Round(v)
}

func applySegmentPricing(b *Booking, fullRoutePricePerSeat float64) {
	coverage := clamp01(b.DropoffFraction - b.PickupFraction)
	b.FullRoutePricePerSeat = roundMoney(fullRoutePricePerSeat)
	b.SegmentCoveragePct = roundMoney(coverage * 100)
	b.SegmentPricePerSeat = roundMoney(fullRoutePricePerSeat * coverage)
	b.TotalFullPrice = roundMoney(fullRoutePricePerSeat * float64(b.Seats))
	b.TotalSavings = roundMoney(b.TotalFullPrice - b.TotalPrice)
	if b.TotalSavings < 0 {
		b.TotalSavings = 0
	}
}

func errorsAsPg(err error, target **pgconn.PgError) bool {
	if err == nil {
		return false
	}
	pgErr, ok := err.(*pgconn.PgError)
	if !ok {
		return false
	}
	*target = pgErr
	return true
}
