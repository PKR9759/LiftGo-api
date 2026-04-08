// internal/review/repository.go
package review

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

func (r *Repository) Create(ctx context.Context, reviewerID string, req CreateRequest) (*Review, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		slog.Error("failed to start review creation tx", "error", err)
		return nil, err
	}
	defer tx.Rollback(ctx)

	var validBooking bool
	err = tx.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM bookings b
			JOIN rides ri ON ri.id = b.ride_id
			WHERE b.id = $1
			  AND b.status = 'completed'
			  AND (
			    (b.rider_id = $2 AND ri.driver_id = $3) OR
			    (ri.driver_id = $2 AND b.rider_id = $3)
			  )
		)`, req.BookingID, reviewerID,
		req.RevieweeID,
	).Scan(&validBooking)
	if err != nil {
		slog.Error("review booking validation query failed", "error", err)
		return nil, err
	}
	if !validBooking {
		slog.Warn("booking not eligible for review or not found", "booking_id", req.BookingID, "reviewer_id", reviewerID)
		return nil, fmt.Errorf("booking not found or not eligible for review")
	}
	if reviewerID == req.RevieweeID {
		return nil, fmt.Errorf("reviewer and reviewee cannot be the same user")
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
		slog.Error("failed to insert review", "error", err, "booking_id", req.BookingID)
		return nil, fmt.Errorf("review already submitted or invalid booking")
	}

	_, err = tx.Exec(ctx,
		`UPDATE users
		 SET avg_rating = ROUND((((avg_rating * total_reviews) + $2)::numeric / NULLIF((total_reviews + 1), 0)), 1),
		     total_reviews = total_reviews + 1,
		     updated_at = now()
		 WHERE id = $1`, req.RevieweeID, req.Rating,
	)
	if err != nil {
		slog.Error("failed to update user avg rating", "error", err, "reviewee_id", req.RevieweeID)
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("failed to commit review tx", "error", err)
		return nil, err
	}

	err = r.db.QueryRow(ctx,
		`SELECT name FROM users WHERE id = $1`, reviewerID,
	).Scan(&review.ReviewerName)
	if err != nil {
		slog.Error("failed to fetch reviewer name", "error", err, "reviewer_id", reviewerID)
		return nil, err
	}

	slog.Info("review successfully saved into db", "review_id", review.ID, "booking_id", req.BookingID)
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
		slog.Error("GetByReviewee db query failed", "error", err, "reviewee_id", revieweeID)
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
			slog.Error("GetByReviewee row scan failed", "error", err)
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
