package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// testCert is a self-signed certificate for 127.0.0.1, so the sender's TLS
// can be tested without a real relay.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

type received struct {
	tls      bool
	auth     string
	from, to string
	data     string
}

// fakeRelay speaks just enough SMTP for one message. With startTLS false it
// does not offer STARTTLS, like a relay the sender must refuse.
func fakeRelay(t *testing.T, cert tls.Certificate, startTLS bool) (port int, got chan received) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got = make(chan received, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var r received
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		say := func(s string) { rw.WriteString(s + "\r\n"); rw.Flush() }
		say("220 fake relay")
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				got <- r
				return
			}
			cmd := strings.TrimSpace(line)
			switch upper := strings.ToUpper(cmd); {
			case strings.HasPrefix(upper, "EHLO"):
				ext := "250-fake\r\n250-AUTH PLAIN\r\n"
				if startTLS && !r.tls {
					ext += "250-STARTTLS\r\n"
				}
				say(ext + "250 8BITMIME")
			case upper == "STARTTLS":
				say("220 go ahead")
				tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
				if tc.Handshake() != nil {
					got <- r
					return
				}
				conn, r.tls = tc, true
				rw = bufio.NewReadWriter(bufio.NewReader(tc), bufio.NewWriter(tc))
			case strings.HasPrefix(upper, "AUTH PLAIN"):
				b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
				r.auth = string(b)
				say("235 ok")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				r.from = cmd[len("MAIL FROM:"):]
				say("250 ok")
			case strings.HasPrefix(upper, "RCPT TO:"):
				r.to = cmd[len("RCPT TO:"):]
				say("250 ok")
			case upper == "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := rw.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				r.data = b.String()
				say("250 queued")
			case upper == "QUIT":
				say("221 bye")
				got <- r
				return
			default:
				say("500 unknown")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestSMTPSendsOverSTARTTLSWithAuth(t *testing.T) {
	cert, pool := testCert(t)
	port, got := fakeRelay(t, cert, true)
	s := &SMTP{Host: "127.0.0.1", Port: port, Username: "relay-user", Password: "relay-pass", From: "JobPilot <jobs@example.test>", tlsConfig: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	err := s.Send(context.Background(), Message{To: "ana.testova@example.test", Subject: "Verify your email", Body: "Open this link:\n\nhttps://jobs.example.test/verify-email#token=abc\n.\nend"})
	if err != nil {
		t.Fatal(err)
	}
	r := <-got
	if !r.tls {
		t.Fatal("the message must be sent after STARTTLS")
	}
	if r.auth != "\x00relay-user\x00relay-pass" {
		t.Fatalf("unexpected AUTH PLAIN %q", r.auth)
	}
	if !strings.HasPrefix(r.from, "<jobs@example.test>") || r.to != "<ana.testova@example.test>" {
		t.Fatalf("envelope from %q to %q", r.from, r.to)
	}
	for _, want := range []string{"Subject: Verify your email\r\n", "To: <ana.testova@example.test>\r\n", "https://jobs.example.test/verify-email#token=abc\r\n", "\r\n..\r\nend"} {
		if !strings.Contains(r.data, want) {
			t.Errorf("message lacks %q:\n%s", want, r.data)
		}
	}
}

// A relay that offers no STARTTLS gets nothing: no credentials, no message.
func TestSMTPRefusesARelayWithoutTLS(t *testing.T) {
	cert, _ := testCert(t)
	port, got := fakeRelay(t, cert, false)
	s := &SMTP{Host: "127.0.0.1", Port: port, Username: "relay-user", Password: "relay-pass", From: "jobs@example.test", Timeout: 5 * time.Second}
	if err := s.Send(context.Background(), Message{To: "ana@example.test", Subject: "x", Body: "y"}); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected a refusal naming STARTTLS, got %v", err)
	}
	select {
	case r := <-got:
		if r.auth != "" || r.data != "" {
			t.Fatalf("nothing may be sent in the clear: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never saw the connection close")
	}
}

// A subject or address carrying a line break cannot add a header.
func TestComposeKeepsHeadersOnTheirLines(t *testing.T) {
	from, _ := mail.ParseAddress("jobs@example.test")
	to, _ := mail.ParseAddress("ana@example.test")
	out := string(compose(from, to, Message{Subject: "Hello\r\nBcc: victim@example.test", Body: "x"}, time.Unix(0, 0)))
	headers := out[:strings.Index(out, "\r\n\r\n")]
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("an injected header got through:\n%s", headers)
		}
	}
	if _, err := mail.ParseAddress("ana@example.test\r\nBcc: victim@example.test"); err == nil {
		t.Fatal("recipient parsing should refuse a line break")
	}
}

func TestValidateRefusesIncompleteSettings(t *testing.T) {
	for _, s := range []SMTP{{Port: 587, From: "a@example.test"}, {Host: "h", Port: 0, From: "a@example.test"}, {Host: "h", Port: 587, From: "nope"}} {
		if s.Validate() == nil {
			t.Errorf("%+v should be refused", s)
		}
	}
	if (&SMTP{Host: "h", Port: 587, From: "a@example.test"}).Validate() != nil {
		t.Error("a complete configuration should pass")
	}
}
