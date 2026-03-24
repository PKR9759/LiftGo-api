// internal/booking/repository.go
package booking

import (
	"context"
	"fmt"

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
		return nil, err
	}
	defer tx.Rollback(ctx)

	// atomically decrement seats — returns nothing if not enough
	var rideID string
	err = tx.QueryRow(ctx,
		`UPDATE rides
		 SET available_seats = available_seats - $1,
		     updated_at      = now()
		 WHERE id = $2
		   AND available_seats >= $1
		   AND status = 'active'
		 RETURNING id`,
		req.Seats, req.RideID,
	).Scan(&rideID)
	if err != nil {
		return nil, fmt.Errorf("not enough seats or ride is unavailable")
	}

	// get price
	var pricePerSeat float64
	err = tx.QueryRow(ctx,
		`SELECT price_per_seat FROM rides WHERE id = $1`, req.RideID,
	).Scan(&pricePerSeat)
	if err != nil {
		return nil, err
	}

	totalPrice := pricePerSeat * float64(req.Seats)

	// nullable seek_id
	var seekID *string
	if req.SeekID != "" {
		seekID = &req.SeekID
	}

	// insert booking
	var bookingID, status string
	err = tx.QueryRow(ctx,
		`INSERT INTO bookings (ride_id, rider_id, seek_id, seats, total_price)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, status`,
		req.RideID, riderID, seekID, req.Seats, totalPrice,
	).Scan(&bookingID, &status)
	if err != nil {
		return nil, err
	}

	// mark ride full if seats hit 0
	_, err = tx.Exec(ctx,
		`UPDATE rides SET status = 'full'
		 WHERE id = $1 AND available_seats = 0`, req.RideID,
	)
	if err != nil {
		return nil, err
	}

	// if booking came from a seek, mark seek as matched
	if seekID != nil {
		_, err = tx.Exec(ctx,
			`UPDATE seeks SET status = 'matched', updated_at = now()
			 WHERE id = $1`, seekID,
		)
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

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
		return nil, fmt.Errorf("booking not found or not authorised")
	}

	// give seat back if rider cancels
	if role == "rider" && newStatus == "cancelled" {
		_, err = r.db.Exec(ctx,
			`UPDATE rides r
			 SET available_seats = available_seats + b.seats,
			     status = CASE
			       WHEN status = 'full' THEN 'active'
			       ELSE status
			     END,
			     updated_at = now()
			 FROM bookings b
			 WHERE b.id = $1 AND r.id = b.ride_id`, id,
		)
		if err != nil {
			return nil, err
		}
	}

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
		return nil, fmt.Errorf("booking not found or not authorised")
	}
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
		return nil, fmt.Errorf("booking not found or not authorised")
	}
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
		return nil, fmt.Errorf("booking not found or not authorised")
	}
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
		return nil, fmt.Errorf("booking not found or not authorised")
	}
	return r.GetByID(ctx, returnedID)
}

func (r *Repository) CheckAndUpdateRideCompletion(ctx context.Context, rideID string) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
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
		return false, err
	}

	completedRide := false
	if pendingCount == 0 {
		var activeStatus string
		err = tx.QueryRow(ctx, `SELECT status FROM rides WHERE id = $1`, rideID).Scan(&activeStatus)
		if err == nil && activeStatus == "active" {
			_, err = tx.Exec(ctx, `UPDATE rides SET status = 'completed', updated_at = now() WHERE id = $1`, rideID)
			if err != nil {
				return false, err
			}
			completedRide = true
		}
	}

	if err := tx.Commit(ctx); err != nil {
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
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, nil
}
