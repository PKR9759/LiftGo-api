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
	"github.com/PKR9759/LiftGo-api/internal/db"
	"github.com/PKR9759/LiftGo-api/internal/user"
	"github.com/PKR9759/LiftGo-api/internal/ride"
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

	authHandler := auth.NewHandler(auth.NewService(pool))
	userHandler := user.NewHandler(user.NewService(user.NewRepository(pool)))
	rideHandler := ride.NewHandler(ride.NewService(ride.NewRepository(pool)))

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

	r.Route("/api/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login",    authHandler.Login)
	})

	r.Route("/api/users", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			r.Get("/me",  userHandler.GetMe)
			r.Put("/me",  userHandler.UpdateMe)
		})
	})

	// public rides routes
	r.Get("/api/rides/nearby", rideHandler.FindNearby)
	r.Get("/api/rides/{id}",   rideHandler.GetByID)

	// protected rides routes
	r.Route("/api/rides", func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Post("/",       rideHandler.Create)
		r.Get("/mine",    rideHandler.GetMine)
		r.Put("/{id}",    rideHandler.Update)
		r.Delete("/{id}", rideHandler.Cancel)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("LiftGo API running on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}