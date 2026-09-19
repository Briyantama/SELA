package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

const (
	smtpDialTimeout = 10 * time.Second
	smtpTimeout     = 30 * time.Second
)

// Mailer sends a plain-text email.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTPConfig configures the SMTP relay. Username and Password are optional (Mailpit needs neither).
type SMTPConfig struct {
	Addr     string
	From     string
	Username string
	Password string
}

// SMTPMailer delivers mail through an SMTP relay, using STARTTLS whenever the server offers it.
type SMTPMailer struct {
	cfg SMTPConfig
}

// NewSMTPMailer returns a Mailer that talks to the relay in cfg.
func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cfg}
}

// Send delivers one message. The recipient must be a bare address and the subject a single line,
// so callers cannot inject SMTP commands or extra headers.
func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if err := requireBareAddress(to); err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	if err := requireBareAddress(m.cfg.From); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if strings.ContainsAny(subject, "\r\n") {
		return errors.New("subject must be a single line")
	}

	client, closeConn, err := m.connect(ctx)
	if err != nil {
		return err
	}
	defer closeConn()

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write([]byte(m.message(to, subject, body))); err != nil {
		return fmt.Errorf("smtp write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp finish message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp QUIT: %w", err)
	}
	return nil
}

func (m *SMTPMailer) connect(ctx context.Context) (*smtp.Client, func(), error) {
	dialer := net.Dialer{Timeout: smtpDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", m.cfg.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to smtp server: %w", err)
	}

	deadline := time.Now().Add(smtpTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stopWatch := context.AfterFunc(ctx, func() { _ = conn.Close() })

	host, _, err := net.SplitHostPort(m.cfg.Addr)
	if err != nil {
		stopWatch()
		_ = conn.Close()
		return nil, nil, fmt.Errorf("parse smtp address: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		stopWatch()
		_ = conn.Close()
		return nil, nil, fmt.Errorf("smtp handshake: %w", err)
	}
	closeConn := func() {
		stopWatch()
		_ = client.Close()
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			closeConn()
			return nil, nil, fmt.Errorf("smtp STARTTLS: %w", err)
		}
	}
	if m.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, host)); err != nil {
			closeConn()
			return nil, nil, fmt.Errorf("smtp auth: %w", err)
		}
	}
	return client, closeConn, nil
}

func (m *SMTPMailer) message(to, subject, body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	headers := []string{
		"From: " + m.cfg.From,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

// requireBareAddress accepts only a plain addr-spec: no display name, brackets or control characters.
func requireBareAddress(raw string) error {
	if raw == "" || strings.ContainsAny(raw, "\r\n<>") {
		return errors.New("must be a bare email address")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Name != "" || addr.Address != raw {
		return errors.New("must be a bare email address")
	}
	return nil
}
