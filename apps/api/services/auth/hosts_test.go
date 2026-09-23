package auth_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Briyantama/SELA/internal/db"
	"github.com/Briyantama/SELA/internal/testdb"
	"github.com/Briyantama/SELA/services/auth"
)

func TestPostgresHosts_createsAHostOnFirstSignIn(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	hosts := auth.NewPostgresHosts(conn)

	// Act
	id, created, err := hosts.FindOrCreateByEmail(context.Background(), "host@example.test")

	// Assert
	if err != nil {
		t.Fatalf("FindOrCreateByEmail: %v", err)
	}
	if !created || len(id) != 36 {
		t.Fatalf("got (%q, created=%v), want a new UUID host", id, created)
	}
	var email string
	if err := conn.QueryRow(`SELECT email FROM hosts WHERE host_id = $1`, id).Scan(&email); err != nil || email != "host@example.test" {
		t.Errorf("stored email = %q, %v", email, err)
	}
}

func TestPostgresHosts_returnsTheSameHostForTheSameAddress(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	hosts := auth.NewPostgresHosts(conn)
	first, _, err := hosts.FindOrCreateByEmail(context.Background(), "host@example.test")
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Act
	second, created, err := hosts.FindOrCreateByEmail(context.Background(), "HOST@Example.Test")

	// Assert
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if created || second != first {
		t.Errorf("got (%q, created=%v), want (%q, false)", second, created, first)
	}
}

func TestPostgresHosts_concurrentFirstSignInsCreateOneHost(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	hosts := auth.NewPostgresHosts(conn)
	const workers = 8
	type result struct {
		id      string
		created bool
		err     error
	}
	results := make(chan result, workers)

	// Act
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, created, err := hosts.FindOrCreateByEmail(context.Background(), "race@example.test")
			results <- result{id, created, err}
		}()
	}
	wg.Wait()
	close(results)

	// Assert
	creations := 0
	ids := map[string]bool{}
	for r := range results {
		if r.err != nil {
			t.Fatalf("FindOrCreateByEmail: %v", r.err)
		}
		ids[r.id] = true
		if r.created {
			creations++
		}
	}
	if creations != 1 || len(ids) != 1 {
		t.Fatalf("creations=%d distinct ids=%d, want 1 and 1", creations, len(ids))
	}
	var rows int
	if err := conn.QueryRow(`SELECT count(*) FROM hosts`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("hosts rows = %d, %v; want 1", rows, err)
	}
}

func TestPostgresHosts_reportsDatabaseFailures(t *testing.T) {
	// Arrange
	conn := testdb.New(t)
	hosts := auth.NewPostgresHosts(conn) // no migrations applied, so the table does not exist

	// Act
	_, _, err := hosts.FindOrCreateByEmail(context.Background(), "host@example.test")

	// Assert
	if err == nil {
		t.Fatal("expected an error when the hosts table is missing")
	}
}
