// internal/booking/repository.go
package booking

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, riderID string, req CreateRequest) (*Booking, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		slog.Error("failed to start create booking tx", "error", err)
		return nil, err
	}
	defer tx.Rollback(ctx)

	// verify ride exists, is bookable, and has enough seats (don't decrement yet)
	var rideID string
	var pricePerSeat float64
	err = tx.QueryRow(ctx,
		`SELECT id, price_per_seat FROM rides
		 WHERE id = $1
		   AND available_seats >= $2
		   AND status IN ('scheduled', 'active')
		 FOR UPDATE`,
		req.RideID, req.Seats,
	).Scan(&rideID, &pricePerSeat)
	if err != nil {
		slog.Warn("failed to book seats or unavailable", "ride_id", req.RideID, "requested_seats", req.Seats)
		return nil, fmt.Errorf("not enough seats or ride is unavailable")
	}

	totalPrice := pricePerSeat * float64(req.Seats)

	// nullable seek_id
	var seekID *string
	if req.SeekID != "" {
		seekID = &req.SeekID
	}

	// insert booking as pending (seats NOT decremented yet)
	var bookingID, status string
	err = tx.QueryRow(ctx,
		`INSERT INTO bookings (ride_id, rider_id, seek_id, seats, total_price)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, status`,
		req.RideID, riderID, seekID, req.Seats, totalPrice,
	).Scan(&bookingID, &status)
	if err != nil {
		slog.Error("insert booking query failed", "error", err)
		return nil, err
	}

	// if booking came from a seek, mark seek as matched
	if seekID != nil {
		_, err = tx.Exec(ctx,
			`UPDATE seeks SET status = 'matched', updated_at = now()
			 WHERE id = $1`, seekID,
		)
		if err != nil {
			slog.Error("failed to update seek status", "error", err, "seek_id", *seekID)
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit create booking failed", "error", err)
		return nil, err
	}

	slog.Info("booking recorded in db", "booking_id", bookingID, "ride_id", rideID, "rider_id", riderID)
	return r.GetByID(ctx, bookingID)
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Booking, error) {
	b := &Booking{}
	err := r.db.QueryRow(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        b.seek_id,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, b.created_at,
		        b.rider_ready_lat, b.rider_ready_lng
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 WHERE b.id = $1`, id,
	).Scan(
		&b.ID, &b.RideID, &b.RiderID, &b.RiderName,
		&b.DriverID, &b.DriverName,
		&b.SeekID,
		&b.OriginLabel, &b.DestLabel, &b.DepartureAt,
		&b.Seats, &b.Status, &b.RideStatus, &b.TotalPrice, &b.CreatedAt,
		&b.RiderReadyLat, &b.RiderReadyLng,
	)
	if err != nil {
		slog.Error("GetByID booking failed", "error", err, "booking_id", id)
		return nil, err
	}
	return b, nil
}

func (r *Repository) GetByRider(ctx context.Context, riderID string) ([]*Booking, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        b.seek_id,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, b.created_at,
		        b.rider_ready_lat, b.rider_ready_lng
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
		        b.seek_id,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, b.created_at,
		        b.rider_ready_lat, b.rider_ready_lng
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

	// confirm the booking, verify it's pending and belongs to this driver's ride
	var returnedID string
	var seats int
	var rideID string
	err = tx.QueryRow(ctx,
		`UPDATE bookings b
		 SET status = 'confirmed', updated_at = now()
		 FROM rides ri
		 WHERE b.id = $1
		   AND b.ride_id = ri.id
		   AND ri.driver_id = $2
		   AND b.status = 'pending'
		 RETURNING b.id, b.seats, b.ride_id`,
		id, driverID,
	).Scan(&returnedID, &seats, &rideID)
	if err != nil {
		slog.Error("booking confirm verification failed", "error", err, "booking_id", id, "driver_id", driverID)
		return nil, fmt.Errorf("booking not found, not pending, or not authorised")
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

	// mark ride full if seats hit 0
	_, err = tx.Exec(ctx,
		`UPDATE rides SET status = 'full'
		 WHERE id = $1 AND available_seats = 0`, rideID,
	)
	if err != nil {
		slog.Error("mark ride full failed", "error", err)
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("commit confirm booking failed", "error", err)
		return nil, err
	}

	slog.Info("booking confirmed successfully in db", "booking_id", returnedID, "ride_id", updatedRideID)
	return r.GetByID(ctx, returnedID)
}

// CancelBooking cancels a booking and restores seats if it was confirmed
func (r *Repository) CancelBooking(ctx context.Context, id, actorID, role string) (*Booking, error) {
	// first get the booking's current status before cancelling
	var currentStatus string
	err := r.db.QueryRow(ctx,
		`SELECT status FROM bookings WHERE id = $1`, id,
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
	err = r.db.QueryRow(ctx, query, id, actorID).Scan(&returnedID)
	if err != nil {
		slog.Error("cancel booking update failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	// only restore seats if booking was confirmed (seats were actually decremented)
	if currentStatus == "confirmed" || currentStatus == "rider_ready" {
		_, err = r.db.Exec(ctx,
			`UPDATE rides r
			 SET available_seats = available_seats + b.seats,
			     status = CASE
			       WHEN status = 'full' AND departure_at > now() THEN 'scheduled'
			       WHEN status = 'full' THEN 'active'
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

	slog.Info("booking cancelled in db", "booking_id", returnedID)
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) GetRideBookingsWithRiderInfo(ctx context.Context, rideID, driverID string) ([]*BookingWithRiderInfo, error) {
	rows, err := r.db.Query(ctx,
		`SELECT b.id, b.ride_id, b.rider_id, ur.name,
		        ri.driver_id, ud.name,
		        b.seek_id,
		        ri.origin_label, ri.dest_label, ri.departure_at,
		        b.seats, b.status, ri.status, b.total_price, b.created_at,
		        b.picked_up_at, b.dropped_at,
		        ur.avg_rating,
		        COALESCE(s.origin_lat, ri.origin_lat) AS rider_origin_lat,
		        COALESCE(s.origin_lng, ri.origin_lng) AS rider_origin_lng,
		        COALESCE(s.dest_lat, ri.dest_lat)     AS rider_dest_lat,
		        COALESCE(s.dest_lng, ri.dest_lng)     AS rider_dest_lng,
		        b.rider_ready_lat, b.rider_ready_lng
		 FROM bookings b
		 JOIN users  ur ON ur.id = b.rider_id
		 JOIN rides  ri ON ri.id = b.ride_id
		 JOIN users  ud ON ud.id = ri.driver_id
		 LEFT JOIN seeks s ON s.id = b.seek_id
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
		err := rows.Scan(
			&bi.ID, &bi.RideID, &bi.RiderID, &bi.RiderName,
			&bi.DriverID, &bi.DriverName,
			&bi.SeekID,
			&bi.OriginLabel, &bi.DestLabel, &bi.DepartureAt,
			&bi.Seats, &bi.Status, &bi.RideStatus, &bi.TotalPrice, &bi.CreatedAt,
			&bi.PickedUpAt, &bi.DroppedAt,
			&bi.RiderRating,
			&bi.RiderOriginLat, &bi.RiderOriginLng,
			&bi.RiderDestLat, &bi.RiderDestLng,
			&bi.RiderReadyLat, &bi.RiderReadyLng,
		)
		if err != nil {
			slog.Error("scan error GetRideBookings", "error", err)
			return nil, err
		}
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
				COALESCE(s.origin_lng, ri.origin_lng),
				COALESCE(s.origin_lat, ri.origin_lat)
			), 4326)::geography,
			200
		) 
		FROM bookings b
		JOIN rides ri ON ri.id = b.ride_id
		LEFT JOIN seeks s ON s.id = b.seek_id
		WHERE b.id = $3`,
		driverLng, driverLat, bookingID,
	).Scan(&isWithin)
	if err != nil {
		slog.Error("CheckDriverLocation query failed", "error", err, "booking_id", bookingID)
		return false, err
	}
	return isWithin, nil
}

func (r *Repository) MarkRiderReady(ctx context.Context, id, riderID string, lat, lng *float64) (*Booking, error) {
	var returnedID string
	err := r.db.QueryRow(ctx,
		`UPDATE bookings
		 SET status = 'rider_ready', rider_ready_lat = $1, rider_ready_lng = $2, updated_at = now()
		 WHERE id = $3 AND rider_id = $4
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
		 WHERE b.id = $1 AND b.ride_id = ri.id AND ri.driver_id = $2
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
		 WHERE b.id = $1 AND b.ride_id = ri.id AND ri.driver_id = $2
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
	var returnedID string
	err := r.db.QueryRow(ctx,
		`UPDATE bookings b
		 SET status = 'no_show', updated_at = now()
		 FROM rides ri
		 WHERE b.id = $1 AND b.ride_id = ri.id AND ri.driver_id = $2
		 RETURNING b.id`,
		id, driverID,
	).Scan(&returnedID)
	if err != nil {
		slog.Error("MarkNoShow failed", "error", err, "booking_id", id)
		return nil, fmt.Errorf("booking not found or not authorised")
	}
	slog.Info("booking marked no_show in db", "booking_id", returnedID)
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
		err := rows.Scan(
			&b.ID, &b.RideID, &b.RiderID, &b.RiderName,
			&b.DriverID, &b.DriverName,
			&b.SeekID,
			&b.OriginLabel, &b.DestLabel, &b.DepartureAt,
			&b.Seats, &b.Status, &b.RideStatus, &b.TotalPrice, &b.CreatedAt,
			&b.RiderReadyLat, &b.RiderReadyLng,
		)
		if err != nil {
			slog.Error("scanBookings failed", "error", err)
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, nil
}
