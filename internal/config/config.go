package config

import (
  	"fmt"
  	"os"
)

type Config struct {
  	GRPCAddress string
  	DatabaseURL string
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
  	return cfg, nil
}
