package discovery

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Why a job was closed, in words the job page shows as they are.
const (
	ReasonGone     = "its link now returns 410 Gone"
	ReasonNotFound = "its link now returns 404 Not Found"
	ReasonBoard    = "the employer's board no longer shows it"
	ReasonUnlisted = "it is no longer listed on the employer's board"
)

// LinkChecker tells whether a posting's link still leads to the posting, and
// where an aggregator's posting hands over to the employer.
type LinkChecker struct {
	client *http.Client
	// once makes a single request and reports a redirect instead of following
	// it, so the Location an aggregator names is read, not fetched.
	once *http.Client
}

// NewLinkChecker builds a checker. Links come from job feeds and from users,
// so unless allowPrivate is set the checker refuses to connect to loopback,
// private and link-local addresses. The check runs on the address being
// dialled, after DNS, so a public name that resolves inward is refused too.
// Tests set allowPrivate to reach httptest servers.
func NewLinkChecker(allowPrivate bool) *LinkChecker {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		dialer.Control = refusePrivate
	}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	return &LinkChecker{
		client: &http.Client{Transport: transport, Timeout: 20 * time.Second},
		once: &http.Client{Transport: transport, Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func refusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("refusing to check a link on the private address %s", host)
	}
	return nil
}

// Check fetches link and reports whether the posting has closed. Only a
// definite answer closes a job; an error means "unknown", never "closed".
func (c *LinkChecker) Check(ctx context.Context, link string) (closed bool, reason string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return false, "", err
	}
	req.Header.Set("User-Agent", "JobPilot link check (self-hosted job tracker)")
	res, err := c.client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	closed, reason = closedBy(res.StatusCode, res.Request.URL)
	return closed, reason, nil
}

// closedBy reads a final response. 404 and 410 are definite; so is Greenhouse
// sending a closed job back to its board with error=true. Everything else,
// including 403 from bot protection, 429 and 5xx, says nothing about the
// posting, and one flaky answer must not hide a live job.
func closedBy(status int, final *url.URL) (bool, string) {
	switch status {
	case http.StatusGone:
		return true, ReasonGone
	case http.StatusNotFound:
		return true, ReasonNotFound
	}
	if final != nil && strings.HasSuffix(final.Hostname(), "greenhouse.io") && final.Query().Get("error") == "true" {
		return true, ReasonBoard
	}
	return false, ""
}

// employerRedirects names the aggregators whose postings hand over to the
// employer through a redirect, and the path on the posting that does it.
// Arbeitnow's "<posting>/apply" answers 302 with the employer's application
// page. It still does after the posting closes, so the redirect says where to
// apply, never whether the job is open: liveness stays with Check.
var employerRedirects = map[string]string{"arbeitnow": "/apply"}

// RedirectsToEmployer reports whether a provider's postings can be followed to
// the employer's own application page.
func RedirectsToEmployer(provider string) bool {
	_, ok := employerRedirects[provider]
	return ok
}

// EmployerURL asks an aggregator where its posting applies. It makes one
// request and reads the redirect without following it. An empty answer, like
// an error, means only "not known": it says nothing about the posting.
func (c *LinkChecker) EmployerURL(ctx context.Context, provider, link string) (string, error) {
	suffix, ok := employerRedirects[provider]
	if !ok || strings.TrimSpace(link) == "" {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(link, "/")+suffix, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JobPilot link check (self-hosted job tracker)")
	res, err := c.once.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 300 || res.StatusCode > 399 {
		return "", nil
	}
	location, err := res.Location()
	if err != nil {
		return "", nil
	}
	return employerLink(location, provider), nil
}

// employerLink accepts a redirect target as the employer's page only when it
// is an http or https address off the aggregator. It is shown to the user as
// a link, so any other scheme is refused outright, and a redirect back onto
// the aggregator (a login page, say) is not an employer.
func employerLink(target *url.URL, provider string) string {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return ""
	}
	for _, label := range strings.Split(strings.ToLower(target.Hostname()), ".") {
		if label == provider {
			return ""
		}
	}
	return target.String()
}
