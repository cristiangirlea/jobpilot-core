package config

import "testing"

// The form reader's origins default to the public services, so an unset
// variable never changes where a real application's form is read.
func TestATSOriginsDefaultToThePublicServices(t *testing.T) {
	for _, key := range []string{"ATS_GREENHOUSE_URL", "ATS_ASHBY_URL", "ATS_RECRUITEE_URL"} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.ATSGreenhouseURL != "https://boards-api.greenhouse.io" || cfg.ATSAshbyURL != "https://jobs.ashbyhq.com" || cfg.ATSRecruiteeURL != "" {
		t.Fatalf("unexpected defaults: %q %q %q", cfg.ATSGreenhouseURL, cfg.ATSAshbyURL, cfg.ATSRecruiteeURL)
	}
}

func TestATSOriginsAreReadFromTheEnvironment(t *testing.T) {
	t.Setenv("ATS_GREENHOUSE_URL", "http://localhost:18999")
	t.Setenv("ATS_ASHBY_URL", "http://localhost:18998")
	t.Setenv("ATS_RECRUITEE_URL", "http://localhost:18997")
	cfg := Load()
	if cfg.ATSGreenhouseURL != "http://localhost:18999" || cfg.ATSAshbyURL != "http://localhost:18998" || cfg.ATSRecruiteeURL != "http://localhost:18997" {
		t.Fatalf("the variables were not read: %q %q %q", cfg.ATSGreenhouseURL, cfg.ATSAshbyURL, cfg.ATSRecruiteeURL)
	}
}

// Sign-up is invite-only unless the environment says otherwise, and a mode
// the API does not know stops it at startup rather than opening sign-up.
func TestRegistrationDefaultsToInviteAndRefusesUnknownModes(t *testing.T) {
	t.Setenv("REGISTRATION", "")
	t.Setenv("ADMIN_EMAILS", " Owner@Example.test, ,second@example.test ")
	cfg := Load()
	if cfg.Registration != RegistrationInvite || cfg.Validate() != nil {
		t.Fatalf("expected invite by default, got %q (%v)", cfg.Registration, cfg.Validate())
	}
	if len(cfg.AdminEmails) != 2 || cfg.AdminEmails[0] != "owner@example.test" || cfg.AdminEmails[1] != "second@example.test" {
		t.Fatalf("admin emails should be trimmed and lowercased: %q", cfg.AdminEmails)
	}
	for mode, valid := range map[string]bool{"Open": true, "closed": true, "invite": true, "public": false} {
		t.Setenv("REGISTRATION", mode)
		if err := Load().Validate(); (err == nil) != valid {
			t.Errorf("REGISTRATION=%s: valid=%v, err=%v", mode, valid, err)
		}
	}
}

// Sending email is off unless SMTP_HOST is set; the port defaults to 587.
func TestMailIsOffUntilSMTPIsConfigured(t *testing.T) {
	for _, key := range []string{"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "MAIL_FROM"} {
		t.Setenv(key, "")
	}
	if m := Load().Mail; m.SMTPHost != "" || m.SMTPPort != 587 {
		t.Fatalf("expected no host and port 587, got %+v", m)
	}
	t.Setenv("SMTP_HOST", "smtp-relay.example.test")
	t.Setenv("SMTP_PORT", "not a port")
	t.Setenv("MAIL_FROM", "JobPilot <jobs@example.test>")
	if m := Load().Mail; m.SMTPHost != "smtp-relay.example.test" || m.SMTPPort != -1 || m.From != "JobPilot <jobs@example.test>" {
		t.Fatalf("unexpected mail config %+v", m)
	}
}
