// internal/review/repository.go
package review

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

func (r *Repository) Create(ctx context.Context, reviewerID string, req CreateRequest) (*Review, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var validBooking bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM bookings b
			JOIN rides ri ON ri.id = b.ride_id
			WHERE b.id = $1
			  AND b.status IN ('confirmed','completed')
			  AND (b.rider_id = $2 OR ri.driver_id = $2)
		)`, req.BookingID, reviewerID,
	).Scan(&validBooking)
	if err != nil {
		return nil, err
	}
	if !validBooking {
		return nil, fmt.Errorf("booking not found or not eligible for review")
	}

	var review Review
	err = tx.QueryRow(ctx,
		`INSERT INTO reviews (booking_id, reviewer_id, reviewee_id, rating, comment)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, booking_id, reviewer_id, reviewee_id, rating, comment, created_at`,
		req.BookingID, reviewerID, req.RevieweeID,
		req.Rating, nullableString(req.Comment),
	).Scan(
		&review.ID, &review.BookingID,
		&review.ReviewerID, &review.RevieweeID,
		&review.Rating, &review.Comment, &review.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("review already submitted or invalid booking")
	}

	_, err = tx.Exec(ctx,
		`UPDATE users
		 SET total_reviews = total_reviews + 1,
		     avg_rating = (
		         SELECT ROUND(AVG(rating)::numeric, 1)
		         FROM reviews WHERE reviewee_id = $1
		     ),
		     updated_at = now()
		 WHERE id = $1`, req.RevieweeID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	err = r.db.QueryRow(ctx,
		`SELECT name FROM users WHERE id = $1`, reviewerID,
	).Scan(&review.ReviewerName)
	if err != nil {
		return nil, err
	}

	return &review, nil
}

func (r *Repository) GetByReviewee(ctx context.Context, revieweeID string) ([]*Review, error) {
	rows, err := r.db.Query(ctx,
		`SELECT rv.id, rv.booking_id, rv.reviewer_id, u.name,
		        rv.reviewee_id, rv.rating, rv.comment, rv.created_at
		 FROM reviews rv
		 JOIN users u ON u.id = rv.reviewer_id
		 WHERE rv.reviewee_id = $1
		 ORDER BY rv.created_at DESC`, revieweeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []*Review
	for rows.Next() {
		rv := &Review{}
		if err := rows.Scan(
			&rv.ID, &rv.BookingID, &rv.ReviewerID, &rv.ReviewerName,
			&rv.RevieweeID, &rv.Rating, &rv.Comment, &rv.CreatedAt,
		); err != nil {
			return nil, err
		}
		reviews = append(reviews, rv)
	}
	return reviews, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}