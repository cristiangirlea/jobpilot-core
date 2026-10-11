package ats

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGreenhouseJobIDOnAnEmployersDomain(t *testing.T) {
	cases := []struct {
		link, id string
		ok       bool
	}{
		{"https://careers.nebius.com/?gh_jid=4967927101&utm_source=arbeitnow.com", "4967927101", true},
		{"https://example.com/jobs/backend?gh_jid=123", "123", true},
		// Greenhouse's own hosts are Detect's to read when they name the board,
		// with or without a port.
		{"https://job-boards.greenhouse.io/acme/jobs/123?gh_jid=123", "", false},
		{"https://job-boards.greenhouse.io:443/acme/jobs/123?gh_jid=123", "", false},
		{"https://boards.greenhouse.io/embed/job_app?for=acme&token=123", "", false},
		// Greenhouse's own embed link with only the job's token names no board.
		{"https://boards.greenhouse.io/embed/job_app?token=123", "123", true},
		{"https://example.com/jobs?gh_jid=abc", "", false},
		// A Greenhouse id fits in an int64: 19 digits is the longest accepted.
		{"https://example.com/jobs?gh_jid=9223372036854775807", "9223372036854775807", true},
		{"https://example.com/jobs?gh_jid=12345678901234567890", "", false},
		{"https://boards.greenhouse.io/embed/job_app?token=12345678901234567890", "", false},
		{"https://example.com/jobs?gh_jid=" + strings.Repeat("1", 1<<20), "", false},
		{"https://example.com/jobs?gh_jid=", "", false},
		// The first of a repeated parameter is the job's.
		{"https://example.com/jobs?gh_jid=111&gh_jid=222", "111", true},
		{"https://example.com/jobs?gh_jid=abc&gh_jid=222", "", false},
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

func TestGreenhouseLookupAsksGreenhouseNotTheCareersPage(t *testing.T) {
	if got, ok := GreenhouseLookup("https://careers.example.com/?gh_jid=123&utm_source=x"); !ok || got != "https://boards.greenhouse.io/embed/job_app?token=123" {
		t.Errorf("lookup = %q, %v", got, ok)
	}
	if _, ok := GreenhouseLookup("https://job-boards.greenhouse.io/acme/jobs/123"); ok {
		t.Error("a link that names its board needs no lookup")
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

func TestBareGreenhouseEmbedLinkIsReadFromItsBoard(t *testing.T) {
	reader := customDomainReader(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://job-boards.greenhouse.io/embed/job_app?for=acme&token=123", http.StatusMovedPermanently)
	})
	form, err := reader.Fetch(context.Background(), "https://boards.greenhouse.io/embed/job_app?token=123")
	if err != nil || form.Board != "acme" || len(form.Fields) == 0 {
		t.Fatalf("form = %+v, err = %v", form, err)
	}
}

// An id too long to be Greenhouse's is refused before any request, by the
// board lookup and the form reader alike.
func TestOverLongGreenhouseJobIDMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	reader := customDomainReader(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	})
	link := "https://careers.example.com/?gh_jid=12345678901234567890"
	if _, _, err := reader.GreenhouseBoard(context.Background(), link); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GreenhouseBoard: want ErrUnsupported, got %v", err)
	}
	if _, err := reader.Fetch(context.Background(), link); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Fetch: want ErrUnsupported, got %v", err)
	}
	if _, ok := GreenhouseLookup(link); ok {
		t.Error("GreenhouseLookup should refuse an over-long id")
	}
	if hits.Load() != 0 {
		t.Fatalf("want no request, got %d", hits.Load())
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
