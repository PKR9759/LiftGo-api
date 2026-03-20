// internal/ride/repository.go
package ride

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, driverID string, req CreateRequest) (*Ride, error) {
	departure, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		return nil, fmt.Errorf("invalid departure_at format, use ISO 8601")
	}

	ride := &Ride{}
	err = r.db.QueryRow(ctx,
		`WITH pts AS (
			SELECT
				ST_SetSRID(ST_MakePoint($3, $2), 4326) AS origin_pt,
				ST_SetSRID(ST_MakePoint($6, $5), 4326) AS dest_pt
		)
		INSERT INTO rides (
			driver_id,
			origin_lat, origin_lng, origin_label,
			dest_lat,   dest_lng,   dest_label,
			route,
			departure_at, total_seats, available_seats,
			price_per_seat, is_recurring, recurrence_days, notes
		)
		SELECT
			$1,
			$2, $3, $4,
			$5, $6, $7,
			ST_MakeLine(pts.origin_pt, pts.dest_pt),
			$8, $9, $9,
			$10, $11, $12, $13
		FROM pts
		RETURNING
			id, driver_id,
			origin_lat, origin_lng, origin_label,
			dest_lat, dest_lng, dest_label,
			departure_at, total_seats, available_seats,
			price_per_seat, is_recurring, recurrence_days,
			notes, status, created_at`,
		driverID,
		req.OriginLat, req.OriginLng, req.OriginLabel,
		req.DestLat, req.DestLng, req.DestLabel,
		departure, req.TotalSeats,
		req.PricePerSeat, req.IsRecurring,
		req.RecurrenceDays, nullableString(req.Notes),
	).Scan(
		&ride.ID, &ride.DriverID,
		&ride.OriginLat, &ride.OriginLng, &ride.OriginLabel,
		&ride.DestLat, &ride.DestLng, &ride.DestLabel,
		&ride.DepartureAt, &ride.TotalSeats, &ride.AvailableSeats,
		&ride.PricePerSeat, &ride.IsRecurring, &ride.RecurrenceDays,
		&ride.Notes, &ride.Status, &ride.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return ride, nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Ride, error) {
	ride := &Ride{}
	err := r.db.QueryRow(ctx,
		`SELECT r.id, r.driver_id, u.name, u.avg_rating, u.total_reviews,
		        r.origin_lat, r.origin_lng, r.origin_label,
		        r.dest_lat,   r.dest_lng,   r.dest_label,
		        r.departure_at, r.total_seats, r.available_seats,
		        r.price_per_seat, r.is_recurring, r.recurrence_days,
		        r.notes, r.status, r.created_at
		 FROM rides r
		 JOIN users u ON u.id = r.driver_id
		 WHERE r.id = $1`, id,
	).Scan(
		&ride.ID, &ride.DriverID, &ride.DriverName,
		&ride.DriverRating, &ride.DriverReviews,
		&ride.OriginLat, &ride.OriginLng, &ride.OriginLabel,
		&ride.DestLat, &ride.DestLng, &ride.DestLabel,
		&ride.DepartureAt, &ride.TotalSeats, &ride.AvailableSeats,
		&ride.PricePerSeat, &ride.IsRecurring, &ride.RecurrenceDays,
		&ride.Notes, &ride.Status, &ride.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return ride, nil
}

// FindNearby — the core geo matching query
// finds rides whose route passes within radius meters
// of both the seeker's origin AND destination
func (r *Repository) FindNearby(ctx context.Context, p NearbyParams) ([]*Ride, error) {
	radius := p.RadiusMeters
	if radius <= 0 {
		radius = 1500 // default 1.5km
	}

	rows, err := r.db.Query(ctx,
		`SELECT r.id, r.driver_id, u.name, u.avg_rating, u.total_reviews,
		        r.origin_lat, r.origin_lng, r.origin_label,
		        r.dest_lat,   r.dest_lng,   r.dest_label,
		        r.departure_at, r.total_seats, r.available_seats,
		        r.price_per_seat, r.is_recurring, r.recurrence_days,
		        r.notes, r.status, r.created_at
		 FROM rides r
		 JOIN users u ON u.id = r.driver_id
		 WHERE r.status = 'active'
		   AND r.available_seats > 0
		   AND r.departure_at > now()
		   AND ST_DWithin(
		         r.route::geography,
		         ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
		         $5
		       )
		   AND ST_DWithin(
		         r.route::geography,
		         ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography,
		         $5
		       )
		 ORDER BY r.departure_at ASC`,
		p.OriginLat, p.OriginLng,
		p.DestLat, p.DestLng,
		radius,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRides(rows)
}

func (r *Repository) GetByDriver(ctx context.Context, driverID string) ([]*Ride, error) {
	rows, err := r.db.Query(ctx,
		`SELECT r.id, r.driver_id, u.name, u.avg_rating, u.total_reviews,
		        r.origin_lat, r.origin_lng, r.origin_label,
		        r.dest_lat,   r.dest_lng,   r.dest_label,
		        r.departure_at, r.total_seats, r.available_seats,
		        r.price_per_seat, r.is_recurring, r.recurrence_days,
		        r.notes, r.status, r.created_at
		 FROM rides r
		 JOIN users u ON u.id = r.driver_id
		 WHERE r.driver_id = $1
		 ORDER BY r.departure_at DESC`, driverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRides(rows)
}

func (r *Repository) Update(ctx context.Context, id, driverID string, req UpdateRequest) (*Ride, error) {
	departure, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		return nil, fmt.Errorf("invalid departure_at format")
	}

	ride := &Ride{}
	err = r.db.QueryRow(ctx,
		`UPDATE rides
		 SET departure_at    = $1,
		     total_seats     = CASE WHEN $2 > 0 THEN $2 ELSE total_seats END,
		     price_per_seat  = CASE WHEN $3 > 0 THEN $3 ELSE price_per_seat END,
		     notes           = COALESCE(NULLIF($4,''), notes),
		     is_recurring    = $5,
		     recurrence_days = $6,
		     updated_at      = now()
		 WHERE id = $7 AND driver_id = $8
		 RETURNING
		 	id, driver_id,
		 	origin_lat, origin_lng, origin_label,
		 	dest_lat, dest_lng, dest_label,
		 	departure_at, total_seats, available_seats,
		 	price_per_seat, is_recurring, recurrence_days,
		 	notes, status, created_at`,
		departure, req.TotalSeats, req.PricePerSeat,
		req.Notes, req.IsRecurring, req.RecurrenceDays,
		id, driverID,
	).Scan(
		&ride.ID, &ride.DriverID,
		&ride.OriginLat, &ride.OriginLng, &ride.OriginLabel,
		&ride.DestLat, &ride.DestLng, &ride.DestLabel,
		&ride.DepartureAt, &ride.TotalSeats, &ride.AvailableSeats,
		&ride.PricePerSeat, &ride.IsRecurring, &ride.RecurrenceDays,
		&ride.Notes, &ride.Status, &ride.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return ride, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id, driverID, status string) error {
	result, err := r.db.Exec(ctx,
		`UPDATE rides SET status = $1, updated_at = now()
		 WHERE id = $2 AND driver_id = $3`,
		status, id, driverID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("ride not found or you are not the driver")
	}
	return nil
}

func (r *Repository) Cancel(ctx context.Context, id, driverID string) error {
	result, err := r.db.Exec(ctx,
		`UPDATE rides SET status = 'cancelled', updated_at = now()
		 WHERE id = $1 AND driver_id = $2 AND status = 'active'`,
		id, driverID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("ride not found or already cancelled")
	}
	return nil
}

func scanRides(rows interface {
	Next() bool
	Scan(...any) error
}) ([]*Ride, error) {
	var rides []*Ride
	for rows.Next() {
		r := &Ride{}
		err := rows.Scan(
			&r.ID, &r.DriverID, &r.DriverName,
			&r.DriverRating, &r.DriverReviews,
			&r.OriginLat, &r.OriginLng, &r.OriginLabel,
			&r.DestLat, &r.DestLng, &r.DestLabel,
			&r.DepartureAt, &r.TotalSeats, &r.AvailableSeats,
			&r.PricePerSeat, &r.IsRecurring, &r.RecurrenceDays,
			&r.Notes, &r.Status, &r.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		rides = append(rides, r)
	}
	return rides, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
