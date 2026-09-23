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
	"github.com/Briyantama/SELA/internal/objstore"
	"github.com/Briyantama/SELA/services/auth"
	"github.com/Briyantama/SELA/services/event"
	"github.com/Briyantama/SELA/services/media"
	"github.com/Briyantama/SELA/services/rbac"
)

const (
	readTimeout     = 10 * time.Second
	writeTimeout    = 15 * time.Second
	shutdownTimeout = 10 * time.Second
	connectTimeout  = 10 * time.Second
	// sweepInterval is how often abandoned uploads are failed and their shots returned.
	sweepInterval = time.Minute
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("api exited with error", "error", err)
		stop()
		os.Exit(1)
	}
}

// run serves HTTP and gRPC until ctx is cancelled, then shuts both down gracefully.
func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	slog.Info("starting api", "config", cfg.String())

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

	eventSvc := event.NewService(event.NewPostgresRepository(conn), event.Options{ShortLinkBaseURL: cfg.ShortLinkBaseURL})
	rbacSvc := rbac.NewService(rbac.NewPostgresRepository(conn))

	store, err := objstore.NewS3(objstore.S3Config{
		Endpoint:        cfg.S3.Endpoint,
		Region:          cfg.S3.Region,
		Bucket:          cfg.S3.Bucket,
		AccessKeyID:     cfg.S3.AccessKeyID,
		SecretAccessKey: cfg.S3.SecretAccessKey,
		PathStyle:       cfg.S3.PathStyle,
	})
	if err != nil {
		return err
	}
	// Guest tokens are hashed under the same secret as OTPs, with their own domain prefix.
	mediaSvc, err := media.NewService(media.NewPostgresRepository(conn), media.NewRedisGuests(rdb), store, media.Options{
		HMACKey:    cfg.OTPHMACKey,
		SessionTTL: cfg.GuestSessionTTL,
	})
	if err != nil {
		return err
	}
	go mediaSvc.RunSweeper(ctx, sweepInterval)

	authHTTP := auth.NewHTTPHandler(svc, auth.HTTPConfig{CookieSecure: cfg.CookieSecure})
	rbacHTTP := rbac.NewHTTPHandler(rbacSvc, authHTTP)
	mediaHTTP := media.NewHTTPHandler(mediaSvc, authHTTP, media.HTTPConfig{CookieSecure: cfg.CookieSecure})
	httpSrv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      newMux(authHTTP, event.NewHTTPHandler(eventSvc, authHTTP, rbacHTTP), rbacHTTP, mediaHTTP),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	grpcSrv := newGRPCServer(svc, eventSvc)
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
