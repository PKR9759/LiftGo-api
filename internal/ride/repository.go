// internal/ride/repository.go
package ride

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PKR9759/LiftGo-api/internal/audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, driverID string, req CreateRequest) (*Ride, error) {
	slog.Info("Creating ride for driver", "driver_id", driverID, "departing", req.DepartureAt)

	departure, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		return nil, fmt.Errorf("invalid departure_at format, use ISO 8601")
	}

	// Build route geometry: real polyline from waypoints, or straight line fallback
	var routeWKT *string
	if len(req.Waypoints) >= 2 {
		points := make([]string, len(req.Waypoints))
		for i, wp := range req.Waypoints {
			points[i] = fmt.Sprintf("%.7f %.7f", wp.Lng, wp.Lat) // PostGIS WKT order: lng lat
		}
		wkt := "LINESTRING(" + strings.Join(points, ", ") + ")"
		routeWKT = &wkt
		slog.Debug("Built route WKT from waypoints", "waypoint_count", len(req.Waypoints), "wkt_length", len(wkt))
	}

	ride := &Ride{}

	if routeWKT != nil {
		// Insert with real polyline
		err = r.db.QueryRow(ctx,
			`INSERT INTO rides (
				driver_id,
				origin_lat, origin_lng, origin_label,
				dest_lat,   dest_lng,   dest_label,
				route,
				departure_at, total_seats, available_seats,
				price_per_seat, is_recurring, recurrence_days, notes, status
			) VALUES (
				$1,
				$2, $3, $4,
				$5, $6, $7,
				ST_SetSRID(ST_GeomFromText($8), 4326),
				$9, $10, $10,
				$11, $12, $13, $14, 'scheduled'
			)
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
			*routeWKT,
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
			slog.Error("Failed to create ride with polyline", "error", err, "driver_id", driverID)
			return nil, err
		}
		slog.Info("Ride created with real route polyline", "ride_id", ride.ID, "waypoint_count", len(req.Waypoints), "status", ride.Status)
	} else {
		// Fallback: 2-point straight line (original behavior)
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
				price_per_seat, is_recurring, recurrence_days, notes, status
			)
			SELECT
				$1,
				$2, $3, $4,
				$5, $6, $7,
				ST_SetSRID(ST_MakeLine(pts.origin_pt, pts.dest_pt), 4326),
				$8, $9, $9,
				$10, $11, $12, $13, 'scheduled'
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
			slog.Error("Failed to create ride with straight line", "error", err, "driver_id", driverID)
			return nil, err
		}
		slog.Info("Ride created with straight-line fallback", "ride_id", ride.ID, "status", ride.Status)
	}

	audit.Log(ctx, r.db, "ride", ride.ID, driverID, "created", nil, map[string]any{
		"origin_label":   req.OriginLabel,
		"dest_label":     req.DestLabel,
		"departure_at":   req.DepartureAt,
		"total_seats":    req.TotalSeats,
		"price_per_seat": req.PricePerSeat,
	})

	return ride, nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (*Ride, error) {
	ride := &Ride{}
	var routeGeoJSON *string
	err := r.db.QueryRow(ctx,
		`SELECT r.id, r.driver_id, u.name, u.avg_rating, u.total_reviews,
		        r.origin_lat, r.origin_lng, r.origin_label,
		        r.dest_lat,   r.dest_lng,   r.dest_label,
		        r.departure_at, r.total_seats, r.available_seats,
		        r.price_per_seat, r.is_recurring, r.recurrence_days,
		        r.notes, r.status, r.created_at,
		        ST_AsGeoJSON(r.route) as route_geojson
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
		&routeGeoJSON,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("ride not found")
		}
		return nil, err
	}

	if routeGeoJSON != nil {
		ride.RouteCoordinates = parseGeoJSON(*routeGeoJSON)
	}

	return ride, nil
}

// FindNearby — the core geo matching query.
// Uses the actual stored route polyline for 4-layer spatial matching:
//  1. Proximity: pickup & dropoff within radius of driver route
//  2. Direction: pickup comes before dropoff along the route
//  3. Seats: driver has enough available seats
//  4. Relevance scoring: pickup proximity + dropoff proximity + route coverage + driver rating + seat availability
func (r *Repository) FindNearby(ctx context.Context, p NearbyParams) ([]*Ride, error) {
	radius := p.RadiusMeters
	if radius <= 0 {
		radius = 1000 // default 1km — tighter for real road routes
	}
	seatsNeeded := p.SeatsNeeded
	if seatsNeeded <= 0 {
		seatsNeeded = 1
	}

	slog.Debug("FindNearby searching",
		"origin_lat", p.OriginLat, "origin_lng", p.OriginLng,
		"dest_lat", p.DestLat, "dest_lng", p.DestLng,
		"radius", radius, "seats_needed", seatsNeeded)

	start := time.Now()

	rows, err := r.db.Query(ctx,
		`SELECT
		  r.id,
		  r.driver_id,
		  u.name                  AS driver_name,
		  u.avg_rating            AS driver_avg_rating,
		  u.total_reviews         AS driver_total_reviews,
		  r.origin_lat,
		  r.origin_lng,
		  r.origin_label,
		  r.dest_lat,
		  r.dest_lng,
		  r.dest_label,
		  r.departure_at,
		  r.total_seats,
		  r.available_seats,
		  r.price_per_seat,
		  r.is_recurring,
		  r.recurrence_days,
		  r.notes,
		  r.status,
		  r.created_at,
		  ST_AsGeoJSON(r.route) AS route_geojson,

		  -- distance from passenger pickup to nearest point on driver route
		  ROUND(ST_Distance(
		    r.route::geography,
		    ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography
		  )::numeric, 1) AS pickup_distance_m,

		  -- distance from passenger dropoff to nearest point on driver route
		  ROUND(ST_Distance(
		    r.route::geography,
		    ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography
		  )::numeric, 1) AS dropoff_distance_m,

		  -- fraction 0.0-1.0: where pickup falls along driver route
		  ROUND(ST_LineLocatePoint(
		    r.route,
		    ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($2, $1), 4326))
		  )::numeric, 4) AS pickup_fraction,

		  -- fraction 0.0-1.0: where dropoff falls along driver route
		  ROUND(ST_LineLocatePoint(
		    r.route,
		    ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($4, $3), 4326))
		  )::numeric, 4) AS dropoff_fraction,

		  -- percentage of passenger journey covered by this driver route
		  ROUND((
		    ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($4,$3),4326)))
		    -
		    ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($2,$1),4326)))
		  )::numeric * 100, 1) AS route_coverage_pct,

		  -- relevance score (higher = better match, max ~6.0)
		  (
		    GREATEST(0.0, 1.0 - ST_Distance(
		      r.route::geography,
		      ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography
		    ) / $5)
		    +
		    GREATEST(0.0, 1.0 - ST_Distance(
		      r.route::geography,
		      ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography
		    ) / $5)
		    +
		    GREATEST(0.0, (
		      ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($4,$3),4326)))
		      -
		      ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($2,$1),4326)))
		    )) * 2.0
		    +
		    (COALESCE(u.avg_rating, 3.0) / 5.0)
		    +
		    (r.available_seats::float / NULLIF(r.total_seats, 0)::float)
		  ) AS match_score

		FROM rides r
		JOIN users u ON u.id = r.driver_id

		WHERE
		  r.status IN ('scheduled', 'active')
		  AND r.available_seats >= $7
		  -- Step 2.3 — Segment-specific capacity check
		  AND (
		    r.total_seats - (
		      SELECT COALESCE(SUM(b2.seats), 0)
		      FROM bookings b2
		      WHERE b2.ride_id = r.id
		        AND b2.status IN ('confirmed', 'rider_ready', 'picked_up')
		        -- Overlap check: booking starts before passenger ends AND ends after passenger starts
		        AND b2.pickup_fraction < ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($4, $3), 4326)))
		        AND b2.dropoff_fraction > ST_LineLocatePoint(r.route, ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($2, $1), 4326)))
		    )
		  ) >= $7
		  AND r.departure_at > (now() - interval '1 hour')
		  AND ($6 = '' OR r.driver_id::text != $6)
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
		  AND ST_LineLocatePoint(
		    r.route,
		    ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($2, $1), 4326))
		  )
		  <
		  ST_LineLocatePoint(
		    r.route,
		    ST_ClosestPoint(r.route, ST_SetSRID(ST_MakePoint($4, $3), 4326))
		  )

		ORDER BY match_score DESC, r.departure_at ASC`,
		p.OriginLat, p.OriginLng,
		p.DestLat, p.DestLng,
		radius,
		p.ExcludeUserID,
		seatsNeeded,
	)
	if err != nil {
		slog.Error("FindNearby query error", "error", err)
		return nil, err
	}
	defer rows.Close()

	rides, err := scanMatchRides(rows)
	if err != nil {
		return nil, err
	}

	elapsed := time.Since(start).Milliseconds()
	topScore := 0.0
	if len(rides) > 0 {
		topScore = rides[0].MatchScore
	}
	slog.Info("FindNearby completed",
		"found_rides", len(rides),
		"duration_ms", elapsed,
		"top_match_score", fmt.Sprintf("%.2f", topScore))

	if len(rides) == 0 {
		slog.Info("FindNearby returned zero results — direction filter or radius may have filtered all candidates")
	}

	return rides, nil
}

func (r *Repository) GetByDriver(ctx context.Context, driverID string) ([]*Ride, error) {
	slog.Info("Fetching rides for driver", "driver_id", driverID)
	rows, err := r.db.Query(ctx,
		`SELECT r.id, r.driver_id, u.name, u.avg_rating, u.total_reviews,
		        r.origin_lat, r.origin_lng, r.origin_label,
		        r.dest_lat,   r.dest_lng,   r.dest_label,
		        r.departure_at, r.total_seats, r.available_seats,
		        r.price_per_seat, r.is_recurring, r.recurrence_days,
		        r.notes, r.status, r.created_at,
		        ST_AsGeoJSON(r.route) as route_geojson
		 FROM rides r
		 JOIN users u ON u.id = r.driver_id
		 WHERE r.driver_id = $1
		 ORDER BY r.departure_at DESC`, driverID,
	)
	if err != nil {
		slog.Error("GetByDriver error", "error", err, "driver_id", driverID)
		return nil, err
	}
	defer rows.Close()

	rides, err := scanRides(rows)
	if err != nil {
		return nil, err
	}
	slog.Info("GetByDriver found rides", "count", len(rides), "driver_id", driverID)
	return rides, nil
}

func (r *Repository) Update(ctx context.Context, id, driverID string, req UpdateRequest) (*Ride, error) {
	oldRide, _ := r.GetByID(ctx, id)
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

	action := "updated"
	if oldRide != nil && oldRide.PricePerSeat != ride.PricePerSeat {
		action = "price_changed"
	}
	audit.Log(ctx, r.db, "ride", ride.ID, driverID, action, oldRide, ride)

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
	audit.Log(ctx, r.db, "ride", id, driverID, "status_changed", nil, map[string]string{"status": status})
	return nil
}

func (r *Repository) Cancel(ctx context.Context, id, driverID string) error {
	result, err := r.db.Exec(ctx,
		`UPDATE rides SET status = 'cancelled', updated_at = now()
		 WHERE id = $1 AND driver_id = $2 AND status IN ('scheduled', 'full')`,
		id, driverID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("ride not found or already cancelled")
	}
	audit.Log(ctx, r.db, "ride", id, driverID, "cancelled", nil, map[string]string{"status": "cancelled"})
	return nil
}

func scanRides(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*Ride, error) {
	var rides []*Ride
	for rows.Next() {
		r := &Ride{}
		var routeGeoJSON *string
		err := rows.Scan(
			&r.ID, &r.DriverID, &r.DriverName,
			&r.DriverRating, &r.DriverReviews,
			&r.OriginLat, &r.OriginLng, &r.OriginLabel,
			&r.DestLat, &r.DestLng, &r.DestLabel,
			&r.DepartureAt, &r.TotalSeats, &r.AvailableSeats,
			&r.PricePerSeat, &r.IsRecurring, &r.RecurrenceDays,
			&r.Notes, &r.Status, &r.CreatedAt,
			&routeGeoJSON,
		)
		if err != nil {
			slog.Error("scanRides Scan error", "error", err)
			return nil, err
		}
		if routeGeoJSON != nil {
			r.RouteCoordinates = parseGeoJSON(*routeGeoJSON)
		}
		rides = append(rides, r)
	}

	if err := rows.Err(); err != nil {
		slog.Error("scanRides rows.Err", "error", err)
		return nil, err
	}

	return rides, nil
}

// scanMatchRides scans the extended FindNearby result set that includes
// match scoring fields (pickup_distance_m, dropoff_distance_m, pickup_fraction,
// dropoff_fraction, route_coverage_pct, match_score) appended after the base columns.
func scanMatchRides(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*Ride, error) {
	var rides []*Ride
	for rows.Next() {
		r := &Ride{}
		var routeGeoJSON *string
		err := rows.Scan(
			&r.ID, &r.DriverID, &r.DriverName,
			&r.DriverRating, &r.DriverReviews,
			&r.OriginLat, &r.OriginLng, &r.OriginLabel,
			&r.DestLat, &r.DestLng, &r.DestLabel,
			&r.DepartureAt, &r.TotalSeats, &r.AvailableSeats,
			&r.PricePerSeat, &r.IsRecurring, &r.RecurrenceDays,
			&r.Notes, &r.Status, &r.CreatedAt,
			&routeGeoJSON,
			// match scoring fields
			&r.PickupDistanceM, &r.DropoffDistanceM,
			&r.PickupFraction, &r.DropoffFraction,
			&r.RouteCoveragePct, &r.MatchScore,
		)
		if err != nil {
			slog.Error("scanMatchRides Scan error", "error", err)
			return nil, err
		}
		if routeGeoJSON != nil {
			r.RouteCoordinates = parseGeoJSON(*routeGeoJSON)
		}
		rides = append(rides, r)
	}

	if err := rows.Err(); err != nil {
		slog.Error("scanMatchRides rows.Err", "error", err)
		return nil, err
	}

	return rides, nil
}

func parseGeoJSON(s string) []Waypoint {
	var geo struct {
		Coordinates [][]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal([]byte(s), &geo); err != nil {
		slog.Error("Failed to unmarshal route GeoJSON", "error", err)
		return nil
	}
	waypoints := make([]Waypoint, len(geo.Coordinates))
	for i, c := range geo.Coordinates {
		// GeoJSON is [lng, lat]
		waypoints[i] = Waypoint{Lat: c[1], Lng: c[0]}
	}
	return waypoints
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
