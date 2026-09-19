package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/testdb"
)

func env(url string) func(string) string {
	return func(key string) string {
		if key == "DATABASE_URL" {
			return url
		}
		return ""
	}
}

func eventsTableExists(t *testing.T, url string) bool {
	t.Helper()
	conn, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()
	var exists bool
	if err := conn.QueryRow(`SELECT to_regclass('public.events') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	return exists
}

func TestRun_upAppliesPendingMigrations(t *testing.T) {
	// Arrange
	url := testdb.NewURL(t)
	var out bytes.Buffer

	// Act
	err := run(context.Background(), []string{"up"}, env(url), &out)

	// Assert
	if err != nil {
		t.Fatalf("run up: %v", err)
	}
	if !eventsTableExists(t, url) {
		t.Error("events table missing after up")
	}
	if !strings.Contains(out.String(), "up") {
		t.Errorf("output %q should confirm the up command", out.String())
	}
}

func TestRun_defaultsToUpAndIsRepeatable(t *testing.T) {
	// Arrange
	url := testdb.NewURL(t)

	// Act
	first := run(context.Background(), nil, env(url), &bytes.Buffer{})
	second := run(context.Background(), nil, env(url), &bytes.Buffer{})

	// Assert
	if first != nil || second != nil {
		t.Fatalf("run with no args twice: first=%v second=%v, want both nil", first, second)
	}
	if !eventsTableExists(t, url) {
		t.Error("events table missing after default run")
	}
}

func TestRun_downRevertsMigrations(t *testing.T) {
	// Arrange
	url := testdb.NewURL(t)
	if err := run(context.Background(), []string{"up"}, env(url), &bytes.Buffer{}); err != nil {
		t.Fatalf("seed up: %v", err)
	}

	// Act
	err := run(context.Background(), []string{"down"}, env(url), &bytes.Buffer{})

	// Assert
	if err != nil {
		t.Fatalf("run down: %v", err)
	}
	if eventsTableExists(t, url) {
		t.Error("events table still exists after down")
	}
}

func TestRun_rejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		getenv  func(string) string
		wantMsg string
	}{
		{"missing DATABASE_URL", []string{"up"}, env(""), "DATABASE_URL"},
		{"unknown command", []string{"sideways"}, env("postgres://x"), "usage"},
		{"too many arguments", []string{"up", "down"}, env("postgres://x"), "usage"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			err := run(context.Background(), tc.args, tc.getenv, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantMsg)
			}
		})
	}
}

func TestRun_reportsAnUnreachableDatabase(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Act
	err := run(ctx, []string{"up"}, env("postgres://postgres@127.0.0.1:1/none?sslmode=disable"), &bytes.Buffer{})

	// Assert
	if err == nil {
		t.Fatal("run against an unreachable database returned nil, want an error")
	}
}
