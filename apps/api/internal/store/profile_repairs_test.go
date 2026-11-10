package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	database "jobpilot/core/api/internal/db"
	"jobpilot/core/api/internal/models"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRepairCandidateKeepsAbsentFactsAndLogistics(t *testing.T) {
	before := ProfileSnapshot{Profile: models.Profile{Name: "Before", GivenName: "Explicit", YearsExperience: 7, SalaryExpectation: 90000, RateExpectation: 400, Currency: "EUR", NoticePeriod: "30 days", WorkAuthorization: "Explicit", Experiences: []models.WorkExperience{{Company: "Old", Title: "Merged"}}, Skills: []models.Skill{{Name: "Old"}}}}
	source := models.CVDocument{Kind: "base", Parsed: map[string]any{"summary": "Source summary", "years_experience": 0, "warnings": []string{"Source warning"}}}
	after, w, e := repairCandidate(before, source)
	if e != nil || after.Profile.Name != "Before" || after.Profile.YearsExperience != 7 || after.Profile.GivenName != "Explicit" || after.Profile.SalaryExpectation != 90000 || after.Profile.RateExpectation != 400 || after.Profile.NoticePeriod != "30 days" || after.Profile.WorkAuthorization != "Explicit" || len(after.Profile.Experiences) != 1 || len(after.Profile.Skills) != 1 || len(w) != 3 {
		t.Fatalf("missing facts/logistics changed: %v", e)
	}
	if before.Profile.Summary != "" {
		t.Fatal("mutated before snapshot")
	}
	// The parser reports unknown overall years as zero; only a positive value replaces.
	source.Parsed = map[string]any{"years_experience": 12}
	after, _, e = repairCandidate(before, source)
	if e != nil || after.Profile.YearsExperience != 12 || before.Profile.YearsExperience != 7 {
		t.Fatalf("parsed years not proposed: %v", e)
	}
	source.Parsed = map[string]any{"experiences": []models.WorkExperience{{Company: "New", Title: "Engineer"}}}
	_, w, e = repairCandidate(before, source)
	if e != nil || !strings.Contains(strings.Join(w, " "), "unknown dates") {
		t.Fatal("unknown dates hidden")
	}

	for _, facts := range []map[string]any{{"experiences": make([]models.WorkExperience, 201)}, {"skills": make([]models.Skill, 501)}} {
		if _, _, e = repairCandidate(before, models.CVDocument{Kind: "base", Parsed: facts}); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("oversized source accepted")
		}
	}
	for _, doc := range []models.CVDocument{{Kind: "tailored", Parsed: source.Parsed}, {Kind: "base"}, {Kind: "base", Parsed: map[string]any{"skills": []models.Skill{{Name: "Go"}, {Name: "Go"}}}}} {
		if _, _, e = repairCandidate(before, doc); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("invalid source accepted")
		}
	}
	// A role whose employer the parser could not tell is kept, and said.
	source.Parsed = map[string]any{"experiences": []models.WorkExperience{{Title: "Contract engineer", StartDate: "2020", EndDate: "2021"}}}
	after, w, e = repairCandidate(before, source)
	if e != nil || len(after.Profile.Experiences) != 1 || !strings.Contains(strings.Join(w, " "), "Role 1 has no employer") {
		t.Fatalf("role without employer: %v %v", w, e)
	}
	// Each refusal says what is wrong, and all of them match ErrRepairInvalid.
	for want, doc := range map[string]models.CVDocument{
		"Role 2 of the CV's stored parse has no title": {Kind: "base", Parsed: map[string]any{"experiences": []models.WorkExperience{{Company: "A", Title: "Engineer"}, {Company: "B"}}}},
		"Skill 2 of the CV's stored parse":             {Kind: "base", Parsed: map[string]any{"skills": []models.Skill{{Name: "Go"}, {Name: " "}}}},
		"Only a stored base CV":                        {Kind: "tailored", Parsed: map[string]any{"summary": "Synthetic"}},
		"no facts to repair from":                      {Kind: "base", Parsed: map[string]any{"warnings": []string{"Nothing found"}}},
		"at most 200 roles and 500 skills":             {Kind: "base", Parsed: map[string]any{"skills": make([]models.Skill, 501)}},
	} {
		if _, _, e = repairCandidate(before, doc); !errors.Is(e, ErrRepairInvalid) || !strings.Contains(e.Error(), want) {
			t.Fatalf("refusal %q: %v", want, e)
		}
	}
}

// An employer that leaves the role list stays excluded, once, however it was known.
func TestRetainedExclusions(t *testing.T) {
	role := func(company, domain string) models.WorkExperience {
		return models.WorkExperience{Company: company, CompanyDomain: domain, Title: "Engineer"}
	}
	before := ProfileSnapshot{Profile: models.Profile{Experiences: []models.WorkExperience{
		role("Leaving Co", "https://www.Leaving.example/jobs"),
		role("leaving co.", "leaving.example"), // the same employer written differently
		role("Staying Co", "staying.example"),
		role("Lost Domain Co", "lost.example"),
		role("Listed Co", ""),
		role("", "domain-only.example"),
		role("", "staying.example"), // its domain is still a role's
		role("", ""),
	}}}
	after := ProfileSnapshot{
		Profile:   models.Profile{Experiences: []models.WorkExperience{role("Staying Co", "staying.example"), role("Lost Domain Co", "")}},
		Blacklist: []models.BlacklistEntry{{CompanyName: "Listed Co", NormalizedName: "listed co", Reason: "User exclusion"}},
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := retainedExclusions(before, after, now)
	want := [][2]string{{"Leaving Co", "leaving.example"}, {"Lost Domain Co", "lost.example"}, {"domain-only.example", "domain-only.example"}}
	if len(got) != len(want) {
		t.Fatalf("retained %d exclusions, want %d: %+v", len(got), len(want), got)
	}
	seen := map[string]bool{}
	for i, x := range got {
		if x.CompanyName != want[i][0] || x.CompanyDomain != want[i][1] || x.NormalizedName == "" || x.Reason == "" || !x.CreatedAt.Equal(now) {
			t.Errorf("exclusion %d = %+v, want %v", i, x, want[i])
		}
		// The table allows one entry per normalised name and domain.
		if k := x.NormalizedName + "|" + x.CompanyDomain; seen[k] {
			t.Errorf("duplicate exclusion %s", k)
		} else {
			seen[k] = true
		}
	}
	if again := retainedExclusions(ProfileSnapshot{Profile: before.Profile}, ProfileSnapshot{Profile: after.Profile, Blacklist: append(after.Blacklist, got...)}, now); len(again) != 0 {
		t.Errorf("already retained exclusions were added again: %+v", again)
	}
}

func TestSourceFingerprintIgnoresApprovalAndLabel(t *testing.T) {
	now := time.Now()
	doc := models.CVDocument{ID: "cv", Kind: "base", Filename: "synthetic.pdf", Format: "pdf", ExtractedText: "Text", Parsed: map[string]any{"summary": "Synthetic"}}
	same := doc
	same.Approved, same.ApprovedAt, same.UpdatedAt, same.Label, same.RoleFamily = true, &now, now, "Renamed", "platform"
	if sourceFingerprint(doc) != sourceFingerprint(same) {
		t.Fatal("approving or relabelling the CV changed the source fingerprint")
	}
	for name, change := range map[string]func(*models.CVDocument){
		"text":   func(d *models.CVDocument) { d.ExtractedText = "Other text" },
		"parse":  func(d *models.CVDocument) { d.Parsed = map[string]any{"summary": "Other"} },
		"kind":   func(d *models.CVDocument) { d.Kind = "tailored" },
		"record": func(d *models.CVDocument) { d.ID = "other" },
	} {
		changed := doc
		change(&changed)
		if sourceFingerprint(doc) == sourceFingerprint(changed) {
			t.Errorf("a changed %s kept the source fingerprint", name)
		}
	}
}

func TestProfileRepairsPostgres(t *testing.T) {
	dsn := os.Getenv("JOBPILOT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires migrated throwaway database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, e := database.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	s := New(pool)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	u, e := s.CreateUser(ctx, fmt.Sprintf("repair-%d@example.test", time.Now().UnixNano()), "fake")
	must(e)
	other, e := s.CreateUser(ctx, fmt.Sprintf("repair-other-%d@example.test", time.Now().UnixNano()), "fake")
	must(e)
	must(s.SaveProfile(ctx, u.ID, models.Profile{Name: "Old", Summary: "Scrambled", SalaryExpectation: 98765, RateExpectation: 456, Currency: "EUR", NoticePeriod: "User supplied", WorkAuthorization: "User supplied"}))
	_, e = s.AddExperience(ctx, u.ID, models.WorkExperience{Company: "Merged Old Co", CompanyDomain: "old.example.test", Title: "Wrong"})
	must(e)
	_, e = s.AddSkill(ctx, u.ID, models.Skill{Name: "Bogus", Years: 99})
	must(e)
	_, e = s.AddBlacklist(ctx, u.ID, models.BlacklistEntry{CompanyName: "Explicit No", Reason: "User exclusion"})
	must(e)
	_, e = s.AddProject(ctx, u.ID, models.Project{Name: "Keep project", Skills: []string{"Go"}})
	must(e)
	doc, e := s.SaveCV(ctx, u.ID, models.CVDocument{Kind: "base", Filename: "synthetic.txt", Format: "text", ExtractedText: "Synthetic source facts", Parsed: map[string]any{"name": "Source Name", "text": "Synthetic source facts", "summary": "Accurate summary", "skills": []models.Skill{{Name: "Go"}}, "experiences": []models.WorkExperience{{Company: "Source Co", Title: "Engineer", Current: true}}, "warnings": []string{"Synthetic parse warning"}}}, []byte("Synthetic source bytes"))
	must(e)
	get := func() ProfileSnapshot {
		t.Helper()
		ctx, tx, cleanup, e := repairTx(ctx, s, u.ID)
		must(e)
		defer cleanup()
		v, e := snapshot(ctx, tx, u.ID)
		must(e)
		return v
	}
	before := get()
	propose := func() ProfileRepair {
		t.Helper()
		r, e := s.ProposeProfileRepair(ctx, u.ID, doc.ID, RepairByBrowser)
		must(e)
		return r
	}
	r := propose()
	if repairHash(get()) != repairHash(before) || r.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("Synthetic source bytes"))) || len(r.After.Blacklist) != 2 || len(r.Warnings) < 2 {
		t.Fatal("proposal mutated profile or missed provenance/exclusions/warnings")
	}
	if _, e = s.ProfileRepair(ctx, other.ID, r.ID); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cross-tenant read")
	}
	if _, e = s.DecideProfileRepair(ctx, other.ID, r.ID, "apply", r.BeforeFingerprint); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cross-tenant decision")
	}
	if _, e = s.ProposeProfileRepair(ctx, other.ID, doc.ID, RepairByBrowser); !errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("cross-tenant source")
	}
	if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", "wrong"); !errors.Is(e, ErrRepairConflict) {
		t.Fatal("stale browser accepted")
	}
	applied, e := s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
	must(e)
	actual := get()
	if len(actual.Profile.Experiences) != 1 || actual.Profile.Experiences[0].Company != "Source Co" || len(actual.Profile.Skills) != 1 || actual.Profile.Skills[0].Name != "Go" || actual.Profile.SalaryExpectation != 98765 || actual.Profile.RateExpectation != 456 || len(actual.Profile.Projects) != 1 || len(actual.Blacklist) != 2 || applied.Status != "applied" {
		t.Fatal("incorrect atomic replacement")
	}
	unchanged, e := s.CV(ctx, u.ID, doc.ID)
	must(e)
	if unchanged.Approved || unchanged.ExtractedText != "Synthetic source facts" {
		t.Fatal("source changed/approved")
	}
	restored, e := s.DecideProfileRepair(ctx, u.ID, r.ID, "restore", applied.AppliedFingerprint)
	must(e)
	if restored.Status != "restored" || repairHash(get()) != repairHash(before) {
		t.Fatal("full snapshot restore differs")
	}
	retained, e := s.ProfileRepair(ctx, u.ID, r.ID)
	must(e)
	if len(retained.Before.Profile.Skills) != 1 || retained.Source.Parsed["name"] != "Source Name" || retained.ProposedBy != RepairByBrowser {
		t.Fatal("snapshot/source lost")
	}
	// The record keeps the parsed facts, never the CV's text, in any copy.
	var stored string
	must(pool.QueryRow(ctx, `SELECT proposal::text FROM profile_repairs WHERE id=$1`, r.ID).Scan(&stored))
	if retained.Source.ExtractedText != "" || strings.Contains(stored, "Synthetic source facts") || strings.Contains(r.Source.ExtractedText, "Synthetic") {
		t.Fatal("the CV text was kept in the repair record")
	}
	list, e := s.ProfileRepairs(ctx, u.ID)
	must(e)
	if len(list) != 1 || list[0].ID != r.ID || list[0].Status != "restored" || list[0].ProposedBy != RepairByBrowser {
		t.Fatalf("summaries: %+v", list)
	}
	if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "restore", applied.AppliedFingerprint); !errors.Is(e, ErrRepairConflict) {
		t.Fatal("restore replay")
	}

	t.Run("proposal size and pending limits", func(t *testing.T) {
		large, e := s.SaveCV(ctx, other.ID, models.CVDocument{Kind: "base", Filename: "large-synthetic.txt", Format: "text", ExtractedText: strings.Repeat("x", 2<<20), Parsed: map[string]any{"summary": "Synthetic"}}, []byte("Synthetic bytes"))
		must(e)
		if _, e = s.ProposeProfileRepair(ctx, other.ID, large.ID, RepairByBrowser); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("oversized proposal accepted")
		}
		small, e := s.SaveCV(ctx, other.ID, models.CVDocument{Kind: "base", Filename: "small-synthetic.txt", Format: "text", Parsed: map[string]any{"summary": "Synthetic"}}, []byte("Synthetic bytes"))
		must(e)
		_, e = pool.Exec(ctx, `INSERT INTO profile_repairs(user_id,proposal) SELECT $1,'{}'::jsonb FROM generate_series(1,20)`, other.ID)
		must(e)
		if _, e = s.ProposeProfileRepair(ctx, other.ID, small.ID, RepairByBrowser); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("pending limit ignored")
		}
		// Exhausted pending quota rejects before looking up/transferring a source.
		if _, e = s.ProposeProfileRepair(ctx, other.ID, "00000000-0000-0000-0000-000000000000", RepairByBrowser); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("pending check did not run before source load")
		}
		_, e = pool.Exec(ctx, `DELETE FROM profile_repairs WHERE user_id=$1`, other.ID)
		must(e)
		// Preflight also bounds current children and large current profile fields.
		_, e = pool.Exec(ctx, `INSERT INTO skills(user_id,name) SELECT $1,'Synthetic '||n FROM generate_series(1,501) n`, other.ID)
		must(e)
		if _, e = s.ProposeProfileRepair(ctx, other.ID, small.ID, RepairByBrowser); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("oversized current skills accepted")
		}
		_, e = pool.Exec(ctx, `DELETE FROM skills WHERE user_id=$1`, other.ID)
		must(e)
		must(s.SaveProfile(ctx, other.ID, models.Profile{Summary: strings.Repeat("x", 2<<20)}))
		if _, e = s.ProposeProfileRepair(ctx, other.ID, small.ID, RepairByBrowser); !errors.Is(e, ErrRepairInvalid) {
			t.Fatal("oversized current profile accepted")
		}
		var pending int
		must(pool.QueryRow(ctx, `SELECT count(*) FROM profile_repairs WHERE user_id=$1`, other.ID).Scan(&pending))
		if pending != 0 {
			t.Fatal("rejected size limit left a proposal")
		}

	})
	t.Run("stale active profile", func(t *testing.T) {
		r := propose()
		_, e = s.AddSkill(ctx, u.ID, models.Skill{Name: "Later edit"})
		must(e)
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("clobbered later edit")
		}
	})
	t.Run("source approved and relabelled after the proposal", func(t *testing.T) {
		r := propose()
		_, e = pool.Exec(ctx, `UPDATE cv_documents SET approved=true,approved_at=now(),updated_at=now(),label='Renamed' WHERE user_id=$1 AND id=$2`, u.ID, doc.ID)
		must(e)
		a, e := s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
		must(e)
		if a.Status != "applied" {
			t.Fatal("approval of the source refused the apply")
		}
		_, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "restore", a.AppliedFingerprint)
		must(e)
	})
	t.Run("removed employers stay excluded by name and domain", func(t *testing.T) {
		_, e = s.AddExperience(ctx, u.ID, models.WorkExperience{CompanyDomain: "domain-only.example.test", Title: "Contractor"})
		must(e)
		r := propose()
		_, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
		must(e)
		list, e := s.Blacklist(ctx, u.ID)
		must(e)
		found := map[string]string{}
		for _, b := range list {
			found[b.CompanyName] = b.CompanyDomain
		}
		if found["Merged Old Co"] != "old.example.test" || found["domain-only.example.test"] != "domain-only.example.test" || found["Source Co"] != "" {
			t.Fatalf("exclusions after apply: %v", found)
		}
		if _, listed := found["Explicit No"]; !listed {
			t.Fatal("the user's own exclusion was lost")
		}
	})
	t.Run("source parse changed", func(t *testing.T) {
		r := propose()
		_, e = pool.Exec(ctx, `UPDATE cv_documents SET parsed=parsed || '{"summary":"Changed"}'::jsonb WHERE user_id=$1 AND id=$2`, u.ID, doc.ID)
		must(e)
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("changed parse accepted")
		}
	})
	t.Run("source bytes changed", func(t *testing.T) {
		r := propose()
		_, e = pool.Exec(ctx, `UPDATE cv_documents SET data=$3 WHERE user_id=$1 AND id=$2`, u.ID, doc.ID, []byte("Different synthetic bytes"))
		must(e)
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("changed bytes accepted")
		}
	})
	t.Run("restore after later edit", func(t *testing.T) {
		r := propose()
		a, e := s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
		must(e)
		_, e = s.AddBlacklist(ctx, u.ID, models.BlacklistEntry{CompanyName: "Later exclusion"})
		must(e)
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "restore", a.AppliedFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("restore clobbered later exclusion")
		}
	})
	t.Run("atomic rollback", func(t *testing.T) {
		r := propose()
		original := get()
		otherSkill, e := s.AddSkill(ctx, other.ID, models.Skill{Name: "Owned elsewhere"})
		must(e)
		// Simulate a write constraint failure after profile/role updates have begun.
		r.After.Profile.Skills[0].ID = otherSkill.ID
		raw, _ := json.Marshal(r)
		_, e = pool.Exec(ctx, `UPDATE profile_repairs SET proposal=$3 WHERE user_id=$1 AND id=$2`, u.ID, r.ID, raw)
		must(e)
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint); e == nil {
			t.Fatal("expected constraint failure")
		}
		if repairHash(get()) != repairHash(original) {
			t.Fatal("partial profile write survived")
		}
	})
	t.Run("concurrent decisions", func(t *testing.T) {
		r := propose()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint)
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		success, conflict := 0, 0
		for e := range errs {
			if e == nil {
				success++
			} else if errors.Is(e, ErrRepairConflict) {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal("concurrent double apply")
		}
	})
	t.Run("reject retains source after deletion", func(t *testing.T) {
		pending := propose()
		r := propose()
		_, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "reject", r.BeforeFingerprint)
		must(e)
		must(s.DeleteCV(ctx, u.ID, doc.ID))
		if _, e = s.DecideProfileRepair(ctx, u.ID, pending.ID, "apply", pending.BeforeFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("deleted source applied")
		}
		v, e := s.ProfileRepair(ctx, u.ID, r.ID)
		must(e)
		if v.Status != "rejected" || v.Source.Parsed["name"] != "Source Name" || len(v.Before.Profile.Experiences) == 0 {
			t.Fatal("rejected snapshot lost")
		}
		if _, e = s.DecideProfileRepair(ctx, u.ID, r.ID, "apply", r.BeforeFingerprint); !errors.Is(e, ErrRepairConflict) {
			t.Fatal("rejected proposal applied")
		}
	})
}
