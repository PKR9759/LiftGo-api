package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := "postgresql://neondb_owner:npg_eSxXMd0CVWI6@ep-long-wildflower-a1vg3l9a-pooler.ap-southeast-1.aws.neon.tech/neondb?sslmode=require&channel_binding=require"
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		fmt.Println("DB error:", err)
		os.Exit(1)
	}
	defer pool.Close()

	bookingID := "7ea7c086-3787-4d2a-b5ab-e16561b60017"
	userID := "7e1be0ec-1431-4cfc-aa66-0eec5837954e"

	var exists bool
	var actualRiderID, actualStatus string
	
	err = pool.QueryRow(context.Background(), "SELECT rider_id, status FROM bookings WHERE id = $1", bookingID).Scan(&actualRiderID, &actualStatus)
	if err != nil {
		fmt.Println("Booking not found or error:", err)
	} else {
		fmt.Printf("Booking exists! RiderID in DB: %s, Status: %s\n", actualRiderID, actualStatus)
		if actualRiderID != userID {
			fmt.Printf("MISMATCH! Token userID is %s\n", userID)
		}
	}

	query := `
		SELECT EXISTS(
			SELECT 1 FROM bookings b
			WHERE b.id = $1 AND b.rider_id = $2 AND b.status IN ('confirmed', 'rider_ready', 'picked_up')
		)`
	err = pool.QueryRow(context.Background(), query, bookingID, userID).Scan(&exists)
	if err != nil {
		fmt.Println("Query error:", err)
		return
	}
	fmt.Println("RiderWS check returned exists =", exists)
}
