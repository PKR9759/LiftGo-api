// cmd/server/main.go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/booking"
	"github.com/PKR9759/LiftGo-api/internal/db"
	customMiddleware "github.com/PKR9759/LiftGo-api/internal/middleware"
	"github.com/PKR9759/LiftGo-api/internal/notification"
	"github.com/PKR9759/LiftGo-api/internal/review"
	"github.com/PKR9759/LiftGo-api/internal/ride"
	"github.com/PKR9759/LiftGo-api/internal/seek"
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

	// ── websocket hub ───────────────────────────────────────
	hub := ws.NewHub()
	go hub.Run()

	// ── notifications ───────────────────────────────────────
	emailClient := notification.NewEmailClient()
	pushClient := notification.NewPushClient(pool)

	// ── handlers ────────────────────────────────────────────
	authHandler := auth.NewHandler(auth.NewService(pool))
	userHandler := user.NewHandler(user.NewService(user.NewRepository(pool)))
	rideHandler := ride.NewHandler(ride.NewService(ride.NewRepository(pool)), pool, emailClient, pushClient)
	seekHandler := seek.NewHandler(seek.NewService(seek.NewRepository(pool)))
	bookingHandler := booking.NewHandler(booking.NewService(booking.NewRepository(pool)), pool, emailClient, pushClient, hub)
	reviewHandler := review.NewHandler(review.NewService(review.NewRepository(pool)))
	wsHandler := ws.NewHandler(hub, pool, []byte(os.Getenv("JWT_SECRET")))

	// ── router ───────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(customMiddleware.RequestID)
	r.Use(customMiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:3000",
			"https://liftgo.tech",
			"https://www.liftgo.tech",
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Requested-With"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// ── auth ─────────────────────────────────────────────────
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/refresh", authHandler.Refresh)
		r.Post("/logout", authHandler.Logout)

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

	// ── seeks ─────────────────────────────────────────────────
	r.With(auth.PopulateUser).Get("/api/seeks/nearby", seekHandler.FindNearby)
	r.Route("/api/seeks", func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Post("/", seekHandler.Create)
		r.Get("/mine", seekHandler.GetMine)
		r.Get("/{id}", seekHandler.GetByID)
		r.Delete("/{id}", seekHandler.Cancel)
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

	// ── websocket routes ──────────────────────────────────────
	r.Get("/ws/driver/{bookingID}", wsHandler.DriverWS)
	r.Get("/ws/rider/{bookingID}", wsHandler.RiderWS)

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
