// cmd/server/main.go
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/booking"
	"github.com/PKR9759/LiftGo-api/internal/cache"
	"github.com/PKR9759/LiftGo-api/internal/db"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/review"
	"github.com/PKR9759/LiftGo-api/internal/ride"
	"github.com/PKR9759/LiftGo-api/internal/user"
	"github.com/PKR9759/LiftGo-api/internal/ws"
)

func main() {
	var level slog.Level
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil {
		slog.Debug("no .env file, reading from environment")
	}

	ctx := context.Background()

	pool, err := db.Connect(ctx)
	if err != nil {
		slog.Error("db connect failure", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	slog.Info("database connected")

	if err := db.RunMigrations(ctx, pool); err != nil {
		slog.Error("migrations failure", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations complete")

	// ── redis ────────────────────────────────────────────────
	redisClient, _ := cache.NewClient(ctx)
	// redisClient may be nil if REDIS_URL is unset / USE_CACHE=false — all
	// downstream callers are nil-safe and skip Redis logic in that case.

	// ── websocket hub ───────────────────────────────────────
	hub := ws.NewHub()
	go hub.Run()

	// ── notifications ───────────────────────────────────────
	emailClient := notification.NewEmailClient()
	pushClient := notification.NewPushClient(pool)

	// ── handlers ────────────────────────────────────────────
	authHandler := auth.NewHandler(auth.NewService(pool))
	userHandler := user.NewHandler(user.NewService(user.NewRepository(pool)))
	rideHandler := ride.NewHandler(ride.NewService(ride.NewRepository(pool)), pool, emailClient, pushClient, hub, redisClient)
	bookingHandler := booking.NewHandler(booking.NewService(booking.NewRepository(pool)), pool, emailClient, pushClient, hub, redisClient)
	reviewHandler := review.NewHandler(review.NewService(review.NewRepository(pool)))
	wsHandler := ws.NewHandler(hub, pool, []byte(os.Getenv("JWT_SECRET")))

	// ── router ───────────────────────────────────────────────
	r := chi.NewRouter()

	// ── global middleware (must be declared before any routes in chi) ──
	allowedOrigins := allowedOriginsFromEnv()
	r.Use(customMiddleware.RequestID)
	r.Use(customMiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// ── websocket routes (no rate limiter — long-lived upgrades) ──────
	// Auth is enforced inside the handler via JWT query param / cookie.
	r.Group(func(r chi.Router) {
		r.Get("/ws/driver/{bookingID}", wsHandler.DriverWS)
		r.Get("/ws/rider/{bookingID}", wsHandler.RiderWS)
	})

	// ── auth critical paths (no rate limit — needed for WS reconnects) ──
	// /refresh is called before every WS reconnect attempt; rate limiting it
	// cascades into WS failures. /logout must always succeed to clear sessions.
	r.Post("/api/auth/refresh", authHandler.Refresh)
	r.Post("/api/auth/logout", authHandler.Logout)

	// ── REST routes (rate-limited) ─────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(customMiddleware.RateLimit(redisClient))

		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			useCache := os.Getenv("USE_CACHE") == "true"

			// Ping database
			dbStatus := "ok"
			if err := pool.Ping(r.Context()); err != nil {
				dbStatus = "error"
			}

			// Ping Redis
			redisStatus := "disabled"
			if useCache {
				if redisClient == nil {
					redisStatus = "error"
				} else if _, err := redisClient.Ping(r.Context()).Result(); err != nil {
					redisStatus = "error"
				} else {
					redisStatus = "ok"
				}
			}

			// Overall status: degraded when Redis is required but unavailable
			overallStatus := "ok"
			if dbStatus == "error" || (useCache && redisStatus == "error") {
				overallStatus = "degraded"
			}

			w.Header().Set("Content-Type", "application/json")
			if overallStatus == "degraded" {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":        overallStatus,
				"database":      dbStatus,
				"redis":         redisStatus,
				"cache_enabled": useCache,
			})
		})

		// ── auth ─────────────────────────────────────────────────
		r.Route("/api/auth", func(r chi.Router) {
			r.Post("/register", authHandler.Register)
			r.Post("/login", authHandler.Login)
			// /refresh and /logout are outside the rate-limited group (registered above)

			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Get("/me", authHandler.GetMe)
			})
		})

		// ── users ─────────────────────────────────────────────────
		r.Route("/api/users", func(r chi.Router) {
			r.Get("/{id}/reviews", reviewHandler.GetByReviewee)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Get("/me", userHandler.GetMe)
				r.Put("/me", userHandler.UpdateMe)
			})
		})

		// ── notifications ────────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Post("/api/push/subscribe", notification.SubscribeHandler(pushClient))
		})

		// ── rides ─────────────────────────────────────────────────
		r.With(auth.PopulateUser).Get("/api/rides/nearby", rideHandler.FindNearby)
		r.Route("/api/rides", func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Post("/", rideHandler.Create)
			r.Get("/mine", rideHandler.GetMine)
			r.Get("/{id}", rideHandler.GetByID)
			r.Put("/{id}", rideHandler.Update)
			r.Put("/{id}/status", rideHandler.UpdateStatus)
			r.Delete("/{id}", rideHandler.Cancel)
			r.Get("/{id}/bookings", bookingHandler.GetRideBookings)
			r.Put("/{id}/start-ride", bookingHandler.StartRide)
			r.Get("/{id}/status-summary", rideHandler.GetStatusSummary)
		})

		// ── bookings ──────────────────────────────────────────────
		r.Route("/api/bookings", func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Post("/", bookingHandler.Create)
			r.Get("/mine", bookingHandler.GetMine)
			r.Get("/incoming", bookingHandler.GetIncoming)
			r.Get("/{id}", bookingHandler.GetByID)
			r.Put("/{id}/confirm", bookingHandler.Confirm)
			r.Put("/{id}/cancel", bookingHandler.Cancel)
			r.Put("/{id}/rider-ready", bookingHandler.RiderReady)
			r.Put("/{id}/picked-up", bookingHandler.PickedUp)
			r.Put("/{id}/dropped", bookingHandler.Dropped)
			r.Put("/{id}/no-show", bookingHandler.NoShow)
		})

		// ── reviews ───────────────────────────────────────────────
		r.Route("/api/reviews", func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Post("/", reviewHandler.Create)
		})
	})

	// ── start ─────────────────────────────────────────────────
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("LiftGo API starting", "port", port)

	err = http.ListenAndServe(":"+port, r)
	if err != nil {
		slog.Error("server fatal error", "error", err)
		os.Exit(1)
	}
}

func allowedOriginsFromEnv() []string {
	if raw := os.Getenv("CORS_ALLOWED_ORIGINS"); raw != "" {
		parts := strings.Split(raw, ",")
		origins := make([]string, 0, len(parts))
		for _, part := range parts {
			origin := strings.TrimSpace(part)
			if origin != "" {
				origins = append(origins, origin)
			}
		}
		if len(origins) > 0 {
			return origins
		}
	}

	return []string{
		"http://localhost:3000",
		"https://liftgo.tech",
		"https://www.liftgo.tech",
	}
}
