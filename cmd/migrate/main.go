// Command migrate applies (up) or reverts (down) the SQL migrations in db/migrations
// against the PostgreSQL database named by DATABASE_URL.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Briyantama/SELA/db"
)

const (
	usage       = "usage: migrate [up|down]"
	pingTimeout = 10 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		slog.Error("migrate failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	command, err := parseCommand(args)
	if err != nil {
		return err
	}

	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}

	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := conn.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	switch command {
	case "down":
		err = db.Down(ctx, conn)
	default:
		err = db.Up(ctx, conn)
	}
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "migrate %s: done\n", command)
	return err
}

func parseCommand(args []string) (string, error) {
	switch len(args) {
	case 0:
		return "up", nil
	case 1:
		if args[0] == "up" || args[0] == "down" {
			return args[0], nil
		}
	}
	return "", errors.New(usage)
}
