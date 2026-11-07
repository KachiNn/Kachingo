package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionLifetime = 24 * time.Hour

var errInvalidCredentials = errors.New("invalid credentials")

func validateRegistration(email, name, password string) (string, string, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", "", errors.New("valid email is required")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return "", "", errors.New("name must contain 1 to 100 bytes")
	}
	if len(password) < 12 || len(password) > 72 {
		return "", "", errors.New("password must contain 12 to 72 bytes")
	}
	return email, name, nil
}
func newSessionToken() (string, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}
func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
