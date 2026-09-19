package config_test

import (
	"strings"
	"testing"

	"github.com/Briyantama/SELA/internal/config"
)

const testHMACKey = "0123456789abcdef0123456789abcdef-test-only"

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func fullEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":   "postgres://user:dbsecret@127.0.0.1:5432/sela?sslmode=disable",
		"REDIS_ADDR":     "127.0.0.1:6379",
		"REDIS_PASSWORD": "redissecret",
		"SMTP_ADDR":      "127.0.0.1:1025",
		"SMTP_FROM":      "no-reply@sela.test",
		"OTP_HMAC_KEY":   testHMACKey,
	}
}

func TestLoad_readsARequiredEnvironment(t *testing.T) {
	// Arrange
	getenv := envFrom(fullEnv())

	// Act
	cfg, err := config.Load(getenv)

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL == "" || cfg.RedisAddr != "127.0.0.1:6379" || cfg.SMTPAddr != "127.0.0.1:1025" || cfg.SMTPFrom != "no-reply@sela.test" {
		t.Errorf("unexpected config: %s", cfg)
	}
	if string(cfg.OTPHMACKey) != testHMACKey {
		t.Error("OTP_HMAC_KEY not loaded")
	}
}

func TestLoad_appliesSafeDefaults(t *testing.T) {
	// Arrange
	getenv := envFrom(fullEnv())

	// Act
	cfg, err := config.Load(getenv)

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPPort != "8080" || cfg.GRPCPort != "9090" {
		t.Errorf("ports = %s/%s, want 8080/9090", cfg.HTTPPort, cfg.GRPCPort)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure must default to true")
	}
}

func TestLoad_reportsEveryMissingRequiredVariable(t *testing.T) {
	// Act
	_, err := config.Load(envFrom(nil))

	// Assert
	if err == nil {
		t.Fatal("Load with an empty environment returned nil")
	}
	for _, name := range []string{"DATABASE_URL", "REDIS_ADDR", "SMTP_ADDR", "SMTP_FROM", "OTP_HMAC_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q should mention %s", err, name)
		}
	}
}

func TestLoad_rejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantMsg string
	}{
		{"short HMAC key", "OTP_HMAC_KEY", "too-short", "OTP_HMAC_KEY"},
		{"non-numeric port", "PORT", "eighty", "PORT"},
		{"port out of range", "GRPC_PORT", "70000", "GRPC_PORT"},
		{"bad boolean", "COOKIE_SECURE", "banana", "COOKIE_SECURE"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			env := fullEnv()
			env[tc.key] = tc.value

			// Act
			_, err := config.Load(envFrom(env))

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("err = %v, want it to mention %s", err, tc.wantMsg)
			}
		})
	}
}

func TestLoad_errorsNeverEchoSecretValues(t *testing.T) {
	// Arrange
	env := fullEnv()
	env["OTP_HMAC_KEY"] = "short-secret"

	// Act
	_, err := config.Load(envFrom(env))

	// Assert
	if err == nil {
		t.Fatal("expected an error for the short key")
	}
	if strings.Contains(err.Error(), "short-secret") {
		t.Errorf("error leaks the key value: %v", err)
	}
}

func TestLoad_allowsInsecureCookiesForLocalDevelopment(t *testing.T) {
	// Arrange
	env := fullEnv()
	env["COOKIE_SECURE"] = "false"

	// Act
	cfg, err := config.Load(envFrom(env))

	// Assert
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false when COOKIE_SECURE=false")
	}
}

func TestConfigString_redactsSecrets(t *testing.T) {
	// Arrange
	env := fullEnv()
	env["SMTP_USERNAME"] = "mailer"
	env["SMTP_PASSWORD"] = "smtpsecret"
	cfg, err := config.Load(envFrom(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Act
	printed := cfg.String()

	// Assert
	for _, secret := range []string{"dbsecret", "redissecret", "smtpsecret", testHMACKey} {
		if strings.Contains(printed, secret) {
			t.Errorf("String() leaks %q: %s", secret, printed)
		}
	}
}
