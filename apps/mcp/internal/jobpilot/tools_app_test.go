package jobpilot

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type appRequest struct {
	Route string
	Body  map[string]any
}

const settingsFixture = `{"enabled":true,"interval_minutes":720,"auto_prepare":true,"min_score":80,"max_jobs_per_run":42,"last_run_at":"2026-09-11T00:00:00Z","next_run_at":"2026-09-12T00:00:00Z"}`
const summaryFixture = `{"last_job_at":"2026-09-11T00:00:00Z","jobs_last_24h":2,"jobs_last_7d":8,"open_jobs":10,"open_matches":3}`

func (f *fakeAPI) recordAppRequest(w http.ResponseWriter, r *http.Request, send func(http.ResponseWriter, int, string)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.appRequests = append(f.appRequests, appRequest{r.Method + " " + r.URL.Path, body})
	if f.member && strings.HasPrefix(r.URL.Path, "/api/discovery/") {
		send(w, 403, `{"error":"Discovery access denied by fixture M403."}`)
		return true
	}
	if f.appWriteDenied && r.Method != http.MethodGet {
		send(w, 403, `{"error":"This account's agent tokens can only read. Policy fixture W403."}`)
		return true
	}
	if f.appSource != "" && r.Method == http.MethodGet && r.URL.Path == "/api/discovery/sources" {
		send(w, 200, "["+f.appSource+"]")
		return true
	}
	return false
}

func (f *fakeAPI) appRoutes(m *http.ServeMux, send func(http.ResponseWriter, int, string)) {
	m.HandleFunc("GET /api/blacklist", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `[{"id":"b-1","company_name":"Excluded Ltd","company_domain":"excluded.test","normalized_name":"excluded","reason":"User choice","created_at":"2026-09-11T00:00:00Z"}]`)
	})
	m.HandleFunc("POST /api/blacklist", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["id"] = "b-new"
		raw, _ := json.Marshal(body)
		send(w, 201, string(raw))
	})
	m.HandleFunc("GET /api/discovery/settings", func(w http.ResponseWriter, r *http.Request) { send(w, 200, settingsFixture) })
	m.HandleFunc("PUT /api/discovery/settings", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		send(w, 200, string(raw))
	})
	m.HandleFunc("PUT /api/discovery/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		gone := f.sourceGone
		f.mu.Unlock()
		if gone {
			send(w, 404, `{"error":"Source not found."}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		send(w, 200, string(raw))
	})
	m.HandleFunc("POST /api/jobs/find", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, `{"scored":7,"summary":`+summaryFixture+`}`)
	})
	m.HandleFunc("GET /api/catalog/summary", func(w http.ResponseWriter, r *http.Request) { send(w, 200, summaryFixture) })
}

func appRequests(t *testing.T, f *fakeAPI, want ...appRequest) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(f.appRequests, want) {
		t.Fatalf("requests: got %#v, want %#v", f.appRequests, want)
	}
}

func TestBlacklistTools(t *testing.T) {
	cs, f := session(t)
	res, text := call(t, cs, "list_blacklist", nil)
	if res.IsError || !strings.Contains(text, "excluded.test") || !strings.Contains(text, "User choice") {
		t.Fatal(text)
	}
	args := map[string]any{"company_name": "Excluded Ltd", "company_domain": "excluded.test", "reason": "User choice"}
	res, text = call(t, cs, "add_to_blacklist", args)
	if res.IsError || !strings.Contains(text, "b-new") {
		t.Fatal(text)
	}
	appRequests(t, f, appRequest{"GET /api/blacklist", nil}, appRequest{"POST /api/blacklist", args})
}

func TestDiscoverySettingsTools(t *testing.T) {
	cs, f := session(t)
	res, text := call(t, cs, "get_discovery_settings", nil)
	if res.IsError || !strings.Contains(text, "720") {
		t.Fatal(text)
	}
	res, text = call(t, cs, "set_discovery_settings", map[string]any{"enabled": false, "auto_prepare": false, "min_score": 0})
	if res.IsError {
		t.Fatal(text)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(settingsFixture), &want)
	want["enabled"], want["auto_prepare"], want["min_score"] = false, false, float64(0)
	appRequests(t, f, appRequest{"GET /api/discovery/settings", nil}, appRequest{"GET /api/discovery/settings", nil}, appRequest{"PUT /api/discovery/settings", want})
	if got := structured[map[string]any](t, res); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings lost: %s", text)
	}
}

func TestUpdateDiscoverySourceMerges(t *testing.T) {
	for key, value := range map[string]any{"active": true, "name": "Renamed", "search_query": "", "excluded_search_query": ""} {
		t.Run(key, func(t *testing.T) {
			cs, f := session(t)
			f.mu.Lock()
			f.appSource = `{"id":"s-1","provider":"greenhouse","name":"Board","identifier":"board-token","search_query":"Go","excluded_search_query":"Sales","active":false,"last_error":"old error","last_crawled_at":"2026-09-11T00:00:00Z"}`
			f.mu.Unlock()
			args := map[string]any{"id": "s-1"}
			want := map[string]any{"provider": "greenhouse", "name": "Board", "identifier": "board-token", "search_query": "Go", "excluded_search_query": "Sales", "active": false}
			args[key], want[key] = value, value
			res, text := call(t, cs, "update_discovery_source", args)
			if res.IsError {
				t.Fatal(text)
			}
			appRequests(t, f, appRequest{"GET /api/discovery/sources", nil}, appRequest{"PUT /api/discovery/sources/s-1", want})
		})
	}
}

func TestUpdateDiscoverySourcePauseAndRemotiveValidation(t *testing.T) {
	cs, f := session(t)
	res, text := call(t, cs, "update_discovery_source", map[string]any{"id": "s-1", "active": false})
	if res.IsError {
		t.Fatal(text)
	}
	want := map[string]any{"provider": "remotive", "name": "Remote Go", "identifier": "", "search_query": "go backend", "excluded_search_query": "", "active": false}
	appRequests(t, f, appRequest{"GET /api/discovery/sources", nil}, appRequest{"PUT /api/discovery/sources/s-1", want})
	cs, f = session(t)
	res, text = call(t, cs, "update_discovery_source", map[string]any{"id": "s-1", "search_query": " "})
	if !res.IsError || !strings.Contains(text, "focused search query") {
		t.Fatal(text)
	}
	appRequests(t, f, appRequest{"GET /api/discovery/sources", nil})
}

func TestAppToolsWriteForbidden(t *testing.T) {
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"add_to_blacklist", map[string]any{"company_name": "Example"}},
		{"set_discovery_settings", map[string]any{"enabled": false}},
		{"update_discovery_source", map[string]any{"id": "s-1", "active": false}},
		{"find_jobs_for_profile", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			cs, f := session(t)
			f.mu.Lock()
			f.appWriteDenied = true
			f.mu.Unlock()
			res, text := call(t, cs, c.name, c.args)
			if !res.IsError || !strings.Contains(text, "403") || !strings.Contains(text, "Policy fixture W403") {
				t.Fatalf("write denial: %s", text)
			}
			if !strings.Contains(text, "administrator setting") || strings.Contains(text, "create a token") || strings.Contains(text, "-Write") {
				t.Fatalf("account policy must not suggest minting a write token: %s", text)
			}
		})
	}
}

func TestDiscoverySettingsChangesNumericLimits(t *testing.T) {
	cs, f := session(t)
	res, text := call(t, cs, "set_discovery_settings", map[string]any{"interval_minutes": 360, "max_jobs_per_run": 500, "min_score": 100})
	if res.IsError {
		t.Fatal(text)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(settingsFixture), &want)
	want["interval_minutes"], want["max_jobs_per_run"], want["min_score"] = float64(360), float64(500), float64(100)
	appRequests(t, f, appRequest{"GET /api/discovery/settings", nil}, appRequest{"PUT /api/discovery/settings", want})
}

func TestAppToolsInvalidBeforeAnyRequest(t *testing.T) {
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"add_to_blacklist", map[string]any{"company_name": " "}},
		{"set_discovery_settings", map[string]any{"interval_minutes": 359}},
		{"set_discovery_settings", map[string]any{"min_score": -1}},
		{"set_discovery_settings", map[string]any{"min_score": 101}},
		{"set_discovery_settings", map[string]any{"max_jobs_per_run": 0}},
		{"set_discovery_settings", map[string]any{"max_jobs_per_run": 501}},
		{"update_discovery_source", map[string]any{"id": " "}},
		{"update_discovery_source", map[string]any{"id": "s-1", "name": " "}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cs, f := session(t)
			res, text := call(t, cs, c.name, c.args)
			if !res.IsError {
				t.Fatalf("invalid input succeeded: %s", text)
			}
			appRequests(t, f)
		})
	}
}

func TestAppToolsMemberForbidden(t *testing.T) {
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"get_discovery_settings", nil}, {"set_discovery_settings", map[string]any{"enabled": false}}, {"update_discovery_source", map[string]any{"id": "s-1", "active": false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cs, f := session(t)
			f.mu.Lock()
			f.member = true
			f.mu.Unlock()
			res, text := call(t, cs, c.name, c.args)
			if !res.IsError || !strings.Contains(text, "403") || !strings.Contains(text, "Discovery access denied by fixture M403") {
				t.Fatalf("member error: %s", text)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.appRequests) != 1 || !strings.HasPrefix(f.appRequests[0].Route, "GET ") {
				t.Fatalf("must stop after denied read: %#v", f.appRequests)
			}
		})
	}
}

func TestUpdateDiscoverySourceNotFound(t *testing.T) {
	for _, id := range []string{"unknown", "s-1"} {
		cs, f := session(t)
		f.mu.Lock()
		f.sourceGone = true
		f.mu.Unlock()
		res, text := call(t, cs, "update_discovery_source", map[string]any{"id": id, "active": false})
		if !res.IsError || !strings.Contains(text, "404") || !strings.Contains(strings.ToLower(text), "source not found") {
			t.Fatalf("missing source: %s", text)
		}
		if id == "unknown" {
			appRequests(t, f, appRequest{"GET /api/discovery/sources", nil})
		} else {
			want := map[string]any{"provider": "remotive", "name": "Remote Go", "identifier": "", "search_query": "go backend", "excluded_search_query": "", "active": false}
			appRequests(t, f, appRequest{"GET /api/discovery/sources", nil}, appRequest{"PUT /api/discovery/sources/s-1", want})
		}
	}
}

func TestFindJobsAndCatalogSummary(t *testing.T) {
	cs, f := session(t)
	f.mu.Lock()
	f.member = true
	f.mu.Unlock()
	res, text := call(t, cs, "find_jobs_for_profile", nil)
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[map[string]any](t, res)
	if got["scored"] != float64(7) || got["summary"].(map[string]any)["open_matches"] != float64(3) {
		t.Fatal(text)
	}
	res, text = call(t, cs, "get_catalog_summary", nil)
	if res.IsError || !strings.Contains(text, "last_job_at") {
		t.Fatal(text)
	}
	appRequests(t, f, appRequest{"POST /api/jobs/find", nil}, appRequest{"GET /api/catalog/summary", nil})
}

func TestUpdateDiscoverySourceRemotiveExclusion(t *testing.T) {
	cs, f := session(t)
	res, text := call(t, cs, "update_discovery_source", map[string]any{"id": "s-1", "excluded_search_query": "Sales"})
	if res.IsError {
		t.Fatal(text)
	}
	want := map[string]any{"provider": "remotive", "name": "Remote Go", "identifier": "", "search_query": "go backend", "excluded_search_query": "", "active": true}
	appRequests(t, f, appRequest{"GET /api/discovery/sources", nil}, appRequest{"PUT /api/discovery/sources/s-1", want})
	if got := structured[DiscoverySource](t, res); got.Provider != "remotive" || got.ExcludedSearchQuery != "" {
		t.Fatalf("remotive exclusion must be cleared: %s", text)
	}
	if !strings.Contains(text, "excluded_search_query does not apply to remotive") {
		t.Fatalf("must explain ignored exclusion: %s", text)
	}
}
