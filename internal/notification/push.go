package notification

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PushClient struct {
	publicKey  string
	privateKey string
	email      string
	db         *pgxpool.Pool
}

func NewPushClient(pool *pgxpool.Pool) *PushClient {
	return &PushClient{
		publicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		privateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		email:      os.Getenv("VAPID_EMAIL"),
		db:         pool,
	}
}

func (c *PushClient) SaveSubscription(userID string, endpoint, p256dh, auth string) error {
	ctx := context.Background()
	_, err := c.db.Exec(ctx,
		`INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) 
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, endpoint) DO NOTHING`,
		userID, endpoint, p256dh, auth,
	)
	return err
}

func (c *PushClient) SendToUser(userID string, title, body string) {
	go func() {
		ctx := context.Background()
		rows, err := c.db.Query(ctx, "SELECT id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1", userID)
		if err != nil {
			log.Printf("Failed to query push subscriptions for user %s: %v", userID, err)
			return
		}
		defer rows.Close()

		type subRow struct {
			ID       int
			Endpoint string
			P256dh   string
			Auth     string
		}

		var subs []subRow
		for rows.Next() {
			var s subRow
			if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth); err == nil {
				subs = append(subs, s)
			}
		}

		if len(subs) == 0 {
			return // No subscriptions
		}

		payloadBytes, _ := json.Marshal(map[string]string{
			"title": title,
			"body":  body,
		})

		for _, s := range subs {
			sub := &webpush.Subscription{
				Endpoint: s.Endpoint,
				Keys: webpush.Keys{
					P256dh: s.P256dh,
					Auth:   s.Auth,
				},
			}

			resp, err := webpush.SendNotification(payloadBytes, sub, &webpush.Options{
				Subscriber:      c.email,
				VAPIDPublicKey:  c.publicKey,
				VAPIDPrivateKey: c.privateKey,
				TTL:             30,
			})

			if err != nil {
				log.Printf("Failed to send push to sub %d (user %s): %v", s.ID, userID, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode == 410 {
				_, delErr := c.db.Exec(ctx, "DELETE FROM push_subscriptions WHERE id = $1", s.ID)
				if delErr != nil {
					log.Printf("Failed to delete expired subscription %d: %v", s.ID, delErr)
				}
			}
		}
	}()
}

func (c *PushClient) PushNewBookingRequest(driverUserID string, riderName, origin, destination string) {
	title := "New Booking Request"
	body := riderName + " requested a seat from " + origin + " to " + destination
	c.SendToUser(driverUserID, title, body)
}

func (c *PushClient) PushBookingConfirmed(riderUserID string, driverName, origin, destination string) {
	title := "Booking Confirmed"
	body := "Your ride with " + driverName + " from " + origin + " to " + destination + " is confirmed"
	c.SendToUser(riderUserID, title, body)
}

func (c *PushClient) PushDriverStartedRide(riderUserID string, driverName string) {
	title := "Driver is on the way"
	body := driverName + " has started the ride and is on their way"
	c.SendToUser(riderUserID, title, body)
}

func (c *PushClient) PushRideCompleted(userID string) {
	title := "Trip Completed"
	body := "Your trip has been completed. Please leave a review!"
	c.SendToUser(userID, title, body)
}

func (c *PushClient) PushBookingCancelled(recipientUserID string, cancelledByName string) {
	title := "Booking Cancelled"
	body := cancelledByName + " has cancelled the booking"
	c.SendToUser(recipientUserID, title, body)
}
