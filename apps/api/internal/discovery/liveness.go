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
	// ReasonReposted closes an older copy of an opening a board reposted.
	// Seeing the old posting again does not reopen it.
	ReasonReposted = "a later posting of the same opening replaces it"
)

// LinkChecker tells whether a posting's link still leads to the posting.
type LinkChecker struct{ client *http.Client }

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
	return &LinkChecker{client: &http.Client{Transport: transport, Timeout: 20 * time.Second}}
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
