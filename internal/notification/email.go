package notification

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/resend/resend-go/v2"
)

type EmailClient struct {
	resendClient *resend.Client
	senderEmail  string
}

func NewEmailClient() *EmailClient {
	apiKey := os.Getenv("RESEND_API_KEY")
	senderEmail := os.Getenv("RESEND_FROM_EMAIL")

	client := resend.NewClient(apiKey)

	return &EmailClient{
		resendClient: client,
		senderEmail:  senderEmail,
	}
}

func (c *EmailClient) SendNewBookingRequestToDriver(driverEmail, driverName, riderName, origin, destination string, departureTime time.Time, seats int) {
	go func() {
		subject := "New booking request — LiftGo"
		formattedTime := departureTime.Format("Jan 02, 2006 at 03:04 PM")

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>%s has requested %d seat(s) on your ride from %s to %s departing at %s.</p>
			<p>Please open the LiftGo app to confirm or decline the request.</p>
			<p>Thanks,<br>The LiftGo Team</p>
		`, driverName, riderName, seats, origin, destination, formattedTime)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{driverEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send new booking request email to %s: %v", driverEmail, err)
		}
	}()
}

func (c *EmailClient) SendBookingConfirmedToRider(riderEmail, riderName, driverName, origin, destination string, departureTime time.Time) {
	go func() {
		subject := "Booking confirmed — LiftGo"
		formattedTime := departureTime.Format("Jan 02, 2006 at 03:04 PM")

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>Your booking is confirmed! Your driver for the trip is %s.</p>
			<p><strong>Ride Details:</strong><br>From: %s<br>To: %s<br>Departure: %s</p>
			<p>Thanks for riding with LiftGo!</p>
		`, riderName, driverName, origin, destination, formattedTime)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{riderEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send booking confirmed email to %s: %v", riderEmail, err)
		}
	}()
}

func (c *EmailClient) SendDriverStartedRideToRider(riderEmail, riderName, driverName string) {
	go func() {
		subject := "Your driver is on the way — LiftGo"

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>%s has started the ride and is on the way to pick you up.</p>
			<p>Open the LiftGo app to track their live location.</p>
			<p>Enjoy your trip,<br>The LiftGo Team</p>
		`, riderName, driverName)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{riderEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send driver started ride email to %s: %v", riderEmail, err)
		}
	}()
}

func (c *EmailClient) SendRideCompletedToRider(riderEmail, riderName, driverName string) {
	go func() {
		subject := "Trip completed — LiftGo"

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>Your trip with %s has been completed.</p>
			<p>Thank you for using LiftGo! Please open the app to leave a review for %s.</p>
			<p>The LiftGo Team</p>
		`, riderName, driverName, driverName)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{riderEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send ride completed email to rider %s: %v", riderEmail, err)
		}
	}()
}

func (c *EmailClient) SendRideCompletedToDriver(driverEmail, driverName, riderName string) {
	go func() {
		subject := "Trip completed — LiftGo"

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>Your trip with %s has been completed.</p>
			<p>Thank you for driving with LiftGo! Please open the app to leave a review for %s.</p>
			<p>The LiftGo Team</p>
		`, driverName, riderName, riderName)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{driverEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send ride completed email to driver %s: %v", driverEmail, err)
		}
	}()
}

func (c *EmailClient) SendBookingCancelled(recipientEmail, recipientName, cancelledByName, origin, destination string) {
	go func() {
		subject := "Booking cancelled — LiftGo"

		htmlBody := fmt.Sprintf(`
			<p>Hi %s,</p>
			<p>We're writing to inform you that %s has cancelled the booking for the ride from %s to %s.</p>
			<p>Please open the LiftGo app for more details or to find an alternative ride.</p>
			<p>Best regards,<br>The LiftGo Team</p>
		`, recipientName, cancelledByName, origin, destination)

		params := &resend.SendEmailRequest{
			From:    c.senderEmail,
			To:      []string{recipientEmail},
			Subject: subject,
			Html:    htmlBody,
		}

		_, err := c.resendClient.Emails.Send(params)
		if err != nil {
			log.Printf("Failed to send booking cancelled email to %s: %v", recipientEmail, err)
		}
	}()
}
