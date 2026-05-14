package main

import (
	"fmt"
	"log"

	"github.com/gorilla/websocket"
)

func main() {
	url := "ws://localhost:8080/ws/rider/7ea7c086-3787-4d2a-b5ab-e16561b60017?token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiN2UxYmUwZWMtMTQzMS00Y2ZjLWFhNjYtMGVlYzU4Mzc5NTRlIiwiZW1haWwiOiJrdWxkaXBycGFybWFyOTc5QGdtYWlsLmNvbSIsInJvbGUiOiJib3RoIiwiZXhwIjoxNzc4NzUyMDMxLCJpYXQiOjE3Nzg3NTExMzF9.SBv98th3RmtE_uwEw0UrTCq6Jcu4cS66F5tl-vzX4Mk"

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp != nil {
			fmt.Printf("Dial failed with status: %d\n", resp.StatusCode)
			// read body
			buf := make([]byte, 1024)
			n, _ := resp.Body.Read(buf)
			fmt.Printf("Response body: %s\n", string(buf[:n]))
		}
		log.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	fmt.Println("Connected successfully!")
}
