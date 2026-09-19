// Command api runs the Sela API: HTTP (REST) and gRPC servers over the same services.
// Configuration comes from environment variables (see internal/config); run
// `go run ./cmd/migrate` first to apply the database migrations.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/Briyantama/SELA/internal/config"
	"github.com/Briyantama/SELA/services/auth"
)

const (
	readTimeout     = 10 * time.Second
	writeTimeout    = 15 * time.Second
	shutdownTimeout = 10 * time.Second
	connectTimeout  = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	slog.Info("starting api", "config", cfg.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := connectPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	rdb, err := connectRedis(ctx, cfg)
	if err != nil {
		return err
	}
	defer rdb.Close()

	svc := auth.NewService(
		auth.NewRedisStore(rdb),
		auth.NewPostgresHosts(conn),
		auth.NewSMTPMailer(auth.SMTPConfig{
			Addr:     cfg.SMTPAddr,
			From:     cfg.SMTPFrom,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
		}),
		auth.Options{HMACKey: cfg.OTPHMACKey},
	)

	httpSrv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      newMux(auth.NewHTTPHandler(svc, auth.HTTPConfig{CookieSecure: cfg.CookieSecure})),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	grpcSrv := newGRPCServer(svc)
	grpcListener, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return fmt.Errorf("listen for grpc: %w", err)
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("http listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		slog.Info("grpc listening", "addr", grpcListener.Addr().String())
		if err := grpcSrv.Serve(grpcListener); err != nil {
			errCh <- fmt.Errorf("grpc server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		grpcSrv.Stop()
		_ = httpSrv.Close()
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		return shutdown(httpSrv, grpcSrv.GracefulStop)
	}
}

func shutdown(httpSrv *http.Server, stopGRPC func()) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		stopGRPC()
		close(done)
	}()
	err := httpSrv.Shutdown(ctx)
	select {
	case <-done:
	case <-ctx.Done():
	}
	return err
}

func connectPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := conn.PingContext(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return conn, nil
}

func connectRedis(ctx context.Context, cfg config.Config) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return rdb, nil
}
