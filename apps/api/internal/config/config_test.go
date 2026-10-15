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

// Google sign-in is off unless a client id is set, and sign-up through it is
// off unless GOOGLE_ALLOW_SIGNUP is exactly "true".
func TestGoogleSignInIsOffByDefault(t *testing.T) {
	for _, key := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "GOOGLE_REDIRECT_URL", "GOOGLE_ALLOW_SIGNUP"} {
		t.Setenv(key, "")
	}
	if g := Load().Google; g.Enabled() || g.AllowSignup {
		t.Fatalf("unexpected defaults: %+v", g)
	}
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	t.Setenv("GOOGLE_ALLOW_SIGNUP", "yes")
	if g := Load().Google; !g.Enabled() || g.AllowSignup {
		t.Fatalf("client id or sign-up read wrongly: %+v", g)
	}
	t.Setenv("GOOGLE_ALLOW_SIGNUP", "true")
	if !Load().Google.AllowSignup {
		t.Fatal("GOOGLE_ALLOW_SIGNUP=true was not read")
	}
}
