package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	GRPCAddress            string
	DatabaseURL            string
	AuthorizationAddress   string
	IdentityAddress        string
	ZitiManagementAddress  string
	GroupsAddress          string
	NATSURL                string
	GroupSyncDurable       string
	ReconciliationInterval time.Duration
	FirstAdminEmail        string
}

func FromEnv() (Config, error) {
	cfg := Config{}
	cfg.GRPCAddress = os.Getenv("GRPC_ADDRESS")
	if cfg.GRPCAddress == "" {
		cfg.GRPCAddress = ":50051"
	}
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must be set")
	}
	cfg.AuthorizationAddress = os.Getenv("AUTHORIZATION_ADDRESS")
	if cfg.AuthorizationAddress == "" {
		cfg.AuthorizationAddress = "authorization:50051"
	}
	cfg.IdentityAddress = os.Getenv("IDENTITY_ADDRESS")
	if cfg.IdentityAddress == "" {
		cfg.IdentityAddress = "identity:50051"
	}
	cfg.ZitiManagementAddress = os.Getenv("ZITI_MANAGEMENT_ADDRESS")
	if cfg.ZitiManagementAddress == "" {
		cfg.ZitiManagementAddress = "ziti-management:50051"
	}
	cfg.GroupsAddress = os.Getenv("GROUPS_ADDRESS")
	if cfg.GroupsAddress == "" {
		cfg.GroupsAddress = "groups:50051"
	}
	cfg.NATSURL = os.Getenv("NATS_URL")
	if cfg.NATSURL == "" {
		cfg.NATSURL = "nats://nats:4222"
	}
	cfg.GroupSyncDurable = os.Getenv("GROUP_SYNC_DURABLE")
	if cfg.GroupSyncDurable == "" {
		cfg.GroupSyncDurable = "users-group-sync"
	}
	reconciliationInterval := os.Getenv("GROUP_SYNC_RECONCILIATION_INTERVAL")
	if reconciliationInterval == "" {
		reconciliationInterval = "60s"
	}
	duration, err := time.ParseDuration(reconciliationInterval)
	if err != nil {
		return Config{}, fmt.Errorf("GROUP_SYNC_RECONCILIATION_INTERVAL: %w", err)
	}
	cfg.ReconciliationInterval = duration
	// Optional. Unset means the first sign-in takes cluster admin.
	cfg.FirstAdminEmail = os.Getenv("FIRST_ADMIN_EMAIL")
	return cfg, nil
}
