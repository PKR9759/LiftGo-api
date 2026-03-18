// internal/seek/model.go
package seek

import "time"

type Seek struct {
	ID             string    `json:"id"`
	SeekerID       string    `json:"seeker_id"`
	SeekerName     string    `json:"seeker_name"`
	SeekerRating   float64   `json:"seeker_avg_rating"`
	SeekerReviews  int       `json:"seeker_total_reviews"`
	OriginLat      float64   `json:"origin_lat"`
	OriginLng      float64   `json:"origin_lng"`
	OriginLabel    string    `json:"origin_label"`
	DestLat        float64   `json:"dest_lat"`
	DestLng        float64   `json:"dest_lng"`
	DestLabel      string    `json:"dest_label"`
	SeatsNeeded    int       `json:"seats_needed"`
	IsRecurring    bool      `json:"is_recurring"`
	RecurrenceDays []int     `json:"recurrence_days,omitempty"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

type CreateRequest struct {
	OriginLat      float64 `json:"origin_lat"`
	OriginLng      float64 `json:"origin_lng"`
	OriginLabel    string  `json:"origin_label"`
	DestLat        float64 `json:"dest_lat"`
	DestLng        float64 `json:"dest_lng"`
	DestLabel      string  `json:"dest_label"`
	SeatsNeeded    int     `json:"seats_needed"`
	IsRecurring    bool    `json:"is_recurring"`
	RecurrenceDays []int   `json:"recurrence_days"`
}

type NearbyParams struct {
	OriginLat    float64
	OriginLng    float64
	DestLat      float64
	DestLng      float64
	RadiusMeters float64
}