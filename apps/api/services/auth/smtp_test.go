package auth_test

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Briyantama/SELA/services/auth"
)

// fakeSMTP is a minimal SMTP server that records what it receives.
type fakeSMTP struct {
	addr string

	mu         sync.Mutex
	from       string
	rcpts      []string
	data       string
	rejectRcpt bool
}

func startFakeSMTP(t *testing.T, rejectRcpt bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	s := &fakeSMTP{addr: ln.Addr().String(), rejectRcpt: rejectRcpt}
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

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 localhost")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.from = strings.TrimSpace(line[len("MAIL FROM:"):])
			s.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if s.rejectRcpt {
				reply("550 no such user")
				continue
			}
			s.mu.Lock()
			s.rcpts = append(s.rcpts, strings.TrimSpace(line[len("RCPT TO:"):]))
			s.mu.Unlock()
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
			s.data = b.String()
			s.mu.Unlock()
			reply("250 queued")
		case cmd == "RSET", cmd == "NOOP":
			reply("250 ok")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 not implemented")
		}
	}
}

func (s *fakeSMTP) snapshot() (from string, rcpts []string, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.from, append([]string(nil), s.rcpts...), s.data
}

func newMailer(addr string) *auth.SMTPMailer {
	return auth.NewSMTPMailer(auth.SMTPConfig{Addr: addr, From: "no-reply@sela.test"})
}

func TestSMTPMailer_deliversAWellFormedMessage(t *testing.T) {
	// Arrange
	srv := startFakeSMTP(t, false)
	mailer := newMailer(srv.addr)

	// Act
	err := mailer.Send(context.Background(), "host@example.test", "Kode masuk Sela", "Kode Anda: 123456\nBerlaku 5 menit.")

	// Assert
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	from, rcpts, data := srv.snapshot()
	if !strings.Contains(from, "no-reply@sela.test") {
		t.Errorf("MAIL FROM = %q", from)
	}
	if len(rcpts) != 1 || !strings.Contains(rcpts[0], "host@example.test") {
		t.Errorf("RCPT TO = %v", rcpts)
	}
	for _, want := range []string{
		"From: no-reply@sela.test\r\n",
		"To: host@example.test\r\n",
		"Subject: Kode masuk Sela\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"Kode Anda: 123456\r\n",
		"Berlaku 5 menit.",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("message is missing %q:\n%s", want, data)
		}
	}
}

func TestSMTPMailer_encodesNonASCIISubjects(t *testing.T) {
	// Arrange
	srv := startFakeSMTP(t, false)
	mailer := newMailer(srv.addr)

	// Act
	err := mailer.Send(context.Background(), "host@example.test", "Kode masuk Sela — 123456", "body")

	// Assert
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _, data := srv.snapshot()
	subject := ""
	for _, line := range strings.Split(data, "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			subject = line
		}
	}
	for i := 0; i < len(subject); i++ {
		if subject[i] > 127 {
			t.Fatalf("subject header contains raw non-ASCII bytes: %q", subject)
		}
	}
	if !strings.Contains(strings.ToLower(subject), "=?utf-8?") {
		t.Errorf("subject %q should be RFC 2047 encoded", subject)
	}
}

func TestSMTPMailer_refusesRecipientsThatCouldInjectCommandsOrHeaders(t *testing.T) {
	tests := []struct{ name, to string }{
		{"newline", "a@example.test\r\nRCPT TO:<x@example.test>"},
		{"angle brackets", "<a@example.test>"},
		{"display name", "Host <a@example.test>"},
		{"empty", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			srv := startFakeSMTP(t, false)
			mailer := newMailer(srv.addr)

			// Act
			err := mailer.Send(context.Background(), tc.to, "subject", "body")

			// Assert
			if err == nil {
				t.Fatal("Send accepted an unsafe recipient")
			}
			if _, rcpts, _ := srv.snapshot(); len(rcpts) != 0 {
				t.Errorf("server received recipients %v", rcpts)
			}
		})
	}
}

func TestSMTPMailer_refusesSubjectsWithLineBreaks(t *testing.T) {
	// Arrange
	srv := startFakeSMTP(t, false)
	mailer := newMailer(srv.addr)

	// Act
	err := mailer.Send(context.Background(), "host@example.test", "hi\r\nBcc: victim@example.test", "body")

	// Assert
	if err == nil {
		t.Fatal("Send accepted a subject containing a line break")
	}
}

func TestSMTPMailer_reportsARejectedRecipient(t *testing.T) {
	// Arrange
	srv := startFakeSMTP(t, true)
	mailer := newMailer(srv.addr)

	// Act
	err := mailer.Send(context.Background(), "host@example.test", "subject", "body")

	// Assert
	if err == nil {
		t.Fatal("expected an error when the server rejects the recipient")
	}
}

func TestSMTPMailer_reportsAnUnreachableServer(t *testing.T) {
	// Arrange
	mailer := newMailer("127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	err := mailer.Send(ctx, "host@example.test", "subject", "body")

	// Assert
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}

func TestSMTPMailer_honoursACancelledContext(t *testing.T) {
	// Arrange
	srv := startFakeSMTP(t, false)
	mailer := newMailer(srv.addr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	err := mailer.Send(ctx, "host@example.test", "subject", "body")

	// Assert
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}
