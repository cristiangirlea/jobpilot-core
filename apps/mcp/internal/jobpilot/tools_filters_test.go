package jobpilot

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSetFiltersEligibilityLanguageRules(t *testing.T) {
	cs, fake := session(t)
	for _, enabled := range []bool{true, false} {
		res, message := call(t, cs, "set_filters", map[string]any{
			"name": "Eligibility", "reject_eligibility_requirements": enabled, "reject_unlisted_languages": enabled,
		})
		if res.IsError {
			t.Fatal(message)
		}
		_, bodies := fake.savedFilters()
		for _, key := range []string{"reject_eligibility_requirements", "reject_unlisted_languages"} {
			if bodies[len(bodies)-1][key] != enabled {
				t.Fatalf("%s: %v", key, bodies)
			}
		}
	}
	var base FilterPreset
	if err := json.Unmarshal([]byte(`{"name":"Stored","reject_eligibility_requirements":true,"reject_unlisted_languages":true}`), &base); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{}`, `{"reject_eligibility_requirements":false}`, `{"reject_unlisted_languages":false}`} {
		var in setFiltersInput
		if err := json.Unmarshal([]byte(input), &in); err != nil {
			t.Fatal(err)
		}
		got, err := applyFilterChanges(base, in)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(got)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		for _, key := range []string{"reject_eligibility_requirements", "reject_unlisted_languages"} {
			want := !strings.Contains(input, key)
			if fields[key] != want {
				t.Fatalf("input=%s %s=%v want %v", input, key, fields[key], want)
			}
		}
	}
	res, message := call(t, cs, "set_filters", map[string]any{"name": "Default rules"})
	if res.IsError {
		t.Fatal(message)
	}
	_, bodies := fake.savedFilters()
	for _, key := range []string{"reject_eligibility_requirements", "reject_unlisted_languages"} {
		if bodies[len(bodies)-1][key] != false {
			t.Fatalf("%s must default off", key)
		}
	}
}

func (f *fakeAPI) savedFilters() ([]string, []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.filterSaves...), append([]map[string]any(nil), f.filterBodies...)
}

func TestSetFiltersCreatesAPreset(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "set_filters", map[string]any{
		"name": " Go only ", "active": true, "included_technologies": []string{"Go", " ", "Rust "},
		"remote_types": []string{"Remote", "hybrid"}, "currency": "eur", "max_age_days": 14,
	})
	if res.IsError {
		t.Fatal(text)
	}
	got := structured[FilterPreset](t, res)
	if got.ID != "f-new" || !got.Active || got.Name != "Go only" {
		t.Fatalf("unexpected preset: %s", text)
	}
	saves, bodies := fake.savedFilters()
	if len(saves) != 1 || saves[0] != "POST /api/filters" {
		t.Fatalf("a preset without an id is created, got %v", saves)
	}
	body := bodies[0]
	if techs, _ := body["included_technologies"].([]any); len(techs) != 2 || techs[1] != "Rust" {
		t.Fatalf("terms are trimmed and blanks dropped: %v", body["included_technologies"])
	}
	if remote, _ := body["remote_types"].([]any); len(remote) != 2 || remote[0] != "remote" {
		t.Fatalf("remote types are lowercased: %v", body["remote_types"])
	}
	if body["currency"] != "EUR" || body["max_age_days"] != float64(14) {
		t.Fatalf("scalars must reach the API: %v", body)
	}
	// The API stores what it is sent, so a rule nobody named is an empty
	// list, never null, and never a value made up here.
	for _, key := range []string{"included_titles", "excluded_title_words", "locations", "employment_types", "excluded_industries", "required_skills", "excluded_technologies"} {
		if list, ok := body[key].([]any); !ok || len(list) != 0 {
			t.Errorf("%s should be an empty list, got %v", key, body[key])
		}
	}
	if _, has := body["id"]; has {
		t.Fatalf("a new preset must not send an id: %v", body)
	}
}

func TestSetFiltersChangesOnlyWhatItNames(t *testing.T) {
	cs, fake := session(t)
	res, text := call(t, cs, "set_filters", map[string]any{
		"id": "f-1", "excluded_technologies": []string{"PHP", "Java"}, "included_titles": []string{},
	})
	if res.IsError {
		t.Fatal(text)
	}
	saves, bodies := fake.savedFilters()
	if len(saves) != 1 || saves[0] != "PUT /api/filters/f-1" {
		t.Fatalf("a preset with an id is replaced in place, got %v", saves)
	}
	body := bodies[0]
	if techs, _ := body["excluded_technologies"].([]any); len(techs) != 2 {
		t.Fatalf("the named change must be sent: %v", body["excluded_technologies"])
	}
	if titles, ok := body["included_titles"].([]any); !ok || len(titles) != 0 {
		t.Fatalf("an empty list clears the rule and is sent as one: %v", body["included_titles"])
	}
	// Everything else is the stored preset, unchanged.
	if body["name"] != "Backend remote" || body["active"] != true || body["min_salary"] != float64(90000) || body["max_age_days"] != float64(30) {
		t.Fatalf("unnamed fields must keep their stored value: %v", body)
	}
	if locations, _ := body["locations"].([]any); len(locations) != 1 || locations[0] != "Romania" {
		t.Fatalf("unnamed lists must keep their stored value: %v", body["locations"])
	}
}

func TestSetFiltersActivatesAndDeactivates(t *testing.T) {
	cs, fake := session(t)
	for _, active := range []bool{false, true} {
		res, text := call(t, cs, "set_filters", map[string]any{"id": "f-1", "active": active})
		if res.IsError {
			t.Fatal(text)
		}
		if got := structured[FilterPreset](t, res); got.Active != active {
			t.Fatalf("active %v was not applied: %s", active, text)
		}
	}
	if saves, _ := fake.savedFilters(); len(saves) != 2 {
		t.Fatalf("each change is one save, got %v", saves)
	}
}

func TestSetFiltersExplainsInvalidInput(t *testing.T) {
	cs, fake := session(t)
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no name on create", map[string]any{"active": true}, "needs a name"},
		{"blank name on update", map[string]any{"id": "f-1", "name": "  "}, "needs a name"},
		{"negative age", map[string]any{"name": "Old", "max_age_days": -1}, "cannot be negative"},
		{"negative salary", map[string]any{"id": "f-1", "min_salary": -5}, "cannot be negative"},
		{"unknown remote type", map[string]any{"name": "Anywhere", "remote_types": []string{"anywhere"}}, "remote, hybrid, onsite"},
		{"someone else's id", map[string]any{"id": "f-other", "active": true}, "no filter preset has id"},
	} {
		res, text := call(t, cs, "set_filters", c.args)
		if !res.IsError || !strings.Contains(text, c.want) {
			t.Errorf("%s: expected an error mentioning %q, got %s", c.name, c.want, text)
		}
	}
	if saves, _ := fake.savedFilters(); len(saves) != 0 {
		t.Fatalf("invalid input must never be saved, got %v", saves)
	}

	// The API's own refusal comes back as a tool error with its reason.
	res, text := call(t, cs, "set_filters", map[string]any{"name": "Refused"})
	if !res.IsError || !strings.Contains(text, "numeric limits cannot be negative") {
		t.Fatalf("the API's reason must reach the agent: %s", text)
	}
}

// The overlay itself, field by field: a missing value keeps the stored one, an
// empty list or a zero clears it, and a remote type saved as free text in the
// web app survives an unrelated change but is refused when sent.
func TestApplyFilterChanges(t *testing.T) {
	stored := FilterPreset{ID: "f-1", Name: "Stored", Active: true, IncludedTitles: []string{"Backend"}, ExcludedTitleWords: []string{"unpaid"},
		Locations: []string{"Romania"}, RemoteTypes: []string{"Remote-first"}, MinSalary: 90000, Currency: "EUR",
		EmploymentTypes: []string{"full-time"}, ExcludedIndustries: []string{"Gambling"}, RequiredSkills: []string{"Go"},
		IncludedTechnologies: []string{"Go"}, ExcludedTechnologies: []string{"PHP"}, MaxAgeDays: 30}
	clone := func() FilterPreset {
		c := stored
		for _, l := range []*[]string{&c.IncludedTitles, &c.ExcludedTitleWords, &c.Locations, &c.RemoteTypes, &c.EmploymentTypes, &c.ExcludedIndustries, &c.RequiredSkills, &c.IncludedTechnologies, &c.ExcludedTechnologies} {
			*l = append([]string(nil), (*l)...)
		}
		return c
	}

	name := "Renamed"
	got, err := applyFilterChanges(clone(), setFiltersInput{Name: &name})
	if err != nil {
		t.Fatalf("free text stored by the web app must not block an unrelated change: %v", err)
	}
	want := clone()
	want.Name = "Renamed"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("only the name should change: got %+v, want %+v", got, want)
	}

	zero, zeroDays, noCurrency, inactive := 0.0, 0, "", false
	empty := []string{}
	got, err = applyFilterChanges(clone(), setFiltersInput{Active: &inactive, MinSalary: &zero, MaxAgeDays: &zeroDays, Currency: &noCurrency,
		IncludedTitles: empty, ExcludedTitleWords: empty, Locations: empty, RemoteTypes: empty, EmploymentTypes: empty,
		ExcludedIndustries: empty, RequiredSkills: empty, IncludedTechnologies: empty, ExcludedTechnologies: empty})
	if err != nil {
		t.Fatal(err)
	}
	cleared := FilterPreset{ID: "f-1", Name: "Stored", IncludedTitles: []string{}, ExcludedTitleWords: []string{}, Locations: []string{},
		RemoteTypes: []string{}, EmploymentTypes: []string{}, ExcludedIndustries: []string{}, RequiredSkills: []string{},
		IncludedTechnologies: []string{}, ExcludedTechnologies: []string{}}
	if !reflect.DeepEqual(got, cleared) {
		t.Fatalf("every rule should be cleared: got %+v, want %+v", got, cleared)
	}

	if _, err = applyFilterChanges(clone(), setFiltersInput{RemoteTypes: []string{"Remote-first"}}); err == nil || !strings.Contains(err.Error(), "remote, hybrid, onsite") {
		t.Fatalf("a free-text remote type that is sent must be refused, got %v", err)
	}
}
