package main

import (
  	"context"
  	"errors"
  	"fmt"
  	"log"
  	"net"
  	"os"
  	"os/signal"
  	"syscall"

  	usersv1 "github.com/agynio/users/.gen/go/agynio/api/users/v1"
  	"github.com/jackc/pgx/v5/pgxpool"
  	"google.golang.org/grpc"

  	"github.com/agynio/users/internal/config"
  	"github.com/agynio/users/internal/db"
  	"github.com/agynio/users/internal/server"
  	"github.com/agynio/users/internal/store"
)

func main() {
  	if err := run(); err != nil {
  		log.Fatalf("users-service: %v", err)
  	}
}

func run() error {
  	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  	defer stop()

  	cfg, err := config.FromEnv()
  	if err != nil {
  		return err
  	}

  	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
  	if err != nil {
  		return fmt.Errorf("parse database url: %w", err)
  	}
  	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
  	if err != nil {
  		return fmt.Errorf("create connection pool: %w", err)
  	}
  	defer pool.Close()

  	if err := db.ApplyMigrations(ctx, pool); err != nil {
  		return fmt.Errorf("apply migrations: %w", err)
  	}

  	grpcServer := grpc.NewServer()
  	usersv1.RegisterUsersServiceServer(grpcServer, server.New(store.New(pool)))

  	lis, err := net.Listen("tcp", cfg.GRPCAddress)
  	if err != nil {
  		return fmt.Errorf("listen on %s: %w", cfg.GRPCAddress, err)
  	}

  	go func() {
  		<-ctx.Done()
  		grpcServer.GracefulStop()
  	}()

  	log.Printf("UsersService listening on %s", cfg.GRPCAddress)

  	if err := grpcServer.Serve(lis); err != nil {
  		if errors.Is(err, grpc.ErrServerStopped) {
  			return nil
  		}
  		return fmt.Errorf("serve: %w", err)
  	}
  	return nil
}
