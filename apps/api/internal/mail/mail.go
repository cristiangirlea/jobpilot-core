// Package mail sends the few emails the API needs: verifying an address and
// resetting a password. Sending is off unless SMTP is configured; the default
// Disabled sender only logs that a message was not sent, so tests and local
// runs need nothing.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Body    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
	// Enabled reports whether messages actually leave the server. Email
	// verification is only required when they do.
	Enabled() bool
}

// Disabled is the sender when no SMTP server is configured. It logs the
// recipient and subject, never the body: the body holds a single-use link.
type Disabled struct{}

func (Disabled) Send(_ context.Context, m Message) error {
	slog.Info("email not sent: SMTP is not configured", "to", m.To, "subject", m.Subject)
	return nil
}

func (Disabled) Enabled() bool { return false }

// SMTP sends through a relay such as Brevo's (smtp-relay.brevo.com:587).
// The connection must be TLS: STARTTLS on a plain port, or implicit TLS on
// 465. A server that offers neither is refused, so the password and the
// links never cross the network in the clear.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// Timeout bounds the whole exchange; zero means 20 seconds.
	Timeout time.Duration
	// tlsConfig is replaced by tests that talk to a server with a test
	// certificate; nil verifies against the system roots.
	tlsConfig *tls.Config
}

func (s *SMTP) Enabled() bool { return true }

// Validate checks the configuration at startup, so a typo fails there and
// not on the first sign-up.
func (s *SMTP) Validate() error {
	if s.Host == "" || s.Port <= 0 || s.Port > 65535 {
		return errors.New("SMTP_HOST and a valid SMTP_PORT are required")
	}
	if _, err := mail.ParseAddress(s.From); err != nil {
		return fmt.Errorf("MAIL_FROM is not an email address: %w", err)
	}
	return nil
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return err
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	tlsConfig := s.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	}
	dialer := &net.Dialer{}
	var conn net.Conn
	if s.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server does not offer STARTTLS; refusing to send in the clear")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(compose(from, to, m, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// compose renders a plain-text message. Header values come from parsed
// addresses and a Q-encoded subject, so nothing a caller supplies can add a
// header line.
func compose(from, to *mail.Address, m Message, now time.Time) []byte {
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n")
	// A line holding a single dot would end the message early; the
	// smtp package's writer escapes it, so the body goes in as written.
	b.WriteString(body)
	if !strings.HasSuffix(body, "\r\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}
