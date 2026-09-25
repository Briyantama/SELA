package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Briyantama/SELA/internal/config"
)

func TestLoad_readsTheObjectStorageSettings(t *testing.T) {
	// Arrange
	env := fullEnv()

	// Act
	cfg, err := config.Load(envFrom(env))

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.S3{
		Endpoint:        "http://127.0.0.1:9000",
		Region:          "us-east-1",
		Bucket:          "sela-media-test",
		AccessKeyID:     "minio-access",
		SecretAccessKey: "s3secret-value",
		PathStyle:       true,
	}
	if cfg.S3 != want {
		t.Errorf("S3 = %+v, want %+v", cfg.S3, want)
	}
}

func TestLoad_defaultsTheGuestSessionTTL(t *testing.T) {
	// Act
	cfg, err := config.Load(envFrom(fullEnv()))

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GuestSessionTTL != 72*time.Hour {
		t.Errorf("GuestSessionTTL = %v, want 72h", cfg.GuestSessionTTL)
	}
}

func TestLoad_readsOptionalStorageAndGuestOverrides(t *testing.T) {
	// Arrange
	env := fullEnv()
	env["S3_PATH_STYLE"] = "false"
	env["GUEST_SESSION_TTL"] = "24h"

	// Act
	cfg, err := config.Load(envFrom(env))

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.S3.PathStyle {
		t.Error("PathStyle = true, want false")
	}
	if cfg.GuestSessionTTL != 24*time.Hour {
		t.Errorf("GuestSessionTTL = %v, want 24h", cfg.GuestSessionTTL)
	}
}

func TestLoad_reportsEveryMissingStorageVariable(t *testing.T) {
	// Arrange
	env := fullEnv()
	names := []string{"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"}
	for _, name := range names {
		delete(env, name)
	}

	// Act
	_, err := config.Load(envFrom(env))

	// Assert
	if err == nil {
		t.Fatal("Load without storage settings returned nil")
	}
	for _, name := range names {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q should mention %s", err, name)
		}
	}
}

func TestLoad_rejectsInvalidStorageAndGuestValues(t *testing.T) {
	tests := []struct{ name, key, value string }{
		{"endpoint without scheme", "S3_ENDPOINT", "127.0.0.1:9000"},
		{"endpoint with credentials", "S3_ENDPOINT", "http://user:pw@127.0.0.1:9000"},
		{"bad path style", "S3_PATH_STYLE", "sideways"},
		{"unparseable ttl", "GUEST_SESSION_TTL", "three days"},
		{"non-positive ttl", "GUEST_SESSION_TTL", "0s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			env := fullEnv()
			env[tc.key] = tc.value

			// Act
			_, err := config.Load(envFrom(env))

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, want it to mention %s", err, tc.key)
			}
		})
	}
}

func TestConfigString_redactsTheStorageSecret(t *testing.T) {
	// Arrange
	cfg, err := config.Load(envFrom(fullEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Act
	printed := cfg.String()

	// Assert
	if strings.Contains(printed, "s3secret-value") {
		t.Errorf("String() leaks the S3 secret: %s", printed)
	}
	if !strings.Contains(printed, "sela-media-test") {
		t.Errorf("String() should name the bucket: %s", printed)
	}
}
