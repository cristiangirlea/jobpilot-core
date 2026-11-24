package config

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	ATSAshbyURL       string
	ATSRecruiteeURL   string
	TokenTTL          time.Duration
	LLM               LLMConfig
	ExtractionModel   string
	ExtractionScoring string
	// Registration is who may create an account: invite (the default), open
	// or closed. AdminEmails are the accounts that may create invites.
	Registration string
	AdminEmails  []string
	// UserAgentTokens is what agent tokens accounts other than the admins may
	// hold and use (#128): off (the default), read or write. It matters only
	// with AdminEmails set; without admins every account has full access.
	UserAgentTokens string
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
	if utf8.RuneCountInString(c.ExtractionModel) > 200 || strings.ContainsFunc(c.ExtractionModel, unicode.IsControl) {
		return fmt.Errorf("EXTRACTION_MODEL must be at most 200 characters and contain no control characters")
	}
	switch c.ExtractionScoring {
	case "", "off", "shadow":
	default:
		return fmt.Errorf("EXTRACTION_SCORING must be off or shadow")
	}
	switch c.UserAgentTokens {
	case "off", "read", "write":
	default:
		return fmt.Errorf("USER_AGENT_TOKENS must be off, read or write, not %q", c.UserAgentTokens)
	}
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
	BackupProvider string
	BackupModel    string
	BackupTimeout  time.Duration
	Provider       string
	Model          string
	CLICommand     string
	CLIArgs        []string
	CLIDir         string
	OllamaURL      string
	Timeout        time.Duration
	CallsPerHour   int
}

// ModelBudget bounds all model calls made during one request.
func (c LLMConfig) ModelBudget() time.Duration {
	if strings.EqualFold(strings.TrimSpace(c.BackupProvider), "ollama") {
		return c.Timeout + c.BackupTimeout
	}
	return c.Timeout
}

// RequestBudget reserves time after the model deadline to finish the response.
func (c LLMConfig) RequestBudget() time.Duration {
	return max(2*time.Minute, c.ModelBudget()+30*time.Second)
}

func Load() (Config, error) {
	databaseURL, err := secret("DATABASE_URL", "postgres://jobpilot:jobpilot@localhost:5432/jobpilot?sslmode=disable")
	if err != nil {
		return Config{}, err
	}
	jwtSecret, err := secret("JWT_SECRET", "development-only-change-me")
	if err != nil {
		return Config{}, err
	}
	googleSecret, err := secret("GOOGLE_CLIENT_SECRET", "")
	if err != nil {
		return Config{}, err
	}
	llmConfig, err := loadLLM()
	if err != nil {
		return Config{}, err
	}
	smtpPassword, err := secret("SMTP_PASSWORD", "")
	if err != nil {
		return Config{}, err
	}
	hours, _ := strconv.Atoi(env("TOKEN_TTL_HOURS", "168"))
	delay, err := strconv.Atoi(env("OUTBOX_SEND_DELAY_SECONDS", "120"))
	if err != nil || delay < 0 {
		delay = 120
	}
	extractionScoring := strings.TrimSpace(os.Getenv("EXTRACTION_SCORING"))
	if extractionScoring == "off" {
		extractionScoring = ""
	}
	return Config{
		Addr:              env("API_ADDR", ":8080"),
		DatabaseURL:       databaseURL,
		JWTSecret:         jwtSecret,
		FrontendURL:       env("FRONTEND_URL", "http://localhost:3000"),
		ATSGreenhouseURL:  env("ATS_GREENHOUSE_URL", "https://boards-api.greenhouse.io"),
		ATSAshbyURL:       env("ATS_ASHBY_URL", "https://jobs.ashbyhq.com"),
		ATSRecruiteeURL:   env("ATS_RECRUITEE_URL", ""),
		TokenTTL:          time.Duration(hours) * time.Hour,
		LLM:               llmConfig,
		ExtractionModel:   strings.TrimSpace(os.Getenv("EXTRACTION_MODEL")),
		ExtractionScoring: extractionScoring,
		Registration:      strings.ToLower(strings.TrimSpace(env("REGISTRATION", RegistrationInvite))),
		AdminEmails:       emails(os.Getenv("ADMIN_EMAILS")),
		UserAgentTokens:   strings.ToLower(strings.TrimSpace(env("USER_AGENT_TOKENS", "off"))),
		ClientIPHeader:    strings.TrimSpace(os.Getenv("CLIENT_IP_HEADER")),
		Mail:              loadMail(smtpPassword),
		Google: GoogleConfig{
			ClientID:     env("GOOGLE_CLIENT_ID", ""),
			ClientSecret: googleSecret,
			RedirectURL:  env("GOOGLE_REDIRECT_URL", ""),
			AllowSignup:  env("GOOGLE_ALLOW_SIGNUP", "") == "true",
		},
		OutboxSending:   env("OUTBOX_SENDER_ENABLED", "false") == "true",
		OutboxSendDelay: time.Duration(delay) * time.Second,
	}, nil
}

func loadLLM() (LLMConfig, error) {
	backupSeconds, err := strconv.Atoi(os.Getenv("LLM_BACKUP_TIMEOUT_SECONDS"))
	if err != nil || backupSeconds <= 0 {
		backupSeconds = 180
	}
	seconds, err := strconv.Atoi(os.Getenv("LLM_TIMEOUT_SECONDS"))
	if err != nil || seconds <= 0 {
		seconds = 90
	}
	calls, _ := strconv.Atoi(env("LLM_CALLS_PER_HOUR", "120"))
	args, err := splitArgs(env("LLM_CLI_ARGS", ""))
	if err != nil {
		return LLMConfig{}, fmt.Errorf("LLM_CLI_ARGS: %w", err)
	}
	return LLMConfig{
		BackupProvider: env("LLM_BACKUP_PROVIDER", "none"),
		BackupModel:    env("LLM_BACKUP_MODEL", ""),
		BackupTimeout:  time.Duration(backupSeconds) * time.Second,
		Provider:       env("LLM_PROVIDER", "none"),
		Model:          env("LLM_MODEL", ""),
		CLICommand:     env("LLM_CLI_COMMAND", ""),
		CLIArgs:        args,
		CLIDir:         env("LLM_CLI_DIR", ""),
		OllamaURL:      env("LLM_OLLAMA_URL", "http://localhost:11434"),
		Timeout:        time.Duration(seconds) * time.Second,
		CallsPerHour:   calls,
	}, nil
}

// splitArgs splits LLM_CLI_ARGS on spaces, keeping text in double or single
// quotes together, so an empty argument can be written as "". An unclosed
// quote is an error, since it would swallow the options after it.
func splitArgs(v string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	inArg := false
	for _, r := range v {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inArg = r, true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, current.String())
				current.Reset()
				inArg = false
			}
		default:
			current.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote", quote)
	}
	if inArg {
		args = append(args, current.String())
	}
	return args, nil
}

func loadMail(password string) MailConfig {
	port, err := strconv.Atoi(env("SMTP_PORT", "587"))
	if err != nil {
		port = -1 // refused by the sender's validation at startup
	}
	return MailConfig{
		SMTPHost:     strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:     port,
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: password,
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

// secret reads name, or the file named by name_FILE, as Docker and Kubernetes mount secrets.
func secret(name, fallback string) (string, error) {
	value, path := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && path != "" {
		return "", fmt.Errorf("set %s or %s_FILE, not both (%s_FILE path %q)", name, name, name, path)
	}
	if path == "" {
		return env(name, fallback), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE path %q: %w", name, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s_FILE path %q: must be a regular file", name, path)
	}
	if info.Size() > 64*1024 {
		return "", fmt.Errorf("%s_FILE path %q: exceeds 64 KiB", name, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE path %q: %w", name, path, err)
	}
	defer file.Close()
	// Bounded so a file that grows after the Stat cannot be read without limit.
	raw, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil {
		return "", fmt.Errorf("%s_FILE path %q: %w", name, path, err)
	}
	if len(raw) > 64*1024 {
		return "", fmt.Errorf("%s_FILE path %q: exceeds 64 KiB", name, path)
	}
	value = string(raw)
	if strings.HasSuffix(value, "\r\n") {
		value = strings.TrimSuffix(value, "\r\n")
	} else {
		value = strings.TrimSuffix(value, "\n")
	}
	if value == "" {
		return "", fmt.Errorf("%s_FILE path %q: empty secret", name, path)
	}
	return value, nil
}
