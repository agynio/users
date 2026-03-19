//go:build e2e
// +build e2e

package e2e

import "os"

var usersAddr = envOrDefault("USERS_ADDR", "users:50051")

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
