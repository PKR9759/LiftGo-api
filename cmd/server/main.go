// cmd/server/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"

	"github.com/PKR9759/LiftGo-api/internal/auth"
	"github.com/PKR9759/LiftGo-api/internal/booking"
	"github.com/PKR9759/LiftGo-api/internal/db"
	"github.com/PKR9759/LiftGo-api/internal/review"
	"github.com/PKR9759/LiftGo-api/internal/ride"
	"github.com/PKR9759/LiftGo-api/internal/seek"
	"github.com/PKR9759/LiftGo-api/internal/user"
	"github.com/PKR9759/LiftGo-api/internal/ws"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, reading from environment")
	}

	ctx := context.Background()

	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()
	log.Println("database connected")

	if err := db.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	log.Println("migrations complete")

	// ── websocket hub ───────────────────────────────────────
	hub := ws.NewHub()
	go hub.Run()

	// ── handlers ────────────────────────────────────────────
	authHandler := auth.NewHandler(auth.NewService(pool))
	userHandler := user.NewHandler(user.NewService(user.NewRepository(pool)))
	rideHandler := ride.NewHandler(ride.NewService(ride.NewRepository(pool)))
	seekHandler := seek.NewHandler(seek.NewService(seek.NewRepository(pool)))
	bookingHandler := booking.NewHandler(booking.NewService(booking.NewRepository(pool)))
	reviewHandler := review.NewHandler(review.NewService(review.NewRepository(pool)))
	wsHandler := ws.NewHandler(hub, pool, []byte(os.Getenv("JWT_SECRET")))

	// ── router ───────────────────────────────────────────────
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: true,
	}))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// ── auth ─────────────────────────────────────────────────
	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
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

	// ── rides ─────────────────────────────────────────────────
	r.Get("/api/rides/nearby", rideHandler.FindNearby)
	r.Route("/api/rides", func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Post("/", rideHandler.Create)
		r.Get("/mine", rideHandler.GetMine)
		r.Get("/{id}", rideHandler.GetByID)
		r.Put("/{id}", rideHandler.Update)
		r.Put("/{id}/status", rideHandler.UpdateStatus)
		r.Delete("/{id}", rideHandler.Cancel)
	})

	// ── seeks ─────────────────────────────────────────────────
	r.Get("/api/seeks/nearby", seekHandler.FindNearby)
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
	})

	// ── reviews ───────────────────────────────────────────────
	r.Route("/api/reviews", func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Post("/", reviewHandler.Create)
	})

	// ── websocket routes ──────────────────────────────────────
	// Mounted outside auth middleware because token is tested manually via query param
	r.Get("/ws/driver/{bookingID}", wsHandler.DriverWS)
	r.Get("/ws/rider/{bookingID}", wsHandler.RiderWS)

	// ── start ─────────────────────────────────────────────────
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("LiftGo API running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
