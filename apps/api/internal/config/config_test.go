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
