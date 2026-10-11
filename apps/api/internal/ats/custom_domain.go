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

// greenhouseJobIDDigits bounds the job id taken from a link. Greenhouse's ids
// fit in an int64, which has at most 19 digits; a longer run of digits is not
// a Greenhouse job, and refusing it keeps a pasted link from putting an
// arbitrarily long request target on the lookup.
const greenhouseJobIDDigits = 19

var greenhouseJobIDPattern = regexp.MustCompile(fmt.Sprintf(`^\d{1,%d}$`, greenhouseJobIDDigits))

// greenhouseJobID reports a Greenhouse job whose link names no board: one on
// an employer's own domain, where Greenhouse's embed puts the job id in the
// gh_jid parameter, or Greenhouse's own embed link carrying only the job's
// token. Links that do name the board are left to Detect. When the parameter
// repeats, the first one is the job's, as url.Values.Get reads it.
func greenhouseJobID(applyURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	query := parsed.Query()
	if greenhousePattern.MatchString(strings.ToLower(parsed.Hostname())) {
		id := query.Get("token")
		if strings.Contains(parsed.Path, "/embed/job_app") && query.Get("for") == "" && greenhouseJobIDPattern.MatchString(id) {
			return id, true
		}
		return "", false
	}
	id := query.Get("gh_jid")
	return id, greenhouseJobIDPattern.MatchString(id)
}

// GreenhouseLookup is the address at Greenhouse that answers for a job whose
// link names no board: a redirect while the job is open, 404 once it is gone.
// The link check asks it instead of an employer's careers page, which answers
// the same whatever the job's state.
func GreenhouseLookup(link string) (string, bool) {
	id, ok := greenhouseJobID(link)
	if !ok {
		return "", false
	}
	return greenhouseLegacy + "/embed/job_app?token=" + id, true
}

// GreenhouseBoard names the board of a Greenhouse job whose link names none,
// such as a careers-site link carrying gh_jid. It asks only Greenhouse's own
// origin, never the link's host, and returns ErrUnsupported without a request
// when the link carries no Greenhouse job id.
func (r *Reader) GreenhouseBoard(ctx context.Context, link string) (board, jobID string, err error) {
	jobID, ok := greenhouseJobID(link)
	if !ok {
		return "", "", ErrUnsupported
	}
	board, err = r.greenhouseBoard(ctx, jobID)
	if err != nil {
		return "", jobID, err
	}
	return board, jobID, nil
}

// WithGreenhouseLegacyBase replaces the origin asked which board a job id is
// on, so a test outside this package can answer for Greenhouse. An empty base
// keeps the public host.
func (r *Reader) WithGreenhouseLegacyBase(base string) *Reader {
	r.greenhouseLegacyBase = strings.TrimSuffix(strings.TrimSpace(base), "/")
	return r
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
		return "", fmt.Errorf("Greenhouse did not say which board this job is on (it answered %d)", response.StatusCode)
	}
	location, err := response.Location()
	if err != nil {
		return "", fmt.Errorf("Greenhouse did not say which board this job is on")
	}
	provider, board, id, ok := Detect(location.String())
	if !ok || provider != ProviderGreenhouse || id != jobID {
		return "", fmt.Errorf("Greenhouse did not say which board this job is on")
	}
	return board, nil
}
