package jobpilot

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func (f *fakeAPI) repairHandlers(m *http.ServeMux) {
	m.HandleFunc("POST /api/profile/repairs", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		d := json.NewDecoder(r.Body)
		if d.Decode(&in) != nil || len(in) != 1 || in["source_cv_id"] != "cv-synthetic" {
			http.Error(w, "strict source payload", 400)
			return
		}
		_, _ = w.Write([]byte(repairRecordJSON))
	})
	m.HandleFunc("GET /api/profile/repairs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"repair-1","status":"pending"}]`))
	})
	m.HandleFunc("GET /api/profile/repairs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "repair-1" {
			http.Error(w, `{"error":"Record not found."}`, 404)
			return
		}
		_, _ = w.Write([]byte(repairRecordJSON))
	})
}

// The API's record carries the CV text and whole snapshots; the tools must not.
const repairRecordJSON = `{"id":"repair-1","status":"pending","created_at":"2026-11-10T10:00:00Z",
"source":{"id":"cv-synthetic","filename":"synthetic.pdf","label":"Synthetic base","extracted_text":"SNAPSHOT-ONLY cv text","parsed":{"text":"SNAPSHOT-ONLY parsed text"}},
"source_sha256":"synthetic-hash","before_fingerprint":"fp","warnings":["Role 2 has unknown dates; review before applying."],
"before":{"profile":{"summary":"SNAPSHOT-ONLY before","salary_expectation":98765},"blacklist":[]},
"after":{"profile":{"summary":"SNAPSHOT-ONLY after"},"blacklist":[]},
"changes":[
 {"field":"blacklist","before":[],"after":[{"id":"b1","company_name":"Old Employer","reason":"Employer exclusion retained by profile repair"}]},
 {"field":"experiences","before":[{"id":"e1","company":"Old Employer","title":"Wrong title"}],"after":[{"id":"e2","company":"Source Employer","title":"Engineer","start_date":"2021-03","current":true}]},
 {"field":"languages","before":[],"after":["English","German"]},
 {"field":"skills","before":[{"id":"s1","name":"ubernete","years":0},{"id":"s2","name":"Go","years":0}],"after":[{"id":"s3","name":"Go","years":0},{"id":"s4","name":"Helm","years":3}]},
 {"field":"summary","before":"Scrambled","after":"Private synthetic source"}]}`

func TestProfileRepairView(t *testing.T) {
	v, err := viewRepair(json.RawMessage(repairRecordJSON))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	got := string(raw)
	for _, want := range []string{
		`"review_at":"/profile/repairs"`, `"sha256":"synthetic-hash"`, `"label":"Synthetic base"`,
		`"field":"skills","before_count":2,"after_count":2,"removed":["ubernete"],"added":["Helm (3 years)"]`,
		`"removed":["Wrong title at Old Employer"],"added":["Engineer at Source Employer (2021-03 to present)"]`,
		`"field":"blacklist","before_count":0,"after_count":1,"added":["Old Employer"]`,
		`"field":"languages","before":[],"after":["English","German"]`,
		`"field":"summary","before":"Scrambled","after":"Private synthetic source"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("view lacks %s in %s", want, got)
		}
	}
	if strings.Contains(got, "SNAPSHOT-ONLY") || strings.Contains(got, "98765") {
		t.Errorf("view carries snapshots or CV text: %s", got)
	}
	long, err := viewRepair(json.RawMessage(`{"id":"r","changes":[{"field":"summary","before":"","after":"` + strings.Repeat("é", 5000) + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := long.Changes[0].After.(string); len([]rune(after)) > repairTextLimit+60 || !strings.Contains(after, "review page") {
		t.Errorf("long text not cut: %d runes", len([]rune(after)))
	}
	if _, err = viewRepair(json.RawMessage(`[]`)); err == nil {
		t.Error("an unreadable record must be an error")
	}
}

func TestProfileRepairTools(t *testing.T) {
	var logs bytes.Buffer
	cs, _ := sessionWithLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
	if r, text := call(t, cs, "propose_profile_repair", map[string]any{"source_cv_id": ""}); !r.IsError || !strings.Contains(text, "required") {
		t.Fatal(text)
	}
	if r, text := call(t, cs, "propose_profile_repair", map[string]any{"source_cv_id": "cv-synthetic"}); r.IsError || !strings.Contains(text, "Private synthetic source") || !strings.Contains(text, "Role 2 has unknown dates") || strings.Contains(text, "SNAPSHOT-ONLY") {
		t.Fatal(text)
	}
	if r, text := call(t, cs, "get_profile_repairs", nil); r.IsError || !strings.Contains(text, "repair-1") {
		t.Fatal(text)
	}
	if r, text := call(t, cs, "get_profile_repairs", map[string]any{"proposal_id": "repair-1"}); r.IsError || !strings.Contains(text, "synthetic-hash") || !strings.Contains(text, "ubernete") || strings.Contains(text, "SNAPSHOT-ONLY") {
		t.Fatal(text)
	}
	if r, _ := call(t, cs, "get_profile_repairs", map[string]any{"proposal_id": "missing"}); !r.IsError {
		t.Fatal("missing proposal succeeded")
	}
	catalog, e := cs.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range catalog.Tools {
		if strings.Contains(tool.Name, "profile_repair") && tool.Name != "propose_profile_repair" && tool.Name != "get_profile_repairs" {
			t.Fatal("unexpected profile action", tool.Name)
		}
		if tool.Name == "get_profile_repairs" && !tool.Annotations.ReadOnlyHint {
			t.Fatal("read marked write")
		}
		if tool.Name == "propose_profile_repair" && tool.Annotations.ReadOnlyHint {
			t.Fatal("proposal marked read")
		}
	}
	if strings.Contains(logs.String(), "Private synthetic") || strings.Contains(logs.String(), "cv-synthetic") || strings.Contains(logs.String(), "synthetic-hash") {
		t.Fatal("profile facts in logs")
	}
}
