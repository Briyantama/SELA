// Package testdb gives integration tests an isolated PostgreSQL database.
//
// It connects to the server in TEST_DATABASE_URL, creates a uniquely named
// database for the test, and drops it on cleanup. Tests are skipped when the
// variable is unset; scripts/check.sh requires it so the official validation
// never skips silently.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

const envVar = "TEST_DATABASE_URL"

// New returns a connection to a fresh, empty database that is dropped when the test ends.
func New(t *testing.T) *sql.DB {
	t.Helper()

	adminURL := os.Getenv(envVar)
	if adminURL == "" {
		t.Skipf("%s not set; start Postgres (deploy/docker-compose.yml) and export it to run DB tests", envVar)
	}

	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}

	name := "sela_test_" + randomSuffix(t)
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		_ = admin.Close()
		t.Fatalf("create test database: %v", err)
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		_ = admin.Close()
		t.Fatalf("parse %s: %v", envVar, err)
	}
	u.Path = "/" + name

	testDB, err := sql.Open("pgx", u.String())
	if err != nil {
		_ = admin.Close()
		t.Fatalf("open test database: %v", err)
	}

	t.Cleanup(func() {
		_ = testDB.Close()
		if _, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
		_ = admin.Close()
	})

	return testDB
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate database suffix: %v", err)
	}
	return hex.EncodeToString(buf)
}
