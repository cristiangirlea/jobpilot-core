package ats

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// greenhouseLegacy is Greenhouse's original board host. Asked for a job by its
// id alone, it answers with a redirect that names the job's board. That is how
// a posting on an employer's own careers site, which links only
// "careers.example.com/?gh_jid=123", is traced back to the board whose API
// publishes its form.
const greenhouseLegacy = "https://boards.greenhouse.io"

var digitsOnly = regexp.MustCompile(`^\d+$`)

// greenhouseJobID reports the Greenhouse job behind a link on an employer's own
// domain. Greenhouse's embed puts the job id in the gh_jid parameter; links on
// Greenhouse's own hosts are left to Detect.
func greenhouseJobID(applyURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil || parsed.Host == "" || greenhousePattern.MatchString(strings.ToLower(parsed.Host)) {
		return "", false
	}
	id := parsed.Query().Get("gh_jid")
	return id, digitsOnly.MatchString(id)
}

func (r *Reader) greenhouseLegacyOrigin() string {
	if r.greenhouseLegacyBase == "" {
		return greenhouseLegacy
	}
	return r.greenhouseLegacyBase
}

// greenhouseBoard asks Greenhouse which board a job id belongs to. It reads
// the redirect without following it. A 404 is definite, because the request
// names the job: Greenhouse no longer has it.
func (r *Reader) greenhouseBoard(ctx context.Context, jobID string) (string, error) {
	endpoint := r.greenhouseLegacyOrigin() + "/embed/job_app?token=" + url.QueryEscape(jobID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	once := &http.Client{Transport: r.client.Transport, Timeout: r.client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := once.Do(request)
	if err != nil {
		return "", fmt.Errorf("could not reach the employer's job board: %w", err)
	}
	response.Body.Close()

	switch {
	case response.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case response.StatusCode < 300 || response.StatusCode > 399:
		return "", fmt.Errorf("the employer's job board returned %d", response.StatusCode)
	}
	location, err := response.Location()
	if err != nil {
		return "", fmt.Errorf("the employer's job board gave no board for this job")
	}
	provider, board, id, ok := Detect(location.String())
	if !ok || provider != ProviderGreenhouse || id != jobID {
		return "", fmt.Errorf("the employer's job board gave no board for this job")
	}
	return board, nil
}
