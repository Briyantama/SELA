// Package config loads the API's settings from environment variables and validates them at startup.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultHTTPPort = "8080"
	defaultGRPCPort = "9090"
	minHMACKeyLen   = 32
	maxPort         = 65535
)

// Config holds every setting the API needs. Secret fields are never printed.
type Config struct {
	HTTPPort string
	GRPCPort string

	DatabaseURL string

	RedisAddr     string
	RedisPassword string

	SMTPAddr     string
	SMTPFrom     string
	SMTPUsername string
	SMTPPassword string

	// OTPHMACKey keys the HMAC that protects stored one-time codes.
	OTPHMACKey []byte

	// CookieSecure marks the session cookie Secure. Only disable for plain-HTTP local development.
	CookieSecure bool
}

// Load reads and validates the configuration, reporting every problem at once.
// Error messages name variables but never include their values.
func Load(getenv func(string) string) (Config, error) {
	var problems []string

	required := func(name string) string {
		value := getenv(name)
		if value == "" {
			problems = append(problems, name+" is required")
		}
		return value
	}
	port := func(name, fallback string) string {
		value := getenv(name)
		if value == "" {
			return fallback
		}
		if n, err := strconv.Atoi(value); err != nil || n < 1 || n > maxPort {
			problems = append(problems, name+" must be a port number between 1 and 65535")
		}
		return value
	}

	cfg := Config{
		HTTPPort:      port("PORT", defaultHTTPPort),
		GRPCPort:      port("GRPC_PORT", defaultGRPCPort),
		DatabaseURL:   required("DATABASE_URL"),
		RedisAddr:     required("REDIS_ADDR"),
		RedisPassword: getenv("REDIS_PASSWORD"),
		SMTPAddr:      required("SMTP_ADDR"),
		SMTPFrom:      required("SMTP_FROM"),
		SMTPUsername:  getenv("SMTP_USERNAME"),
		SMTPPassword:  getenv("SMTP_PASSWORD"),
		CookieSecure:  true,
	}

	if key := required("OTP_HMAC_KEY"); key != "" {
		if len(key) < minHMACKeyLen {
			problems = append(problems, fmt.Sprintf("OTP_HMAC_KEY must be at least %d characters", minHMACKeyLen))
		}
		cfg.OTPHMACKey = []byte(key)
	}

	if raw := getenv("COOKIE_SECURE"); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			problems = append(problems, "COOKIE_SECURE must be true or false")
		}
		cfg.CookieSecure = secure
	}

	if len(problems) > 0 {
		return Config{}, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

// String summarizes the configuration with every secret redacted.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{http=:%s grpc=:%s redis=%s smtp=%s from=%s cookieSecure=%t database=[redacted] redisPassword=[redacted] smtpPassword=[redacted] otpKey=[redacted]}",
		c.HTTPPort, c.GRPCPort, c.RedisAddr, c.SMTPAddr, c.SMTPFrom, c.CookieSecure,
	)
}
