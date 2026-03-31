package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func main() {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	token := hex.EncodeToString(bytes)

	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])
	
	fmt.Println("Token:", token)
	fmt.Println("Hash:", tokenHash)
	
	// Simulate what RefreshSession does:
	hashRaw := sha256.Sum256([]byte(token))
	tokenHash2 := hex.EncodeToString(hashRaw[:])
	
	if tokenHash == tokenHash2 {
		fmt.Println("Match!")
	} else {
		fmt.Println("Mismatch!")
	}
}
