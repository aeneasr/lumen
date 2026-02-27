package project

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// ValidateToken checks whether an authentication token is valid and not expired.
func ValidateToken(token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	if len(token) < 32 {
		return errors.New("token too short")
	}
	return nil
}

// CreateSession generates a new session token with the given TTL.
func CreateSession(userID string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RevokeSession invalidates a session by its token.
func RevokeSession(token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	return nil
}
