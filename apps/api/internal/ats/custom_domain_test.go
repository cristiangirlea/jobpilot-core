package ats

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestGreenhouseJobIDOnAnEmployersDomain(t *testing.T) {
	cases := []struct {
		link, id string
		ok       bool
	}{
		{"https://careers.nebius.com/?gh_jid=4967927101&utm_source=arbeitnow.com", "4967927101", true},
		{"https://example.com/jobs/backend?gh_jid=123", "123", true},
		// Greenhouse's own hosts are Detect's to read.
		{"https://job-boards.greenhouse.io/acme/jobs/123?gh_jid=123", "", false},
		{"https://example.com/jobs?gh_jid=abc", "", false},
		{"https://example.com/jobs/123", "", false},
		{"not a url", "", false},
	}
	for _, c := range cases {
		id, ok := greenhouseJobID(c.link)
		if ok != c.ok || (ok && id != c.id) {
			t.Errorf("greenhouseJobID(%q) = %q, %v; want %q, %v", c.link, id, ok, c.id, c.ok)
		}
	}
}

// customDomainReader serves both halves of the lookup: the legacy host that
// names the board, answering as legacy says, and the board API with a form.
func customDomainReader(t *testing.T, legacy func(w http.ResponseWriter, r *http.Request)) *Reader {
	t.Helper()
	reader, server := testReader(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embed/job_app":
			legacy(w, r)
		case "/v1/boards/acme/jobs/123":
			w.Write([]byte(greenhousePayload))
		default:
			http.NotFound(w, r)
		}
	})
	reader.WithGreenhouseBase(server.URL)
	reader.greenhouseLegacyBase = server.URL
	return reader
}

func TestCustomDomainGreenhouseFormIsReadFromItsBoard(t *testing.T) {
	reader := customDomainReader(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "123" {
			http.Error(w, "wrong job", http.StatusBadRequest)
			return
		}
		// The real host redirects to job-boards.greenhouse.io; it is read, not
		// followed, so the target never has to answer.
		http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=acme&token=123", http.StatusMovedPermanently)
	})
	form, err := reader.Fetch(context.Background(), "https://careers.example.com/?gh_jid=123&utm_source=arbeitnow.com")
	if err != nil {
		t.Fatal(err)
	}
	if form.Provider != ProviderGreenhouse || form.Board != "acme" || form.JobID != "123" || len(form.Fields) == 0 {
		t.Fatalf("form = %+v", form)
	}
	if form.ApplyURL != "https://careers.example.com/?gh_jid=123&utm_source=arbeitnow.com" {
		t.Errorf("the form should keep the link the user applies at, got %q", form.ApplyURL)
	}
}

// Greenhouse answers 404 for a job id it no longer has. The request names the
// job, so that closes it.
func TestCustomDomainGreenhouseJobGoneIsNotFound(t *testing.T) {
	reader := customDomainReader(t, http.NotFound)
	form, err := reader.Fetch(context.Background(), "https://careers.example.com/?gh_jid=123")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a job Greenhouse no longer has should be ErrNotFound, got %v", err)
	}
	if form.Provider != ProviderGreenhouse {
		t.Errorf("the composer should still be told which ATS it was, got %q", form.Provider)
	}
}

// Anything but a redirect naming this job's board leaves the form unread,
// never the job closed.
func TestCustomDomainGreenhouseUnclearAnswersAreNotClosures(t *testing.T) {
	answers := map[string]func(w http.ResponseWriter, r *http.Request){
		"board error page": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_board?for=&error=true", http.StatusFound)
		},
		"another job": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=acme&token=999", http.StatusMovedPermanently)
		},
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"no redirect":  func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html></html>")) },
	}
	for name, legacy := range answers {
		_, err := customDomainReader(t, legacy).Fetch(context.Background(), "https://careers.example.com/?gh_jid=123")
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want an unreadable form, got %v", name, err)
		}
	}
}
