// internal/user/repository.go
package user

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) GetByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx,
		`SELECT id, name, email, phone, avatar_url,
		        avg_rating, total_reviews, role, created_at
		 FROM users WHERE id = $1`, id,
	).Scan(
		&u.ID, &u.Name, &u.Email, &u.Phone,
		&u.AvatarURL, &u.AvgRating, &u.TotalReviews,
		&u.Role, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *Repository) Update(ctx context.Context, id string, req UpdateRequest) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx,
		`UPDATE users
		 SET name       = COALESCE(NULLIF($1,''), name),
		     phone      = COALESCE(NULLIF($2,''), phone),
		     avatar_url = COALESCE(NULLIF($3,''), avatar_url),
		     role       = COALESCE(NULLIF($4,''), role),
		     updated_at = now()
		 WHERE id = $5
		 RETURNING id, name, email, phone, avatar_url,
		           avg_rating, total_reviews, role, created_at`,
		req.Name, req.Phone, req.AvatarURL, req.Role, id,
	).Scan(
		&u.ID, &u.Name, &u.Email, &u.Phone,
		&u.AvatarURL, &u.AvgRating, &u.TotalReviews,
		&u.Role, &u.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}