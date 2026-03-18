// internal/seek/repository.go
package seek

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

func (r *Repository) Create(ctx context.Context, seekerID string, req CreateRequest) (*Seek, error) {
	wkt := fmt.Sprintf("SRID=4326;LINESTRING(%f %f, %f %f)",
		req.OriginLng, req.OriginLat,
		req.DestLng, req.DestLat,
	)

	seek := &Seek{}
	err := r.db.QueryRow(ctx,
		`INSERT INTO seeks (
			seeker_id,
			origin_lat, origin_lng, origin_label,
			dest_lat,   dest_lng,   dest_label,
			route,
			seats_needed, is_recurring, recurrence_days
		) VALUES (
			$1,
			$2, $3, $4,
			$5, $6, $7,
			$8::geometry,
			$9, $10, $11
		)
		RETURNING
			id, seeker_id,
			origin_lat, origin_lng, origin_label,
			dest_lat, dest_lng, dest_label,
			seats_needed, is_recurring, recurrence_days,
			status, expires_at, created_at`,
		seekerID,
		req.OriginLat, req.OriginLng, req.OriginLabel,
		req.DestLat, req.DestLng, req.DestLabel,
		wkt,
		req.SeatsNeeded, req.IsRecurring, req.RecurrenceDays,
	).Scan(
		&seek.ID, &seek.SeekerID,
		&seek.OriginLat, &seek.OriginLng, &seek.OriginLabel,
		&seek.DestLat, &seek.DestLng, &seek.DestLabel,
		&seek.SeatsNeeded, &seek.IsRecurring, &seek.RecurrenceDays,
		&seek.Status, &seek.ExpiresAt, &seek.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return seek, nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Seek, error) {
	seek := &Seek{}
	err := r.db.QueryRow(ctx,
		`SELECT s.id, s.seeker_id, u.name, u.avg_rating, u.total_reviews,
		        s.origin_lat, s.origin_lng, s.origin_label,
		        s.dest_lat,   s.dest_lng,   s.dest_label,
		        s.seats_needed, s.is_recurring, s.recurrence_days,
		        s.status, s.expires_at, s.created_at
		 FROM seeks s
		 JOIN users u ON u.id = s.seeker_id
		 WHERE s.id = $1`, id,
	).Scan(
		&seek.ID, &seek.SeekerID, &seek.SeekerName,
		&seek.SeekerRating, &seek.SeekerReviews,
		&seek.OriginLat, &seek.OriginLng, &seek.OriginLabel,
		&seek.DestLat, &seek.DestLng, &seek.DestLabel,
		&seek.SeatsNeeded, &seek.IsRecurring, &seek.RecurrenceDays,
		&seek.Status, &seek.ExpiresAt, &seek.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return seek, nil
}

func (r *Repository) GetBySeeker(ctx context.Context, seekerID string) ([]*Seek, error) {
	rows, err := r.db.Query(ctx,
		`SELECT s.id, s.seeker_id, u.name, u.avg_rating, u.total_reviews,
		        s.origin_lat, s.origin_lng, s.origin_label,
		        s.dest_lat,   s.dest_lng,   s.dest_label,
		        s.seats_needed, s.is_recurring, s.recurrence_days,
		        s.status, s.expires_at, s.created_at
		 FROM seeks s
		 JOIN users u ON u.id = s.seeker_id
		 WHERE s.seeker_id = $1
		 ORDER BY s.created_at DESC`, seekerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSeeks(rows)
}

// FindNearby — finds seeks whose route overlaps with a driver's route
// driver uses this to see who needs a ride along their path
func (r *Repository) FindNearby(ctx context.Context, p NearbyParams) ([]*Seek, error) {
	radius := p.RadiusMeters
	if radius <= 0 {
		radius = 1500
	}

	rows, err := r.db.Query(ctx,
		`SELECT s.id, s.seeker_id, u.name, u.avg_rating, u.total_reviews,
		        s.origin_lat, s.origin_lng, s.origin_label,
		        s.dest_lat,   s.dest_lng,   s.dest_label,
		        s.seats_needed, s.is_recurring, s.recurrence_days,
		        s.status, s.expires_at, s.created_at
		 FROM seeks s
		 JOIN users u ON u.id = s.seeker_id
		 WHERE s.status = 'active'
		   AND s.expires_at > now()
		   AND ST_DWithin(
		         s.route::geography,
		         ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
		         $5
		       )
		   AND ST_DWithin(
		         s.route::geography,
		         ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography,
		         $5
		       )
		 ORDER BY s.created_at DESC`,
		p.OriginLat, p.OriginLng,
		p.DestLat, p.DestLng,
		radius,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSeeks(rows)
}

func (r *Repository) Cancel(ctx context.Context, id, seekerID string) error {
	result, err := r.db.Exec(ctx,
		`UPDATE seeks SET status = 'cancelled', updated_at = now()
		 WHERE id = $1 AND seeker_id = $2 AND status = 'active'`,
		id, seekerID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("seek not found or already cancelled")
	}
	return nil
}

// ExpireStale marks seeks past their expiry as expired
// called on every GET /seeks/nearby to keep results clean
func (r *Repository) ExpireStale(ctx context.Context) error {
	_, err := r.db.Exec(ctx,
		`UPDATE seeks SET status = 'expired'
		 WHERE status = 'active' AND expires_at < now()`,
	)
	return err
}

func scanSeeks(rows interface {
	Next() bool
	Scan(...any) error
}) ([]*Seek, error) {
	var seeks []*Seek
	for rows.Next() {
		s := &Seek{}
		err := rows.Scan(
			&s.ID, &s.SeekerID, &s.SeekerName,
			&s.SeekerRating, &s.SeekerReviews,
			&s.OriginLat, &s.OriginLng, &s.OriginLabel,
			&s.DestLat, &s.DestLng, &s.DestLabel,
			&s.SeatsNeeded, &s.IsRecurring, &s.RecurrenceDays,
			&s.Status, &s.ExpiresAt, &s.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		seeks = append(seeks, s)
	}
	return seeks, nil
}