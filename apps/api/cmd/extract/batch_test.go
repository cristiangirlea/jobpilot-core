package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jobpilot/core/api/internal/extraction"
	"jobpilot/core/api/internal/models"
)

const testToken = "invented-secret-for-test"
const marker = "PRIVATE-MARKER"

func posting(id, description string) models.PendingExtraction {
	title := "Entwickler für Example GmbH " + marker
	description = description + "\nStellenbeschreibung: " + marker
	return models.PendingExtraction{ID: id, Title: title, Description: description, TextHash: extraction.Hash(title, description)}
}

func reply(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}

func modelReply(w http.ResponseWriter, reason, content string) {
	reply(w, map[string]any{"choices": []any{map[string]any{
		"finish_reason": reason, "message": map[string]string{"content": content},
	}}})
}

func fakeAPI(t *testing.T, list func(int) []models.PendingExtraction, post http.HandlerFunc) string {
	t.Helper()
	var pages atomic.Int32
	return fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Error("missing API bearer token")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.RequestURI() == pendingPath:
			reply(w, pendingPage{Model: "example-model", PromptVersion: extraction.PromptVersion, Jobs: list(int(pages.Add(1)))})
		case r.Method == http.MethodPost && r.URL.RequestURI() == postPath && post != nil:
			post(w, r)
		default:
			t.Error("unexpected API request")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
}

func fakeServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func testConfig(api, model string) config {
	return config{apiURL: api, modelURL: model, token: testToken, concurrency: 1, timeout: 3 * time.Second}
}

func checkedRun(t *testing.T, ctx context.Context, cfg config) (summary, string, error) {
	t.Helper()
	var out bytes.Buffer
	s, err := run(ctx, cfg, &out)
	if combined := out.String() + fmt.Sprint(err); strings.Contains(combined, marker) || strings.Contains(combined, testToken) {
		t.Fatal("private content appeared in output or error text")
	}
	return s, out.String(), err
}

func TestModelRequestAndRawSubmission(t *testing.T) {
	jobs := []models.PendingExtraction{posting(marker+"-1", "Öffentliche Stelle"), posting(marker+"-2", "Größe"), posting(marker+"-3", "Büro")}
	answers := []string{" \t{\"example\":\"" + marker + "\"}\n", "unparseable " + marker, ""}
	var asked, posted atomic.Int32
	model := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		i := int(asked.Add(1)) - 1
		if i >= len(jobs) {
			t.Error("too many model requests")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.RequestURI() != "/v1/chat/completions" || r.Header.Get("Content-Type") != "application/json" || r.Header.Values("Authorization") != nil {
			t.Error("incorrect model request method, URL or headers")
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error("cannot decode model request")
		}
		var schema any
		if err := json.Unmarshal(extraction.Schema(), &schema); err != nil {
			t.Error("cannot decode extraction schema")
		}
		want := map[string]any{
			"messages": []any{
				map[string]any{"role": "system", "content": extraction.Prompt()},
				map[string]any{"role": "user", "content": "TITLE: " + jobs[i].Title + "\n" + jobs[i].Description},
			},
			"temperature":          float64(0),
			"max_tokens":           float64(3500),
			"chat_template_kwargs": map[string]any{"enable_thinking": false},
			"response_format": map[string]any{
				"type": "json_schema", "json_schema": map[string]any{"name": "label", "strict": true, "schema": schema},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Error("model request did not match the measured request")
		}
		modelReply(w, "stop", answers[i])
	})
	api := fakeAPI(t, func(page int) []models.PendingExtraction {
		if page == 1 {
			return jobs
		}
		return nil
	}, func(w http.ResponseWriter, r *http.Request) {
		i := int(posted.Add(1)) - 1
		if i >= len(jobs) {
			t.Error("too many POST requests")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var got map[string]any
		want := map[string]any{"job_id": jobs[i].ID, "text_hash": jobs[i].TextHash, "answer": answers[i]}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(got, want) || r.Header.Get("Content-Type") != "application/json" {
			t.Error("POST did not carry the original answer and expected fields")
		}
		if i == 0 {
			reply(w, map[string]any{"status": "ok", "dropped": 3})
			return
		}
		reply(w, map[string]string{"status": "rejected", "reason": "invalid"})
	})
	s, out, err := checkedRun(t, context.Background(), testConfig(api, model))
	if err != nil || asked.Load() != 3 || posted.Load() != 3 || s.asked != 3 || s.stored != 1 || s.reasons["invalid"] != 2 || s.dropped != 3 {
		t.Fatalf("unexpected submission results: %+v, %v", s, err)
	}
	for _, want := range []string{"example-model", extraction.PromptVersion, "Dry run: false", "Asked: 3", "Stored ok: 1", "invalid: 2", "Dropped items: 3", "Elapsed seconds:", "Mean seconds per asked posting:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q", want)
		}
	}
}

func TestFailedPostingsAreAskedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		handler    http.HandlerFunc
	}{
		{"cut off", "cut_off", func(w http.ResponseWriter, _ *http.Request) { modelReply(w, "length", marker) }},
		{"status", "status", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, marker, http.StatusInternalServerError) }},
		{"invalid JSON", "bad_response", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, marker) }},
		{"no choices", "bad_response", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"choices":[]}`) }},
		{"null choice", "bad_response", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"choices":[null]}`) }},
		{"null content", "bad_response", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":null}}]}`)
		}},
		{"timeout", "timeout", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}},
		{"hash mismatch", "hash", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := posting(marker+"-1", "Beschreibung")
			if tc.handler == nil {
				job.TextHash = "mismatch"
			}
			var calls, pages atomic.Int32
			model := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.handler == nil {
					t.Error("model asked for a posting with a mismatched hash")
					return
				}
				tc.handler(w, r)
			})
			api := fakeAPI(t, func(page int) []models.PendingExtraction {
				pages.Store(int32(page))
				return []models.PendingExtraction{job, job}
			}, nil)
			cfg := testConfig(api, model)
			if tc.kind == "timeout" {
				cfg.timeout = 30 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, _, err := checkedRun(t, ctx, cfg)
			wantAsked := 1
			if tc.kind == "hash" {
				wantAsked = 0
			}
			if err != nil || s.asked != wantAsked || (tc.kind != "timeout" && int(calls.Load()) != wantAsked) || s.failures[tc.kind] != 1 || len(s.failures) != 1 || pages.Load() != 2 {
				t.Fatalf("unexpected failure results: %+v, %v, pages=%d, model calls=%d", s, err, pages.Load(), calls.Load())
			}
		})
	}
}

func TestPostResultsAndLengthBuckets(t *testing.T) {
	var jobs []models.PendingExtraction
	for i, length := range []int{1999, 2000, 5999, 6000, 11999, 12000} {
		job := posting(marker+fmt.Sprint(i), "")
		jobs = append(jobs, posting(job.ID, strings.Repeat("ü", length-postingLength(job))))
	}
	model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { modelReply(w, "stop", marker) })
	var posted atomic.Int32
	api := fakeAPI(t, func(int) []models.PendingExtraction { return jobs }, func(w http.ResponseWriter, _ *http.Request) {
		switch posted.Add(1) {
		case 1:
			http.Error(w, marker, http.StatusConflict)
		case 2:
			http.Error(w, marker, http.StatusNotFound)
		case 3:
			reply(w, map[string]string{"status": "rejected", "reason": "invalid_quote"})
		case 4:
			reply(w, map[string]string{"status": "rejected", "reason": marker})
		case 5:
			http.Error(w, marker, http.StatusBadRequest)
		case 6:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("cannot close fake API connection")
				return
			}
			conn.Close()
		}
	})
	s, out, err := checkedRun(t, context.Background(), testConfig(api, model))
	if err != nil || s.asked != 6 || s.changed != 1 || s.gone != 1 || s.rejected != 2 || s.reasons["invalid_quote"] != 1 || s.reasons["other"] != 1 || s.failures["post_status"] != 1 || s.failures["post_transport"] != 1 {
		t.Fatalf("unexpected POST results: %+v, %v", s, err)
	}
	for i, want := range [][3]int{{1, 0, 0}, {2, 1, 0}, {2, 1, 1}, {1, 0, 1}} {
		row := s.lengths[i]
		if got := [3]int{row.asked, row.rejected, row.failed}; got != want {
			t.Errorf("bucket %d = %+v, want counts %v", i, row, want)
		}
	}
	if !strings.Contains(out, "invalid_quote: 1") || !strings.Contains(out, "other: 1") {
		t.Error("summary missing the rejection counts")
	}
}

func TestMaxAcrossPagesAndDryRun(t *testing.T) {
	var pages, asked atomic.Int32
	model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		modelReply(w, "stop", marker)
	})
	api := fakeAPI(t, func(page int) []models.PendingExtraction {
		pages.Store(int32(page))
		first := posting(marker+"-1", "Eins")
		if page == 1 {
			bad := posting(marker+"-0", "Null")
			bad.TextHash = "mismatch"
			return []models.PendingExtraction{bad, first}
		}
		return []models.PendingExtraction{first, posting(marker+"-2", "Zwei"), posting(marker+"-3", "Drei")}
	}, nil)
	cfg := testConfig(api, model)
	cfg.dryRun, cfg.max, cfg.concurrency = true, 2, 4
	s, out, err := checkedRun(t, context.Background(), cfg)
	if err != nil || asked.Load() != 2 || s.asked != 2 || s.failures["hash"] != 1 || pages.Load() != 2 || !strings.Contains(out, "Dry run: true") {
		t.Fatalf("unexpected dry-run results: %+v, %v, pages=%d", s, err, pages.Load())
	}
}

func TestConcurrencyAndCancellation(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			jobs := make([]models.PendingExtraction, 10)
			for i := range jobs {
				jobs[i] = posting(marker+fmt.Sprint(i), "Beschreibung")
			}
			const concurrency = 3
			var inFlight, peak atomic.Int32
			var reached, release = make(chan struct{}), make(chan struct{})
			model := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				n := inFlight.Add(1)
				defer func() {
					inFlight.Add(-1)
				}()
				for old := peak.Load(); old < n; old = peak.Load() {
					if peak.CompareAndSwap(old, n) {
						if n == concurrency {
							close(reached)
						}
						break
					}
				}
				select {
				case <-release:
					modelReply(w, "stop", marker)
				case <-r.Context().Done():
				}
			})
			api := fakeAPI(t, func(int) []models.PendingExtraction { return jobs }, nil)
			cfg := testConfig(api, model)
			cfg.timeout, cfg.concurrency, cfg.dryRun = 20*time.Second, concurrency, true
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			released := make(chan time.Time, 1)
			go func() {
				select {
				case <-reached:
					released <- time.Now()
					if stop {
						cancel()
					} else {
						close(release)
					}
				case <-ctx.Done():
				}
			}()
			s, out, err := checkedRun(t, ctx, cfg)
			if peak.Load() != concurrency || len(s.failures) != 0 {
				t.Fatalf("unexpected overlapping requests or failures: peak=%d, %+v", peak.Load(), s)
			}
			if stop {
				if !errors.Is(err, errStopped) || !s.stopped || s.asked != concurrency || !strings.Contains(out, "Run was stopped.") {
					t.Fatalf("unexpected stopped run: %+v, %v", s, err)
				}
				if time.Since(<-released) > 2*time.Second {
					t.Error("run did not stop promptly")
				}
			} else if err != nil || s.asked != len(jobs) {
				t.Fatalf("unexpected concurrent run: %+v, %v", s, err)
			}
		})
	}
}

func TestSettings(t *testing.T) {
	for _, raw := range []string{
		"http://example.test", "http://192.168.1.5", "https://localhost", "http://user:PRIVATE-MARKER@localhost",
		"http://localhost/extra", "http://localhost?", "http://localhost#", "http://[localhost]", "http://localhost:65536",
	} {
		if _, err := validateModelURL(raw); err == nil || strings.Contains(err.Error(), marker) {
			t.Errorf("unsafe model URL accepted or leaked: %q", raw)
		}
	}
	for _, raw := range []string{
		"", "http://example.test", "https://user:PRIVATE-MARKER@example.test", "https://example.test/extra",
		"https://example.test/?q=PRIVATE-MARKER", "https://example.test#", "https://[example.test]",
	} {
		if _, err := validateAPIURL(raw); err == nil || strings.Contains(err.Error(), marker) {
			t.Errorf("unsafe API URL accepted or leaked: %q", raw)
		}
	}
	for _, raw := range []string{"http://localhost:8081/", "http://127.1.2.3", "http://[::1]:8081"} {
		want := strings.TrimSuffix(raw, "/")
		if got, err := validateModelURL(raw); got != want || err != nil {
			t.Errorf("valid model URL %q refused: %v", raw, err)
		}
		if got, err := validateAPIURL(raw); got != want || err != nil {
			t.Errorf("valid API URL %q refused: %v", raw, err)
		}
	}
	if _, err := validateAPIURL("https://example.test:8443"); err != nil {
		t.Errorf("valid HTTPS API URL refused: %v", err)
	}
	path := filepath.Join(t.TempDir(), marker)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, path string }{{"", ""}, {testToken, path}, {"", path}, {"", path + "-missing"}} {
		if _, err := readToken(tc.value, tc.path); err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), marker) {
			t.Error("invalid token settings accepted or leaked")
		}
	}
	for _, tc := range []struct{ input, remaining string }{{"", ""}, {"\n", ""}, {"\r\n", ""}} {
		if err := os.WriteFile(path, []byte(testToken+tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := readToken("", path); got != testToken+tc.remaining || err != nil {
			t.Errorf("unexpected result reading token file with ending %q: %v", tc.input, err)
		}
	}
	env := map[string]string{"JOBPILOT_API_URL": "https://example.test", "JOBPILOT_TOKEN": testToken}
	getenv := func(key string) string { return env[key] }
	for _, args := range [][]string{{"--concurrency=0"}, {"--concurrency=9"}, {"--timeout=9s"}, {"--timeout=16m"}, {"--max=-1"}, {"--timeout=" + testToken}} {
		if _, err := settings(args, getenv, io.Discard); err == nil || strings.Contains(err.Error(), testToken) {
			t.Error("invalid flags accepted or leaked")
		}
	}
	cfg, err := settings(nil, getenv, io.Discard)
	if err != nil || cfg.token != testToken || cfg.modelURL != "http://127.0.0.1:8081" || cfg.concurrency != 4 || cfg.timeout != 2*time.Minute || cfg.max != 0 || cfg.dryRun {
		t.Fatalf("unexpected default settings: %v", err)
	}
}

func TestLoopbackControl(t *testing.T) {
	for _, address := range []string{"192.0.2.1:80", "localhost:80", "not-an-address"} {
		if err := loopbackControl("tcp", address, nil); err == nil {
			t.Errorf("dial guard allowed %q", address)
		}
	}
	for _, address := range []string{"127.0.0.1:80", "[::1]:80"} {
		if err := loopbackControl("tcp", address, nil); err != nil {
			t.Errorf("dial guard refused %q: %v", address, err)
		}
	}
	if newModelTransport().Proxy != nil {
		t.Error("model transport can use a proxy")
	}
}

func TestRedirects(t *testing.T) {
	var redirected atomic.Int32
	destination := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		http.Error(w, marker, http.StatusBadRequest)
	})
	job := posting(marker+"-1", "Beschreibung")
	api := fakeAPI(t, func(int) []models.PendingExtraction { return []models.PendingExtraction{job} }, nil)
	model := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination, http.StatusTemporaryRedirect)
	})
	s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
	if err != nil || s.asked != 1 || s.failures["status"] != 1 || redirected.Load() != 0 {
		t.Fatalf("unexpected model redirect result: %+v, %v", s, err)
	}
	for _, target := range []string{destination, "http://["} {
		api := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", target)
			http.Error(w, marker, http.StatusFound)
		})
		s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
		if err == nil || !strings.Contains(err.Error(), "302") || s.asked != 0 || redirected.Load() != 0 {
			t.Fatalf("unexpected API redirect result: %+v, %v", s, err)
		}
	}
}

func TestAPIRefusesToken(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, duringPost := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d post=%t", status, duringPost), func(t *testing.T) {
				jobs := []models.PendingExtraction{posting(marker+"-1", "Eins"), posting(marker+"-2", "Zwei")}
				var requests, asked atomic.Int32
				api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if duringPost && r.Method == http.MethodGet {
						reply(w, pendingPage{Model: "example-model", PromptVersion: extraction.PromptVersion, Jobs: jobs})
						return
					}
					http.Error(w, marker, status)
				})
				model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
					asked.Add(1)
					modelReply(w, "stop", marker)
				})
				s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
				var wantAsked int32
				if duringPost {
					wantAsked = 1
				}
				if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || !strings.Contains(err.Error(), "write scope") || asked.Load() != wantAsked || requests.Load() != 1+wantAsked || len(s.failures) != 0 {
					t.Fatalf("unexpected authorization outcome: %+v, %v, API calls=%d", s, err, requests.Load())
				}
			})
		}
	}
}

func TestDialFailureStreak(t *testing.T) {
	// The dial guard refuses this address at once, so the test needs no closed port and no network.
	const closed = "http://192.0.2.1:80"
	jobs := []models.PendingExtraction{posting("first", "Eins"), posting("second", "Zwei"), posting("third", "Drei"), posting("fourth", "Vier")}
	api := fakeAPI(t, func(page int) []models.PendingExtraction {
		if page == 1 {
			return jobs[:2]
		}
		return jobs
	}, nil)
	s, _, err := checkedRun(t, context.Background(), testConfig(api, closed))
	if err == nil || !strings.Contains(err.Error(), "the model server cannot be reached at "+closed) || s.asked != 3 || s.failures["transport"] != 3 {
		t.Fatalf("unexpected dial failure results: %+v, %v", s, err)
	}
	model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, marker, http.StatusInternalServerError) })
	s, _, err = checkedRun(t, context.Background(), testConfig(api, model))
	if err != nil || s.asked != 4 || s.failures["status"] != 4 {
		t.Fatalf("unexpected status failure results: %+v, %v", s, err)
	}
}

func TestPendingErrors(t *testing.T) {
	model := fakeServer(t, func(http.ResponseWriter, *http.Request) { t.Error("model contacted despite pending list error") })
	for _, tc := range []struct {
		want    string
		handler http.HandlerFunc
	}{
		{"HTTP 500", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, marker, http.StatusInternalServerError) }},
		{"transport", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error("cannot close fake API connection")
				return
			}
			conn.Close()
		}},
		{"bad_response", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, marker) }},
		{"extraction is off", func(w http.ResponseWriter, _ *http.Request) {
			reply(w, pendingPage{PromptVersion: extraction.PromptVersion})
		}},
		{"different prompt versions", func(w http.ResponseWriter, _ *http.Request) {
			reply(w, pendingPage{Model: "example-model", PromptVersion: marker})
		}},
	} {
		t.Run(tc.want, func(t *testing.T) {
			api := fakeServer(t, tc.handler)
			s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
			if err == nil || !strings.Contains(err.Error(), tc.want) || s.asked != 0 {
				t.Fatalf("unexpected pending list result: %+v, %v", s, err)
			}
		})
	}
}

func TestEmptyPendingList(t *testing.T) {
	api := fakeAPI(t, func(int) []models.PendingExtraction { return nil }, nil)
	model := fakeServer(t, func(http.ResponseWriter, *http.Request) { t.Error("model contacted for an empty list") })
	s, out, err := checkedRun(t, context.Background(), testConfig(api, model))
	if err != nil || s.asked != 0 || len(s.failures) != 0 || !strings.Contains(out, "Mean seconds per asked posting: 0.000") {
		t.Fatalf("unexpected empty run: %+v, %v", s, err)
	}
}

func TestResponseLimits(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		limit    int
		failure  string
	}{
		{"pending", 64 << 20, "bad_response"},
		{"model", 1 << 20, "bad_response"},
		{"post", 64 << 10, "post_status"},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			oversized := func(w http.ResponseWriter) {
				w.Header().Set("Content-Length", fmt.Sprint(tc.limit+1))
				fmt.Fprint(w, marker)
			}
			model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				if tc.endpoint == "model" {
					oversized(w)
					return
				}
				modelReply(w, "stop", marker)
			})
			api := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.endpoint == "pending" || (tc.endpoint == "post" && r.Method == http.MethodPost) {
					oversized(w)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("unexpected API POST")
				}
				reply(w, pendingPage{Model: "example-model", PromptVersion: extraction.PromptVersion, Jobs: []models.PendingExtraction{posting(marker, "Beschreibung")}})
			})
			s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
			if tc.endpoint == "pending" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) || s.asked != 0 {
					t.Fatalf("oversized pending response outcome: %+v, %v", s, err)
				}
			} else if err != nil || s.asked != 1 || s.failures[tc.failure] != 1 {
				t.Fatalf("oversized response outcome: %+v, %v", s, err)
			}
		})
	}
	resp := &http.Response{ContentLength: -1, Body: io.NopCloser(strings.NewReader("{}" + strings.Repeat(" ", 15)))}
	if err := readJSON(resp, 16, &struct{}{}); !errors.Is(err, errBadResponse) {
		t.Errorf("oversized response without content length was accepted: %v", err)
	}
}

// Timings are checked on fixed durations: a loopback request can take less than one clock tick.
func TestTimingsAreSummedPerLengthBucket(t *testing.T) {
	s := summary{reasons: map[string]int{}, failures: map[string]int{}}
	s.record(outcome{asked: true, status: "ok", length: 1999, spent: 2 * time.Second})
	s.record(outcome{asked: true, failure: "timeout", length: 2000, spent: 4 * time.Second})
	s.record(outcome{asked: true, status: "rejected", reason: "not_json", length: 5999, spent: 6 * time.Second})
	s.record(outcome{asked: true, status: "ok", length: 12000, spent: 8 * time.Second})
	s.record(outcome{failure: "hash", length: 6000, spent: time.Hour})
	want := [4]lengthStats{{asked: 1, spent: 2 * time.Second}, {asked: 2, rejected: 1, failed: 1, spent: 10 * time.Second}, {failed: 1}, {asked: 1, spent: 8 * time.Second}}
	if s.lengths != want || s.asked != 4 || s.spent != 20*time.Second || s.failed() != 2 {
		t.Fatalf("summary = %+v, want buckets %+v", s, want)
	}
	var out bytes.Buffer
	s.print(&out)
	for _, line := range []string{"Mean seconds per asked posting: 5.000", "Failed: 2"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}
}

func TestDialStreakResets(t *testing.T) {
	var b batch
	for i, step := range []struct{ failed, stop bool }{{true, false}, {true, false}, {false, false}, {true, false}, {true, false}, {true, true}} {
		if got := b.noteDial(step.failed); got != step.stop {
			t.Fatalf("step %d: stop = %v, want %v", i, got, step.stop)
		}
	}
}

// Plain HTTP carries the token, so the API client connects to this computer only.
func TestAPIOverHTTPDialsLoopbackOnly(t *testing.T) {
	model := fakeServer(t, func(http.ResponseWriter, *http.Request) { t.Error("model asked without a pending list") })
	s, _, err := checkedRun(t, context.Background(), testConfig("http://192.0.2.1:80", model))
	if err == nil || !strings.Contains(err.Error(), "pending list request failed (transport)") || s.asked != 0 {
		t.Fatalf("an API address that is not loopback was dialled: %+v, %v", s, err)
	}
}

func TestExtractionSwitchedOffDuringARun(t *testing.T) {
	model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { modelReply(w, "stop", "{}") })
	api := fakeAPI(t, func(int) []models.PendingExtraction {
		return []models.PendingExtraction{posting("first", "Eins"), posting("second", "Zwei")}
	}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		reply(w, map[string]string{"error": extraction.NotConfigured})
	})
	s, _, err := checkedRun(t, context.Background(), testConfig(api, model))
	if !errors.Is(err, errOff) || s.changed != 0 || s.asked != 1 {
		t.Fatalf("a refusal because extraction is off was not the end of the run: %+v, %v", s, err)
	}
}

func TestAFullPageOfAskedPostingsIsSaid(t *testing.T) {
	model := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	jobs := make([]models.PendingExtraction, pageSize)
	for i := range jobs {
		jobs[i] = posting(fmt.Sprint("job-", i), "Beschreibung")
	}
	api := fakeAPI(t, func(int) []models.PendingExtraction { return jobs }, nil)
	cfg := testConfig(api, model)
	cfg.concurrency = 8
	s, out, err := checkedRun(t, context.Background(), cfg)
	if err != nil || !s.more || s.asked != pageSize || !strings.Contains(out, "More postings may be pending") {
		t.Fatalf("a full page of asked postings was not reported: %+v, %v", s, err)
	}
	s, out, err = checkedRun(t, context.Background(), testConfig(fakeAPI(t, func(int) []models.PendingExtraction { return jobs[:3] }, nil), model))
	if err != nil || s.more || strings.Contains(out, "More postings may be pending") {
		t.Fatalf("a short page was reported as hiding postings: %+v, %v", s, err)
	}
}

func TestExitCodes(t *testing.T) {
	model := fakeServer(t, func(http.ResponseWriter, *http.Request) { t.Error("model asked with nothing pending") })
	api := fakeAPI(t, func(int) []models.PendingExtraction { return nil }, nil)
	env := map[string]string{"JOBPILOT_API_URL": api, "JOBPILOT_TOKEN": testToken}
	getenv := func(key string) string { return env[key] }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name         string
		ctx          context.Context
		args         []string
		getenv       func(string) string
		code         int
		stdout       string
		wantsMessage bool
	}{
		{"help", context.Background(), []string{"--help"}, getenv, 0, "Usage: extract", false},
		{"finished", context.Background(), []string{"--model-url=" + model}, getenv, 0, "Asked: 0", false},
		{"no settings", context.Background(), nil, func(string) string { return "" }, 1, "", true},
		{"bad flag", context.Background(), []string{"--concurrency=9"}, getenv, 1, "", true},
		{"stopped", cancelled, []string{"--model-url=" + model}, getenv, 1, "Run was stopped.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := realMain(tc.ctx, tc.args, tc.getenv, &stdout, &stderr)
			if code != tc.code || !strings.Contains(stdout.String(), tc.stdout) || (stderr.Len() > 0) != tc.wantsMessage {
				t.Fatalf("code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
			if combined := stdout.String() + stderr.String(); strings.Contains(combined, testToken) || strings.Contains(combined, marker) {
				t.Fatal("private content appeared in output")
			}
		})
	}
}

func TestOddTokensAreRefusedWithoutEcho(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(testToken+"\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ value, path string }{{"invented secret", ""}, {string(rune(0xFEFF)) + testToken, ""}, {testToken + "\t", ""}, {"", path}} {
		if _, err := readToken(tc.value, tc.path); err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "invented") {
			t.Errorf("an odd token was accepted or echoed: %v", err)
		}
	}
}

// The guard runs before any connection attempt, so these dials fail the same way with or without a route.
func TestTransportsRefuseOtherAddresses(t *testing.T) {
	const refusal = "connection address is not loopback"
	for name, transport := range map[string]*http.Transport{"model": newModelTransport(), "API over HTTP": newAPITransport("http://localhost:8080")} {
		if transport.Proxy != nil {
			t.Errorf("%s transport may use a proxy", name)
		}
		conn, err := transport.DialContext(context.Background(), "tcp", "192.0.2.1:80")
		if err == nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), refusal) {
			t.Errorf("%s transport dialled an address that is not loopback: %v", name, err)
		}
	}
	// HTTPS reaches the API through the usual dialer and proxy settings.
	if https := newAPITransport("https://example.test"); https.Proxy == nil {
		t.Error("the HTTPS API transport lost its proxy setting")
	}
}
