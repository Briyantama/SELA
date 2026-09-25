// Package config loads the API's settings from environment variables and validates them at startup.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPPort = "8080"
	defaultGRPCPort = "9090"
	minHMACKeyLen   = 32
	maxPort         = 65535

	// defaultGuestSessionTTL is how long an anonymous guest session lives. The docs define no value,
	// so it is a configurable default recorded as pending product input (FSD 8.6).
	defaultGuestSessionTTL = 72 * time.Hour
)

// S3 locates the S3-compatible bucket that holds event media (FSD 2.1). The bucket is private: media is
// only ever reached through short-lived pre-signed URLs (FR-SEC.1).
type S3 struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle addresses the bucket as {endpoint}/{bucket}, which MinIO needs.
	PathStyle bool
}

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

	// ShortLinkBaseURL is the public origin (plus optional path prefix) that event short links and
	// QR codes point to, without a trailing slash. It is configurable so the domain can change (D4).
	ShortLinkBaseURL string

	// S3 is the media bucket.
	S3 S3

	// GuestSessionTTL bounds an anonymous guest session and its shot counter in Redis.
	GuestSessionTTL time.Duration
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
		S3: S3{
			Region:          required("S3_REGION"),
			Bucket:          required("S3_BUCKET"),
			AccessKeyID:     required("S3_ACCESS_KEY_ID"),
			SecretAccessKey: required("S3_SECRET_ACCESS_KEY"),
			PathStyle:       true,
		},
		GuestSessionTTL: defaultGuestSessionTTL,
	}

	if raw := required("S3_ENDPOINT"); raw != "" {
		endpoint, ok := normalizeBaseURL(raw)
		if !ok {
			problems = append(problems, "S3_ENDPOINT must be an absolute http or https URL without credentials, query or fragment")
		}
		cfg.S3.Endpoint = endpoint
	}

	if raw := getenv("S3_PATH_STYLE"); raw != "" {
		pathStyle, err := strconv.ParseBool(raw)
		if err != nil {
			problems = append(problems, "S3_PATH_STYLE must be true or false")
		}
		cfg.S3.PathStyle = pathStyle
	}

	if raw := getenv("GUEST_SESSION_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl <= 0 {
			problems = append(problems, "GUEST_SESSION_TTL must be a positive duration such as 72h")
		}
		cfg.GuestSessionTTL = ttl
	}

	if key := required("OTP_HMAC_KEY"); key != "" {
		if len(key) < minHMACKeyLen {
			problems = append(problems, fmt.Sprintf("OTP_HMAC_KEY must be at least %d characters", minHMACKeyLen))
		}
		cfg.OTPHMACKey = []byte(key)
	}

	if raw := required("SHORT_LINK_BASE_URL"); raw != "" {
		base, ok := normalizeBaseURL(raw)
		if !ok {
			problems = append(problems, "SHORT_LINK_BASE_URL must be an absolute http or https URL without credentials, query or fragment")
		}
		cfg.ShortLinkBaseURL = base
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

// normalizeBaseURL accepts an absolute http(s) URL with no credentials, query or fragment and
// returns it without a trailing slash.
func normalizeBaseURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw, "?#") {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}

// String summarizes the configuration with every secret redacted.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{http=:%s grpc=:%s redis=%s smtp=%s from=%s shortLinkBase=%s cookieSecure=%t s3=%s/%s guestSessionTTL=%s database=[redacted] redisPassword=[redacted] smtpPassword=[redacted] otpKey=[redacted] s3Secret=[redacted]}",
		c.HTTPPort, c.GRPCPort, c.RedisAddr, c.SMTPAddr, c.SMTPFrom, c.ShortLinkBaseURL, c.CookieSecure,
		c.S3.Endpoint, c.S3.Bucket, c.GuestSessionTTL,
	)
}
