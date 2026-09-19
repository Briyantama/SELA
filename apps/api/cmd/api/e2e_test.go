package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/Briyantama/SELA/gen/go/auth/v1"
	"github.com/Briyantama/SELA/internal/db"
	"github.com/Briyantama/SELA/internal/testdb"
)

var sixDigits = regexp.MustCompile(`\b(\d{6})\b`)

// captureSMTP accepts SMTP mail and remembers every message body.
type captureSMTP struct {
	addr string
	mu   sync.Mutex
	msgs []string
}

func startCaptureSMTP(t *testing.T) *captureSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &captureSMTP{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *captureSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(l string) { _, _ = conn.Write([]byte(l + "\r\n")) }
	reply("220 capture ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 localhost")
		case strings.HasPrefix(cmd, "MAIL FROM:"), strings.HasPrefix(cmd, "RCPT TO:"), cmd == "RSET", cmd == "NOOP":
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.msgs = append(s.msgs, b.String())
			s.mu.Unlock()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func (s *captureSMTP) lastCode(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if n := len(s.msgs); n > 0 {
			msg := s.msgs[n-1]
			s.mu.Unlock()
			if code := sixDigits.FindString(msg); code != "" {
				return code
			}
			t.Fatalf("no 6-digit code in message: %q", msg)
		}
		s.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no email arrived")
	return ""
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func waitForHealth(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the API never became healthy")
}

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestRun_serversTheWholeSignInFlowOverHTTPAndGRPCThenStopsCleanly(t *testing.T) {
	// Arrange: real Postgres, in-process Redis and SMTP.
	dbURL := testdb.NewURL(t)
	conn, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	if err := db.Up(context.Background(), conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mr := miniredis.RunT(t)
	smtpSrv := startCaptureSMTP(t)
	httpPort, grpcPort := freePort(t), freePort(t)

	env := map[string]string{
		"DATABASE_URL":  dbURL,
		"REDIS_ADDR":    mr.Addr(),
		"SMTP_ADDR":     smtpSrv.addr,
		"SMTP_FROM":     "no-reply@sela.test",
		"OTP_HMAC_KEY":  "0123456789abcdef0123456789abcdef-e2e-only",
		"PORT":          httpPort,
		"GRPC_PORT":     grpcPort,
		"COOKIE_SECURE": "false",
	}
	getenv := func(k string) string { return env[k] }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, getenv) }()

	base := "http://127.0.0.1:" + httpPort
	waitForHealth(t, base)

	// Act + Assert: HTTP sign-in.
	resp := postJSON(t, base+"/api/v1/auth/otp/request", `{"email":"e2e@example.test"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request otp status = %d", resp.StatusCode)
	}
	code := smtpSrv.lastCode(t)

	resp = postJSON(t, base+"/api/v1/auth/otp/verify", fmt.Sprintf(`{"email":"e2e@example.test","code":"%s"}`, code))
	var verified struct {
		Success bool `json:"success"`
		Data    struct {
			HostID    string `json:"host_id"`
			IsNewHost bool   `json:"is_new_host"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verified); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !verified.Success || !verified.Data.IsNewHost {
		t.Fatalf("verify status = %d, body = %+v", resp.StatusCode, verified)
	}
	var sessionSet bool
	for _, c := range resp.Cookies() {
		if c.Name == "sela_session" && c.HttpOnly && c.Value != "" {
			sessionSet = true
		}
	}
	if !sessionSet {
		t.Error("no HttpOnly session cookie on the verify response")
	}
	var stored int
	if err := conn.QueryRow(`SELECT count(*) FROM hosts WHERE host_id = $1 AND email = 'e2e@example.test'`, verified.Data.HostID).Scan(&stored); err != nil || stored != 1 {
		t.Errorf("host row count = %d, %v; want 1", stored, err)
	}

	// The gRPC server answers on its own port.
	gconn, err := grpc.NewClient("127.0.0.1:"+grpcPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	defer gconn.Close()
	gres, err := authv1.NewAuthServiceClient(gconn).RequestOtp(context.Background(), &authv1.RequestOtpRequest{Email: "grpc@example.test"})
	if err != nil || gres.GetExpiresInSeconds() != 300 {
		t.Errorf("grpc RequestOtp = (%v, %v)", gres, err)
	}

	// Shutdown: cancelling the context stops both servers and run returns nil.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after shutdown, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after the context was cancelled")
	}
}

func TestRun_failsFastOnInvalidConfiguration(t *testing.T) {
	// Act
	err := run(context.Background(), func(string) string { return "" })

	// Assert
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want a configuration error naming DATABASE_URL", err)
	}
}

func TestRun_failsWhenPostgresIsUnreachable(t *testing.T) {
	// Arrange
	env := map[string]string{
		"DATABASE_URL": "postgres://nobody@127.0.0.1:1/none?sslmode=disable",
		"REDIS_ADDR":   "127.0.0.1:1",
		"SMTP_ADDR":    "127.0.0.1:1",
		"SMTP_FROM":    "no-reply@sela.test",
		"OTP_HMAC_KEY": "0123456789abcdef0123456789abcdef-e2e-only",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Act
	err := run(ctx, func(k string) string { return env[k] })

	// Assert
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("err = %v, want a postgres connection error", err)
	}
}

func TestRun_failsWhenRedisIsUnreachable(t *testing.T) {
	// Arrange: a real database, but nothing listening for Redis.
	env := map[string]string{
		"DATABASE_URL": testdb.NewURL(t),
		"REDIS_ADDR":   "127.0.0.1:1",
		"SMTP_ADDR":    "127.0.0.1:1",
		"SMTP_FROM":    "no-reply@sela.test",
		"OTP_HMAC_KEY": "0123456789abcdef0123456789abcdef-e2e-only",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Act
	err := run(ctx, func(k string) string { return env[k] })

	// Assert
	if err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("err = %v, want a redis connection error", err)
	}
}
