package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

const (
	pendingLimit = 64 << 20
	modelLimit   = 1 << 20
	postLimit    = 64 << 10
)

const pageSize = 200

const pendingPath = "/api/extractions/pending?limit=200"
const postPath = "/api/extractions"
const modelPath = "/v1/chat/completions"

var safeReason = regexp.MustCompile(`^[a-z_]{1,40}$`)

var errStopped = errors.New("run was stopped")

var errOff = errors.New("extraction is off on that server")

type pendingPage struct {
	Model         string                     `json:"model"`
	PromptVersion string                     `json:"prompt_version"`
	Jobs          []models.PendingExtraction `json:"jobs"`
}

type lengthStats struct {
	asked, rejected, failed int
	spent                   time.Duration
}

type summary struct {
	model, promptVersion                            string
	dryRun, stopped, more                           bool
	asked, stored, rejected, changed, gone, dropped int
	reasons, failures                               map[string]int
	elapsed, spent                                  time.Duration
	lengths                                         [4]lengthStats
}

type outcome struct {
	status, reason, failure string
	dropped                 int
	spent                   time.Duration
	length                  int
	asked                   bool
}

type batch struct {
	cfg          config
	api, model   *http.Client
	cancel       context.CancelCauseFunc
	out          io.Writer
	mu           sync.Mutex
	dialFailures int
	stats        summary
}

func run(ctx context.Context, cfg config, out io.Writer) (summary, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	modelTransport := newModelTransport()
	defer modelTransport.CloseIdleConnections()
	apiTransport := newAPITransport(cfg.apiURL)
	defer apiTransport.CloseIdleConnections()
	b := batch{
		cfg:    cfg,
		api:    &http.Client{Transport: directTransport{apiTransport}, CheckRedirect: refuseRedirect, Timeout: 2 * time.Minute},
		model:  &http.Client{Transport: directTransport{modelTransport}, CheckRedirect: refuseRedirect, Timeout: cfg.timeout},
		cancel: cancel,
		out:    out,
		stats:  summary{dryRun: cfg.dryRun, reasons: make(map[string]int), failures: make(map[string]int)},
	}
	start := time.Now()
	err := b.runPages(runCtx)
	if ctx.Err() != nil {
		b.stats.stopped = true
		err = errStopped
	} else if cause := context.Cause(runCtx); cause != nil {
		err = cause
	}
	b.stats.elapsed = time.Since(start)
	b.stats.print(out)
	return b.stats, err
}

func (b *batch) runPages(ctx context.Context) error {
	seen := make(map[string]bool)
	for ctx.Err() == nil && (b.cfg.max == 0 || b.stats.asked < b.cfg.max) {
		page, err := b.pending(ctx)
		if err != nil {
			return err
		}
		b.stats.model = publicSetting(page.Model, b.cfg.token)
		if page.PromptVersion == extraction.PromptVersion {
			b.stats.promptVersion = publicSetting(page.PromptVersion, b.cfg.token)
		} else {
			b.stats.promptVersion = "[different version]"
		}
		if page.Model == "" {
			return errOff
		}
		if page.PromptVersion != extraction.PromptVersion {
			return errors.New("the command and the server use different prompt versions")
		}
		var jobs []models.PendingExtraction
		found := false
		for _, job := range page.Jobs {
			if ctx.Err() != nil || (b.cfg.max > 0 && b.stats.asked+len(jobs) >= b.cfg.max) {
				break
			}
			if seen[job.ID] {
				continue
			}
			seen[job.ID] = true
			found = true
			if job.TextHash != extraction.Hash(job.Title, job.Description) {
				b.stats.record(outcome{failure: "hash", length: postingLength(job)})
				continue
			}
			jobs = append(jobs, job)
		}
		if !found {
			// A full page of postings already asked can hide older ones behind it.
			b.stats.more = len(page.Jobs) >= pageSize
			return nil
		}
		b.workPage(ctx, jobs)
		// A long run shows where it is, and a killed one leaves its last counts.
		fmt.Fprintf(b.out, "Progress: asked %d, stored %d, rejected %d, failed %d\n", b.stats.asked, b.stats.stored, b.stats.rejected, b.stats.failed())
	}
	return nil
}

func (b *batch) workPage(ctx context.Context, jobs []models.PendingExtraction) {
	var workers sync.WaitGroup
	queue := make(chan models.PendingExtraction)
	for range min(b.cfg.concurrency, len(jobs)) {
		workers.Go(func() {
			for job := range queue {
				if ctx.Err() != nil {
					return
				}
				b.process(ctx, job)
			}
		})
	}
send:
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			break send
		case queue <- job:
		}
	}
	close(queue)
	workers.Wait()
}

func (b *batch) process(ctx context.Context, job models.PendingExtraction) {
	length := postingLength(job)
	if ctx.Err() != nil {
		return
	}
	start := time.Now()
	answer, failure, dialFailed := b.ask(ctx, job)
	var result outcome
	b.mu.Lock()
	if ctx.Err() == nil {
		result.failure = failure
		if b.noteDial(dialFailed) {
			b.cancel(fmt.Errorf("the model server cannot be reached at %s", publicSetting(b.cfg.modelURL, b.cfg.token)))
		}
	}
	b.mu.Unlock()
	if failure == "" && !b.cfg.dryRun && ctx.Err() == nil {
		result = b.post(ctx, job, answer)
	}
	result.asked = true
	result.length = length
	result.spent = time.Since(start)
	b.mu.Lock()
	b.stats.record(result)
	b.mu.Unlock()
}

// noteDial counts connection failures in a row and reports the third.
func (b *batch) noteDial(failed bool) bool {
	if !failed {
		b.dialFailures = 0
		return false
	}
	b.dialFailures++
	return b.dialFailures == 3
}

func (b *batch) pending(ctx context.Context) (pendingPage, error) {
	var page pendingPage
	resp, err := doRequest(ctx, b.api, http.MethodGet, b.cfg.apiURL+pendingPath, b.cfg.token, nil)
	if err != nil {
		return page, fmt.Errorf("pending list request failed (%s)", transportKind(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if err := refusedToken(resp.StatusCode); err != nil {
			return page, err
		}
		return page, fmt.Errorf("pending list returned HTTP %d", resp.StatusCode)
	}
	if err := readJSON(resp, pendingLimit, &page); err != nil {
		return page, fmt.Errorf("pending list response failed (%s)", responseKind(err))
	}
	return page, nil
}

func (b *batch) ask(ctx context.Context, job models.PendingExtraction) (answer, failure string, dialFailed bool) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	request := map[string]any{
		"messages": []message{
			{Role: "system", Content: extraction.Prompt()},
			{Role: "user", Content: "TITLE: " + extraction.Text(job.Title, job.Description)},
		},
		"temperature":          0,
		"max_tokens":           3500,
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name": "label", "strict": true, "schema": extraction.Schema(),
			},
		},
	}
	resp, err := doRequest(ctx, b.model, http.MethodPost, b.cfg.modelURL+modelPath, "", request)
	if err != nil {
		kind := transportKind(err)
		var op *net.OpError
		return "", kind, kind == "transport" && errors.As(err, &op) && op.Op == "dial"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "status", false
	}
	var response struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if err := readJSON(resp, modelLimit, &response); err != nil {
		return "", responseKind(err), false
	}
	if len(response.Choices) == 0 {
		return "", "bad_response", false
	}
	var choice *struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content *string `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(response.Choices[0], &choice) != nil || choice == nil {
		return "", "bad_response", false
	}
	if choice.FinishReason != "stop" {
		return "", "cut_off", false
	}
	if choice.Message.Content == nil {
		return "", "bad_response", false
	}
	return *choice.Message.Content, "", false
}

func (b *batch) post(ctx context.Context, job models.PendingExtraction, answer string) outcome {
	resp, err := doRequest(ctx, b.api, http.MethodPost, b.cfg.apiURL+postPath, b.cfg.token, map[string]string{
		"job_id": job.ID, "text_hash": job.TextHash, "answer": answer,
	})
	if err != nil {
		if ctx.Err() != nil {
			return outcome{}
		}
		return outcome{failure: "post_transport"}
	}
	defer resp.Body.Close()
	if err := refusedToken(resp.StatusCode); err != nil {
		b.cancel(err)
		return outcome{}
	}
	switch resp.StatusCode {
	case http.StatusConflict:
		var refusal struct {
			Error string `json:"error"`
		}
		if readJSON(resp, postLimit, &refusal) == nil && refusal.Error == extraction.NotConfigured {
			b.cancel(errOff)
			return outcome{}
		}
		return outcome{status: "changed"}
	case http.StatusNotFound:
		return outcome{status: "gone"}
	case http.StatusOK:
		var saved struct {
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Dropped int    `json:"dropped"`
		}
		if err := readJSON(resp, postLimit, &saved); err != nil {
			if ctx.Err() != nil {
				return outcome{}
			}
			if !errors.Is(err, errBadResponse) {
				return outcome{failure: "post_transport"}
			}
			break
		}
		switch saved.Status {
		case "ok":
			return outcome{status: "ok", dropped: saved.Dropped}
		case "rejected":
			if !safeReason.MatchString(saved.Reason) || publicSetting(saved.Reason, b.cfg.token) != saved.Reason {
				saved.Reason = "other"
			}
			return outcome{status: "rejected", reason: saved.Reason}
		}
	}
	return outcome{failure: "post_status"}
}

type directTransport struct{ *http.Transport }

func (t directTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if err == nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Keep malformed redirects visible as HTTP statuses before the client parses Location.
		if _, err := req.URL.Parse(resp.Header.Get("Location")); err != nil {
			resp.Header.Del("Location")
		}
	}
	return resp, err
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func newModelTransport() *http.Transport {
	dialer := &net.Dialer{Control: loopbackControl}
	return &http.Transport{Proxy: nil, DialContext: dialer.DialContext}
}

func newAPITransport(apiURL string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.HasPrefix(apiURL, "http://") {
		// Plain HTTP carries the token, so it may only reach this computer.
		transport.Proxy = nil
		transport.DialContext = (&net.Dialer{Control: loopbackControl}).DialContext
	}
	return transport
}

func loopbackControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("connection address is not loopback")
	}
	return nil
}

func refusedToken(status int) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("API returned HTTP %d: the token was refused or lacks admin access with write scope", status)
	}
	return nil
}

func doRequest(ctx context.Context, client *http.Client, method, endpoint, token string, body any) (*http.Response, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return client.Do(req)
}

var errBadResponse = errors.New("bad response")

func readJSON(resp *http.Response, limit int64, dst any) error {
	if resp.ContentLength > limit {
		return errBadResponse
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, limit))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errBadResponse
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(body, dst) != nil || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return errBadResponse
	}
	return nil
}

func responseKind(err error) string {
	if errors.Is(err, errBadResponse) {
		return "bad_response"
	}
	return transportKind(err)
}

func transportKind(err error) string {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return "timeout"
	}
	return "transport"
}

func publicSetting(value, token string) string {
	if token != "" && strings.Contains(value, token) {
		return "[redacted]"
	}
	return value
}

func postingLength(job models.PendingExtraction) int {
	return utf8.RuneCountInString(job.Title) + 1 + utf8.RuneCountInString(job.Description)
}

func (s *summary) record(result outcome) {
	bucket := 0
	switch {
	case result.length >= 12000:
		bucket = 3
	case result.length >= 6000:
		bucket = 2
	case result.length >= 2000:
		bucket = 1
	}
	length := &s.lengths[bucket]
	if result.asked {
		s.asked++
		length.asked++
		s.spent += result.spent
		length.spent += result.spent
	}
	if result.failure != "" {
		s.failures[result.failure]++
		length.failed++
	}
	switch result.status {
	case "ok":
		s.stored++
		s.dropped += result.dropped
	case "rejected":
		s.rejected++
		s.reasons[result.reason]++
		length.rejected++
	case "changed":
		s.changed++
	case "gone":
		s.gone++
	}
}

func (s summary) print(out io.Writer) {
	fmt.Fprintf(out, "Model: %q\nPrompt version: %q\nDry run: %t\n", s.model, s.promptVersion, s.dryRun)
	fmt.Fprintf(out, "Asked: %d\nStored ok: %d\nRejected: %d\n", s.asked, s.stored, s.rejected)
	reasons := make([]string, 0, len(s.reasons))
	for reason := range s.reasons {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(out, "  %s: %d\n", reason, s.reasons[reason])
	}
	fmt.Fprintf(out, "Changed: %d\nGone: %d\nFailed: %d\n", s.changed, s.gone, s.failed())
	for _, kind := range []string{"timeout", "status", "cut_off", "bad_response", "transport", "hash", "post_status", "post_transport"} {
		fmt.Fprintf(out, "  %s: %d\n", kind, s.failures[kind])
	}
	fmt.Fprintf(out, "Dropped items: %d\nElapsed seconds: %.3f\nMean seconds per asked posting: %.3f\n", s.dropped, s.elapsed.Seconds(), meanSeconds(s.spent, s.asked))
	fmt.Fprintf(out, "%-16s %6s %8s %6s %12s\n", "Text characters", "Asked", "Rejected", "Failed", "Mean seconds")
	for i, label := range []string{"Under 2,000", "2,000-5,999", "6,000-11,999", "12,000 and over"} {
		row := s.lengths[i]
		fmt.Fprintf(out, "%-16s %6d %8d %6d %12.3f\n", label, row.asked, row.rejected, row.failed, meanSeconds(row.spent, row.asked))
	}
	if s.more {
		fmt.Fprintln(out, "More postings may be pending: the last page held only postings already asked.")
	}
	if s.stopped {
		fmt.Fprintln(out, "Run was stopped.")
	}
}

func (s summary) failed() int {
	failed := 0
	for _, count := range s.failures {
		failed += count
	}
	return failed
}

func meanSeconds(spent time.Duration, asked int) float64 {
	if asked == 0 {
		return 0
	}
	return spent.Seconds() / float64(asked)
}
