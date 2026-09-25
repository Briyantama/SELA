package db_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// migrationVersions maps each goose version prefix in migrations/ to the files that carry it.
func migrationVersions(t *testing.T) map[int][]string {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	versions := map[int][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, found := strings.Cut(name, "_")
		if !found {
			t.Fatalf("migration %q has no version prefix", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("migration %q: version prefix %q is not a number: %v", name, prefix, err)
		}
		versions[version] = append(versions[version], name)
	}
	return versions
}

func TestMigrations_versionsAreUnique(t *testing.T) {
	// Arrange
	versions := migrationVersions(t)

	// Act / Assert
	for version, files := range versions {
		if len(files) > 1 {
			t.Errorf("version %d is used by %d migrations: %v (goose rejects duplicate versions)", version, len(files), files)
		}
	}
}

func TestMigrations_guestMediaRunsAfterRBAC(t *testing.T) {
	// Arrange
	versions := migrationVersions(t)
	versionOf := func(suffix string) int {
		t.Helper()
		for version, files := range versions {
			for _, file := range files {
				if strings.HasSuffix(file, suffix) {
					return version
				}
			}
		}
		t.Fatalf("no migration named *%s", suffix)
		return 0
	}

	// Act
	media := versionOf("_guest_sessions_media.sql")
	hostsRole := versionOf("_hosts_role_and_preferences.sql")

	// Assert
	if media <= hostsRole {
		t.Fatalf("guest_sessions_media is version %d, want it after hosts_role_and_preferences (%d)", media, hostsRole)
	}
}
