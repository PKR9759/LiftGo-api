// internal/ride/model.go
package ride

import "time"

// Waypoint represents a single lat/lng point on a route polyline.
type Waypoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type Ride struct {
	ID             string    `json:"id"`
	DriverID       string    `json:"driver_id"`
	DriverName     string    `json:"driver_name"`
	DriverRating   float64   `json:"driver_avg_rating"`
	DriverReviews  int       `json:"driver_total_reviews"`
	OriginLat      float64   `json:"origin_lat"`
	OriginLng      float64   `json:"origin_lng"`
	OriginLabel    string    `json:"origin_label"`
	DestLat        float64   `json:"dest_lat"`
	DestLng        float64   `json:"dest_lng"`
	DestLabel      string    `json:"dest_label"`
	DepartureAt    time.Time `json:"departure_at"`
	TotalSeats     int       `json:"total_seats"`
	AvailableSeats int       `json:"available_seats"`
	PricePerSeat   float64   `json:"price_per_seat"`
	IsRecurring    bool      `json:"is_recurring"`
	RecurrenceDays []int     `json:"recurrence_days,omitempty"`
	Notes          *string   `json:"notes,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`

	// Match result fields — populated only by FindNearby, zero otherwise
	PickupDistanceM  float64 `json:"pickup_distance_m,omitempty"`
	DropoffDistanceM float64 `json:"dropoff_distance_m,omitempty"`
	PickupFraction   float64 `json:"pickup_fraction,omitempty"`
	DropoffFraction  float64 `json:"dropoff_fraction,omitempty"`
	RouteCoveragePct float64    `json:"route_coverage_pct,omitempty"`
	MatchScore       float64    `json:"match_score,omitempty"`
	RouteCoordinates []Waypoint `json:"route_coordinates,omitempty"`
}

type CreateRequest struct {
	OriginLat      float64    `json:"origin_lat"`
	OriginLng      float64    `json:"origin_lng"`
	OriginLabel    string     `json:"origin_label"`
	DestLat        float64    `json:"dest_lat"`
	DestLng        float64    `json:"dest_lng"`
	DestLabel      string     `json:"dest_label"`
	DepartureAt    string     `json:"departure_at"`
	TotalSeats     int        `json:"total_seats"`
	PricePerSeat   float64    `json:"price_per_seat"`
	IsRecurring    bool       `json:"is_recurring"`
	RecurrenceDays []int      `json:"recurrence_days"`
	Notes          string     `json:"notes"`
	Waypoints      []Waypoint `json:"waypoints"` // optional: real road polyline from Ola Directions
}

type UpdateRequest struct {
	DepartureAt    string  `json:"departure_at"`
	TotalSeats     int     `json:"total_seats"`
	PricePerSeat   float64 `json:"price_per_seat"`
	Notes          string  `json:"notes"`
	IsRecurring    bool    `json:"is_recurring"`
	RecurrenceDays []int   `json:"recurrence_days"`
}

type NearbyParams struct {
	OriginLat     float64 `json:"origin_lat"`
	OriginLng     float64 `json:"origin_lng"`
	DestLat       float64 `json:"dest_lat"`
	DestLng       float64 `json:"dest_lng"`
	RadiusMeters  float64 `json:"radius_meters"`
	ExcludeUserID string  `json:"exclude_user_id"`
	SeatsNeeded   int     `json:"seats_needed"` // minimum available seats required, default 1
}

type UpdateStatusRequest struct {
	Status string `json:"status"`
}
