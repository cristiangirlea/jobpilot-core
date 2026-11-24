package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadLLMBackup(t *testing.T) {
	for _, name := range []string{"LLM_BACKUP_PROVIDER", "LLM_BACKUP_MODEL", "LLM_BACKUP_TIMEOUT_SECONDS"} {
		t.Setenv(name, "")
	}
	cfg := mustLoadLLM(t)
	if cfg.BackupProvider != "none" || cfg.BackupModel != "" || cfg.BackupTimeout != 180*time.Second {
		t.Fatalf("unexpected backup defaults: %q %q %v", cfg.BackupProvider, cfg.BackupModel, cfg.BackupTimeout)
	}
	t.Setenv("LLM_BACKUP_PROVIDER", "ollama")
	t.Setenv("LLM_BACKUP_MODEL", "local-model")
	t.Setenv("LLM_BACKUP_TIMEOUT_SECONDS", "42")
	cfg = mustLoadLLM(t)
	if cfg.BackupProvider != "ollama" || cfg.BackupModel != "local-model" || cfg.BackupTimeout != 42*time.Second {
		t.Fatalf("unexpected backup settings: %q %q %v", cfg.BackupProvider, cfg.BackupModel, cfg.BackupTimeout)
	}
}

func TestInvalidBackupTimeoutUsesDefault(t *testing.T) {
	for _, value := range []string{"", "oops", "0", "-1"} {
		t.Setenv("LLM_BACKUP_TIMEOUT_SECONDS", value)
		if got := mustLoadLLM(t).BackupTimeout; got != 180*time.Second {
			t.Errorf("timeout %q: got %v, want 180s", value, got)
		}
	}
}

func TestLLMBudgets(t *testing.T) {
	t.Setenv("LLM_BACKUP_TIMEOUT_SECONDS", "180")
	for _, tc := range []struct {
		name, timeout, backup   string
		primary, model, request time.Duration
	}{
		{"minimum", "1", "", time.Second, time.Second, 2 * time.Minute},
		{"long primary only", "120", "none", 2 * time.Minute, 2 * time.Minute, 150 * time.Second},
		{"empty timeout", "", "off", 90 * time.Second, 90 * time.Second, 2 * time.Minute},
		{"invalid timeout with backup", "oops", " Ollama ", 90 * time.Second, 270 * time.Second, 5 * time.Minute},
		{"zero timeout", "0", "", 90 * time.Second, 90 * time.Second, 2 * time.Minute},
		{"negative timeout", "-1", "", 90 * time.Second, 90 * time.Second, 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LLM_TIMEOUT_SECONDS", tc.timeout)
			t.Setenv("LLM_BACKUP_PROVIDER", tc.backup)
			cfg := mustLoadLLM(t)
			if cfg.Timeout != tc.primary {
				t.Errorf("primary timeout = %v, want %v", cfg.Timeout, tc.primary)
			}
			if got := cfg.ModelBudget(); got != tc.model {
				t.Errorf("model budget = %v, want %v", got, tc.model)
			}
			if got := cfg.RequestBudget(); got != tc.request {
				t.Errorf("request budget = %v, want %v", got, tc.request)
			}
		})
	}
}

func TestSecret(t *testing.T) {
	const name = "TEST_SECRET"
	const content = "secret-content-never-log"
	tests := []struct {
		name    string
		value   string
		kind    string
		content string
		want    string
		wantErr string
	}{
		{name: "plain value", value: content, want: content},
		{name: "LF", kind: "file", content: content + "\n", want: content},
		{name: "CRLF", kind: "file", content: content + "\r\n", want: content},
		{name: "no newline", kind: "file", content: content, want: content},
		{name: "two newlines", kind: "file", content: content + "\n\n", want: content + "\n"},
		{name: "two CRLFs", kind: "file", content: content + "\r\n\r\n", want: content + "\r\n"},
		{name: "preserve spaces", kind: "file", content: " " + content + " ", want: " " + content + " "},
		{name: "preserve CR", kind: "file", content: content + "\r", want: content + "\r"},
		{name: "both set", value: content, kind: "file", content: content, wantErr: "set TEST_SECRET or TEST_SECRET_FILE, not both"},
		{name: "missing file", kind: "missing", wantErr: "TEST_SECRET_FILE"},
		{name: "directory", kind: "directory", wantErr: "regular file"},
		{name: "empty file", kind: "file", wantErr: "empty secret"},
		{name: "only LF", kind: "file", content: "\n", wantErr: "empty secret"},
		{name: "only CRLF", kind: "file", content: "\r\n", wantErr: "empty secret"},
		{name: "over limit", kind: "file", content: content + strings.Repeat("x", 64*1024), wantErr: "64 KiB"},
		{name: "at limit", kind: "file", content: strings.Repeat("x", 64*1024), want: strings.Repeat("x", 64*1024)},
		{name: "neither set", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(name, tt.value)
			t.Setenv(name+"_FILE", "")
			path := ""
			if tt.kind != "" {
				dir := t.TempDir()
				path = filepath.Join(dir, "secret")
				switch tt.kind {
				case "file":
					if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
						t.Fatal(err)
					}
				case "directory":
					path = dir
				}
				t.Setenv(name+"_FILE", path)
			}
			got, err := secret(name, "default")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected a secret-file error")
				}
				message := err.Error()
				if !strings.Contains(message, tt.wantErr) || !strings.Contains(message, name+"_FILE") || !strings.Contains(message, strconv.Quote(path)) {
					t.Fatalf("error must name the variable, path and reason: %v", err)
				}
				if strings.Contains(message, content) {
					t.Fatal("error exposed secret content")
				}
				if got != "" {
					t.Fatal("failed secret read returned a value")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatal("secret value differs from the expected value")
			}
		})
	}
}

// The form reader's origins default to the public services, so an unset
// variable never changes where a real application's form is read.
func TestATSOriginsDefaultToThePublicServices(t *testing.T) {
	for _, key := range []string{"ATS_GREENHOUSE_URL", "ATS_ASHBY_URL", "ATS_RECRUITEE_URL"} {
		t.Setenv(key, "")
	}
	cfg := loadConfig(t)
	if cfg.ATSGreenhouseURL != "https://boards-api.greenhouse.io" || cfg.ATSAshbyURL != "https://jobs.ashbyhq.com" || cfg.ATSRecruiteeURL != "" {
		t.Fatalf("unexpected defaults: %q %q %q", cfg.ATSGreenhouseURL, cfg.ATSAshbyURL, cfg.ATSRecruiteeURL)
	}
}

func TestATSOriginsAreReadFromTheEnvironment(t *testing.T) {
	t.Setenv("ATS_GREENHOUSE_URL", "http://localhost:18999")
	t.Setenv("ATS_ASHBY_URL", "http://localhost:18998")
	t.Setenv("ATS_RECRUITEE_URL", "http://localhost:18997")
	cfg := loadConfig(t)
	if cfg.ATSGreenhouseURL != "http://localhost:18999" || cfg.ATSAshbyURL != "http://localhost:18998" || cfg.ATSRecruiteeURL != "http://localhost:18997" {
		t.Fatalf("the variables were not read: %q %q %q", cfg.ATSGreenhouseURL, cfg.ATSAshbyURL, cfg.ATSRecruiteeURL)
	}
}

func TestExtractionModel(t *testing.T) {
	t.Setenv("REGISTRATION", "")
	t.Setenv("USER_AGENT_TOKENS", "")
	for _, tc := range []struct {
		name, value, want string
		valid             bool
	}{
		{"default", "", "", true},
		{"trimmed", " example-model ", "example-model", true},
		{"blank", "   ", "", true},
		{"at limit", strings.Repeat("ä", 200), strings.Repeat("ä", 200), true},
		{"over limit", strings.Repeat("a", 201), strings.Repeat("a", 201), false},
		{"control", "example\tmodel", "example\tmodel", false},
		{"unicode control", "example\u0085model", "example\u0085model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EXTRACTION_MODEL", tc.value)
			cfg := loadConfig(t)
			if cfg.ExtractionModel != tc.want {
				t.Fatalf("ExtractionModel = %q, want %q", cfg.ExtractionModel, tc.want)
			}
			err := cfg.Validate()
			if (err == nil) != tc.valid || (err != nil && !strings.Contains(err.Error(), "EXTRACTION_MODEL")) {
				t.Fatalf("Validate() = %v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestExtractionScoring(t *testing.T) {
	t.Setenv("REGISTRATION", "")
	t.Setenv("USER_AGENT_TOKENS", "")
	t.Setenv("EXTRACTION_MODEL", "")
	for _, tc := range []struct {
		name, value, want string
		valid             bool
	}{
		{"empty", "", "", true},
		{"blank", " \t", "", true},
		{"off", "off", "", true},
		{"trimmed off", " off ", "", true},
		{"shadow", "shadow", "shadow", true},
		{"trimmed shadow", " shadow\t", "shadow", true},
		{"on", "on", "on", false},
		{"unknown", "SHADOW", "SHADOW", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("EXTRACTION_SCORING", tc.value)
			cfg := loadConfig(t)
			if cfg.ExtractionScoring != tc.want {
				t.Fatalf("ExtractionScoring = %q, want %q", cfg.ExtractionScoring, tc.want)
			}
			err := cfg.Validate()
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != "EXTRACTION_SCORING must be off or shadow" {
				t.Fatalf("Validate() = %v, want the extraction scoring error", err)
			}
		})
	}
}

// Sign-up is invite-only unless the environment says otherwise, and a mode
// the API does not know stops it at startup rather than opening sign-up.
func TestRegistrationDefaultsToInviteAndRefusesUnknownModes(t *testing.T) {
	t.Setenv("REGISTRATION", "")
	t.Setenv("ADMIN_EMAILS", " Owner@Example.test, ,second@example.test ")
	cfg := loadConfig(t)
	if cfg.Registration != RegistrationInvite || cfg.Validate() != nil {
		t.Fatalf("expected invite by default, got %q (%v)", cfg.Registration, cfg.Validate())
	}
	if len(cfg.AdminEmails) != 2 || cfg.AdminEmails[0] != "owner@example.test" || cfg.AdminEmails[1] != "second@example.test" {
		t.Fatalf("admin emails should be trimmed and lowercased: %q", cfg.AdminEmails)
	}
	for mode, valid := range map[string]bool{"Open": true, "closed": true, "invite": true, "public": false} {
		t.Setenv("REGISTRATION", mode)
		if err := loadConfig(t).Validate(); (err == nil) != valid {
			t.Errorf("REGISTRATION=%s: valid=%v, err=%v", mode, valid, err)
		}
	}
}

// Sending email is off unless SMTP_HOST is set; the port defaults to 587.
func TestMailIsOffUntilSMTPIsConfigured(t *testing.T) {
	for _, key := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "MAIL_FROM"} {
		t.Setenv(key, "")
	}
	if m := loadConfig(t).Mail; m.SMTPHost != "" || m.SMTPPort != 587 {
		t.Fatalf("expected no host and port 587, got %+v", m)
	}
	t.Setenv("SMTP_HOST", "smtp-relay.example.test")
	t.Setenv("SMTP_PORT", "not a port")
	t.Setenv("MAIL_FROM", "JobPilot <jobs@example.test>")
	if m := loadConfig(t).Mail; m.SMTPHost != "smtp-relay.example.test" || m.SMTPPort != -1 || m.From != "JobPilot <jobs@example.test>" {
		t.Fatalf("unexpected mail config %+v", m)
	}
}

// Google sign-in is off unless a client id is set, and sign-up through it is
// off unless GOOGLE_ALLOW_SIGNUP is exactly "true".
func TestGoogleSignInIsOffByDefault(t *testing.T) {
	for _, key := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL", "GOOGLE_ALLOW_SIGNUP"} {
		t.Setenv(key, "")
	}
	if g := loadConfig(t).Google; g.Enabled() || g.AllowSignup {
		t.Fatalf("unexpected defaults: %+v", g)
	}
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	t.Setenv("GOOGLE_ALLOW_SIGNUP", "yes")
	if g := loadConfig(t).Google; !g.Enabled() || g.AllowSignup {
		t.Fatalf("client id or sign-up read wrongly: %+v", g)
	}
	t.Setenv("GOOGLE_ALLOW_SIGNUP", "true")
	if !loadConfig(t).Google.AllowSignup {
		t.Fatal("GOOGLE_ALLOW_SIGNUP=true was not read")
	}
}

// Members get no agent tokens unless USER_AGENT_TOKENS says read or write,
// and any other value stops the API at startup instead of guessing.
func TestUserAgentTokensDefaultsToOff(t *testing.T) {
	t.Setenv("USER_AGENT_TOKENS", "")
	t.Setenv("REGISTRATION", "")
	cfg := loadConfig(t)
	if cfg.UserAgentTokens != "off" || cfg.Validate() != nil {
		t.Fatalf("default = %q (%v), want off", cfg.UserAgentTokens, cfg.Validate())
	}
	for _, v := range []string{"read", "Write"} {
		t.Setenv("USER_AGENT_TOKENS", v)
		if cfg := loadConfig(t); cfg.Validate() != nil || cfg.UserAgentTokens != strings.ToLower(v) {
			t.Fatalf("%q was refused or misread: %q", v, cfg.UserAgentTokens)
		}
	}
	t.Setenv("USER_AGENT_TOKENS", "yes")
	if loadConfig(t).Validate() == nil {
		t.Fatal("USER_AGENT_TOKENS=yes was accepted")
	}
}

func loadConfig(t *testing.T) Config {
	t.Helper()
	clearSecrets(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadReturnsSecretFileError(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "JWT_SECRET", "GOOGLE_CLIENT_SECRET", "SMTP_PASSWORD"} {
		t.Run(name, func(t *testing.T) {
			clearSecrets(t)
			path := filepath.Join(t.TempDir(), "missing")
			t.Setenv(name+"_FILE", path)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name+"_FILE") || !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Fatalf("expected %s_FILE error naming %s, got %v", name, path, err)
			}
		})
	}
}

func clearSecrets(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DATABASE_URL", "JWT_SECRET", "GOOGLE_CLIENT_SECRET", "SMTP_PASSWORD"} {
		t.Setenv(name, "")
		t.Setenv(name+"_FILE", "")
	}
}

func TestLoadSecrets(t *testing.T) {
	for _, source := range []string{"default", "value", "file"} {
		t.Run(source, func(t *testing.T) {
			clearSecrets(t)
			want := []string{
				"postgres://jobpilot:jobpilot@localhost:5432/jobpilot?sslmode=disable",
				"development-only-change-me", "", "",
			}
			for i, name := range []string{"DATABASE_URL", "JWT_SECRET", "GOOGLE_CLIENT_SECRET", "SMTP_PASSWORD"} {
				switch source {
				case "value":
					want[i] = "value-" + name
					t.Setenv(name, want[i])
				case "file":
					want[i] = "file-" + name
					path := filepath.Join(t.TempDir(), "secret")
					if err := os.WriteFile(path, []byte(want[i]+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
					t.Setenv(name+"_FILE", path)
				}
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			got := []string{cfg.DatabaseURL, cfg.JWTSecret, cfg.Google.ClientSecret, cfg.Mail.SMTPPassword}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("secret setting %d did not use its %s", i, source)
				}
			}
		})
	}
}

func TestCLIArgsKeepQuotedTextTogether(t *testing.T) {
	cases := map[string][]string{
		"":                                  nil,
		"-p --output-format text":           {"-p", "--output-format", "text"},
		`-p --tools "" --strict-mcp-config`: {"-p", "--tools", "", "--strict-mcp-config"},
		`exec '{{prompt}}'  --model "a b"`:  {"exec", "{{prompt}}", "--model", "a b"},
		`--name=x"y z"`:                     {"--name=xy z"},
	}
	for in, want := range cases {
		got, err := splitArgs(in)
		if err != nil {
			t.Fatalf("splitArgs(%q): %v", in, err)
		}
		if len(got) != len(want) {
			t.Fatalf("splitArgs(%q) = %q, want %q", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("splitArgs(%q) = %q, want %q", in, got, want)
			}
		}
	}
}

func TestLLMCLIDirIsReadFromTheEnvironment(t *testing.T) {
	t.Setenv("LLM_CLI_ARGS", `-p --tools ""`)
	t.Setenv("LLM_CLI_DIR", "/var/empty/jobpilot-cli")
	got, err := loadLLM()
	if err != nil {
		t.Fatal(err)
	}
	if got.CLIDir != "/var/empty/jobpilot-cli" || len(got.CLIArgs) != 3 || got.CLIArgs[2] != "" {
		t.Fatalf("unexpected CLI settings: dir %q args %q", got.CLIDir, got.CLIArgs)
	}
}

func TestCLIArgsRefuseAnUnclosedQuote(t *testing.T) {
	for _, in := range []string{`--model 'some model --strict-mcp-config`, `-p --tools "`} {
		if _, err := splitArgs(in); err == nil {
			t.Fatalf("splitArgs(%q) should refuse an unclosed quote", in)
		}
	}
	t.Setenv("LLM_CLI_ARGS", `-p --tools "`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LLM_CLI_ARGS") {
		t.Fatalf("Load should name LLM_CLI_ARGS, got %v", err)
	}
}

func mustLoadLLM(t *testing.T) LLMConfig {
	t.Helper()
	cfg, err := loadLLM()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
