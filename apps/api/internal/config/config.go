package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string
	JWTSecret   string
	FrontendURL string
	// ATSGreenhouseURL is the Greenhouse board API origin, for both the form
	// reader and discovery's board listings. Tests point it at a fake so a
	// discovery run and a composed application can be exercised end to end.
	ATSGreenhouseURL string
	// ATSAshbyURL is the origin of Ashby's public job pages, and
	// ATSRecruiteeURL stands in for every Recruitee careers site; empty keeps
	// each site's own host. They exist so the end-to-end test can serve those
	// forms too.
	ATSAshbyURL     string
	ATSRecruiteeURL string
	TokenTTL        time.Duration
	LLM             LLMConfig
	// Registration is who may create an account: invite (the default), open
	// or closed. AdminEmails are the accounts that may create invites.
	Registration string
	AdminEmails  []string
	// ClientIPHeader names a request header that holds the caller's address,
	// set by a proxy in front of the API that overwrites it (CF-Connecting-IP
	// behind a Cloudflare Tunnel). Empty uses the connection's address. Never
	// name a header the client can set unchecked, such as X-Forwarded-For.
	ClientIPHeader string
	// Mail is the SMTP relay for verification and reset emails. Sending is
	// off while SMTPHost is empty.
	Mail   MailConfig
	Google GoogleConfig
	// OutboxSending turns on sending approved outbox entries through the
	// employer's form (OUTBOX_SENDER_ENABLED=true). It is off unless asked
	// for, so an instance whose approvals predate sending never sends them
	// on upgrade. OutboxSendDelay is how long an approval waits before it
	// goes, so the user can still withdraw it.
	OutboxSending   bool
	OutboxSendDelay time.Duration
}

// MailConfig is read from SMTP_HOST, SMTP_PORT (587 by default),
// SMTP_USERNAME, SMTP_PASSWORD (a secret) and MAIL_FROM.
type MailConfig struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	From         string
}

// Registration modes.
const (
	RegistrationInvite = "invite"
	RegistrationOpen   = "open"
	RegistrationClosed = "closed"
)

// Validate refuses settings the API cannot run with, so a typo fails at
// startup instead of opening sign-up.
func (c Config) Validate() error {
	switch c.Registration {
	case RegistrationInvite, RegistrationOpen, RegistrationClosed:
		return nil
	}
	return fmt.Errorf("REGISTRATION must be invite, open or closed, not %q", c.Registration)
}

// GoogleConfig turns on "Sign in with Google". With no client id it is off;
// AllowSignup lets a Google account with no JobPilot account create one, and
// is off unless GOOGLE_ALLOW_SIGNUP is "true".
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AllowSignup  bool
}

func (g GoogleConfig) Enabled() bool { return g.ClientID != "" }

// LLMConfig selects the optional AI provider. The zero value ("none") keeps the
// product fully deterministic, which is the supported default.
type LLMConfig struct {
	Provider     string
	Model        string
	CLICommand   string
	CLIArgs      []string
	OllamaURL    string
	Timeout      time.Duration
	CallsPerHour int
}

func Load() Config {
	hours, _ := strconv.Atoi(env("TOKEN_TTL_HOURS", "168"))
	delay, err := strconv.Atoi(env("OUTBOX_SEND_DELAY_SECONDS", "120"))
	if err != nil || delay < 0 {
		delay = 120
	}
	return Config{
		Addr:             env("API_ADDR", ":8080"),
		DatabaseURL:      env("DATABASE_URL", "postgres://jobpilot:jobpilot@localhost:5432/jobpilot?sslmode=disable"),
		JWTSecret:        env("JWT_SECRET", "development-only-change-me"),
		FrontendURL:      env("FRONTEND_URL", "http://localhost:3000"),
		ATSGreenhouseURL: env("ATS_GREENHOUSE_URL", "https://boards-api.greenhouse.io"),
		ATSAshbyURL:      env("ATS_ASHBY_URL", "https://jobs.ashbyhq.com"),
		ATSRecruiteeURL:  env("ATS_RECRUITEE_URL", ""),
		TokenTTL:         time.Duration(hours) * time.Hour,
		LLM:              loadLLM(),
		Registration:     strings.ToLower(strings.TrimSpace(env("REGISTRATION", RegistrationInvite))),
		AdminEmails:      emails(os.Getenv("ADMIN_EMAILS")),
		ClientIPHeader:   strings.TrimSpace(os.Getenv("CLIENT_IP_HEADER")),
		Mail:             loadMail(),
		Google: GoogleConfig{
			ClientID:     env("GOOGLE_CLIENT_ID", ""),
			ClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
			RedirectURL:  env("GOOGLE_REDIRECT_URL", ""),
			AllowSignup:  env("GOOGLE_ALLOW_SIGNUP", "") == "true",
		},
		OutboxSending:   env("OUTBOX_SENDER_ENABLED", "false") == "true",
		OutboxSendDelay: time.Duration(delay) * time.Second,
	}
}

func loadLLM() LLMConfig {
	seconds, _ := strconv.Atoi(env("LLM_TIMEOUT_SECONDS", "90"))
	calls, _ := strconv.Atoi(env("LLM_CALLS_PER_HOUR", "120"))
	return LLMConfig{
		Provider:     env("LLM_PROVIDER", "none"),
		Model:        env("LLM_MODEL", ""),
		CLICommand:   env("LLM_CLI_COMMAND", ""),
		CLIArgs:      strings.Fields(env("LLM_CLI_ARGS", "")),
		OllamaURL:    env("LLM_OLLAMA_URL", "http://localhost:11434"),
		Timeout:      time.Duration(seconds) * time.Second,
		CallsPerHour: calls,
	}
}

func loadMail() MailConfig {
	port, err := strconv.Atoi(env("SMTP_PORT", "587"))
	if err != nil {
		port = -1 // refused by the sender's validation at startup
	}
	return MailConfig{
		SMTPHost:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:     port,
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		From:         strings.TrimSpace(os.Getenv("MAIL_FROM")),
	}
}

func emails(list string) []string {
	out := []string{}
	for _, e := range strings.Split(list, ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
