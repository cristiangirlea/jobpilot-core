package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/cv"
	"jobpilot/core/api/internal/models"
)

var ErrRepairConflict = errors.New("Profile or source CV changed, or this decision is no longer available. Reload and create a new proposal.")

// ErrRepairInvalid matches every refusal to build a proposal; the error returned says which.
var ErrRepairInvalid = errors.New("A repair cannot be proposed from this CV.")

// Limits of one proposal. A parse or profile beyond them is refused, never cut.
const (
	repairMaxRoles   = 200
	repairMaxSkills  = 500
	repairMaxBytes   = 2 << 20
	repairMaxPending = 20
)

type repairInvalid string

func (e repairInvalid) Error() string        { return string(e) }
func (e repairInvalid) Is(target error) bool { return target == ErrRepairInvalid }

const (
	repairNotBase     = repairInvalid("Only a stored base CV can be the source of a repair.")
	repairNoFacts     = repairInvalid("This CV's stored parse has no facts to repair from.")
	repairTooMany     = repairInvalid("A repair handles at most 200 roles and 500 skills, in the CV and in the profile.")
	repairTooLarge    = repairInvalid("The CV and the profile together are larger than a repair's 2 MiB limit.")
	repairTooManyOpen = repairInvalid("20 repair proposals are waiting for review. Apply or reject one first.")
)

type ProfileSnapshot struct {
	Profile   models.Profile          `json:"profile"`
	Blacklist []models.BlacklistEntry `json:"blacklist"`
}
type RepairChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type ProfileRepair struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// ProposedBy is RepairByBrowser or RepairByAgent: who asked for the proposal.
	ProposedBy string `json:"proposed_by"`
	// Source is the CV's record and parsed facts as they were, without its text.
	Source             models.CVDocument `json:"source"`
	SourceSHA256       string            `json:"source_sha256"`
	SourceFingerprint  string            `json:"source_fingerprint"`
	BeforeFingerprint  string            `json:"before_fingerprint"`
	AppliedFingerprint string            `json:"applied_fingerprint,omitempty"`
	Before             ProfileSnapshot   `json:"before"`
	After              ProfileSnapshot   `json:"after"`
	Changes            []RepairChange    `json:"changes"`
	Warnings           []string          `json:"warnings"`
	CreatedAt          time.Time         `json:"created_at"`
}

// Who proposed a repair, shown on the review page.
const (
	RepairByBrowser = "browser"
	RepairByAgent   = "agent"
)

func repairHash(v any) string { b, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(b)) }

// Stable ordering includes IDs, so a delete/re-add is a conflicting edit even
// when it happens to carry the same visible values.
func snapshot(ctx context.Context, tx pgx.Tx, u string) (ProfileSnapshot, error) {
	ps := &profileStore{tx}
	p, e := ps.Profile(ctx, u)
	if e != nil {
		return ProfileSnapshot{}, e
	}
	for _, list := range []*[]string{&p.DesiredTitles, &p.Seniorities, &p.Certifications, &p.Languages, &p.WorkPreference, &p.PreferredCountries, &p.PreferredTimezones, &p.IndustryPreferences} {
		if *list == nil {
			*list = []string{}
		}
	}
	b, e := ps.Blacklist(ctx, u)
	sort.Slice(p.Experiences, func(i, j int) bool { return p.Experiences[i].ID < p.Experiences[j].ID })
	sort.Slice(p.Skills, func(i, j int) bool { return p.Skills[i].ID < p.Skills[j].ID })
	sort.Slice(p.Projects, func(i, j int) bool { return p.Projects[i].ID < p.Projects[j].ID })
	sort.Slice(b, func(i, j int) bool { return b[i].ID < b[j].ID })
	return ProfileSnapshot{p, b}, e
}

const profileTransactionTimeout = 10 * time.Second

// repairTx opens the transaction of a repair or an import: the user's profile
// lock, held from the first read to the commit. The context bounds the whole
// operation, waiting included; the server's own limits end a transaction whose
// client stalls. lock_timeout bounds only the wait for the lock.
func repairTx(parent context.Context, s *Store, u string) (context.Context, pgx.Tx, func(), error) {
	ctx, cancel := context.WithTimeout(parent, profileTransactionTimeout)
	leave, e := enterProfileGate(ctx, u)
	if e != nil {
		cancel()
		return ctx, nil, func() {}, e
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		leave()
		cancel()
		return ctx, nil, func() {}, e
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			// Rollback must still work after the operation's context expires.
			rollbackCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = tx.Rollback(rollbackCtx)
			leave()
		})
	}
	_, e = tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '5s'; SET LOCAL idle_in_transaction_session_timeout = '5s'`)
	if e == nil {
		_, e = tx.Exec(ctx, profileLockSQL, u)
	}
	if e != nil {
		cleanup()
		return ctx, nil, func() {}, e
	}
	return ctx, tx, cleanup, nil
}
func repairSource(ctx context.Context, tx pgx.Tx, u, id string) (models.CVDocument, string, error) {
	d, e := scanCV(tx.QueryRow(ctx, `SELECT `+cvColumns+` FROM cv_documents WHERE user_id=$1 AND id=$2 FOR UPDATE`, u, id))
	if e != nil {
		return d, "", e
	}
	var hash string
	e = tx.QueryRow(ctx, `SELECT encode(sha256(data),'hex') FROM cv_documents WHERE user_id=$1 AND id=$2`, u, id).Scan(&hash)
	return d, hash, e
}

func repairCandidate(before ProfileSnapshot, source models.CVDocument) (ProfileSnapshot, []string, error) {
	var parsed cv.Parsed
	raw, e := json.Marshal(source.Parsed)
	if source.Kind != "base" {
		return before, nil, repairNotBase
	}
	if e != nil || json.Unmarshal(raw, &parsed) != nil {
		return before, nil, repairNoFacts
	}
	if len(parsed.Experiences) > repairMaxRoles || len(parsed.Skills) > repairMaxSkills {
		return before, nil, repairTooMany
	}
	// Deep copy: before is immutable, including its child arrays.
	raw, _ = json.Marshal(before)
	var after ProfileSnapshot
	_ = json.Unmarshal(raw, &after)
	p := &after.Profile
	found := false
	for _, a := range []struct {
		v string
		t *string
	}{{parsed.Name, &p.Name}, {parsed.Email, &p.Email}, {parsed.Phone, &p.Phone}, {parsed.Location, &p.Location}, {parsed.LinkedIn, &p.LinkedIn}, {parsed.GitHub, &p.GitHub}, {parsed.Portfolio, &p.Portfolio}, {parsed.CurrentTitle, &p.CurrentTitle}, {parsed.Summary, &p.Summary}} {
		if strings.TrimSpace(a.v) != "" {
			*a.t = a.v
			found = true
		}
	}
	// Zero means the parser found no overall years, so the profile's value stays.
	if parsed.YearsExperience > 0 {
		p.YearsExperience = parsed.YearsExperience
		found = true
	}
	if len(parsed.Certifications) > 0 {
		p.Certifications = parsed.Certifications
		found = true
	}
	if len(parsed.Languages) > 0 {
		p.Languages = parsed.Languages
		found = true
	}
	warnings := append([]string{}, parsed.Warnings...)
	// Absence is not evidence for clearing a field or the whole role/skill list.
	if len(parsed.Experiences) > 0 {
		p.Experiences = parsed.Experiences
		found = true
		for i := range p.Experiences {
			x := &p.Experiences[i]
			x.ID = ""
			if strings.TrimSpace(x.Title) == "" {
				return before, nil, repairInvalid(fmt.Sprintf("Role %d of the CV's stored parse has no title. Upload a CV that parses with one.", i+1))
			}
			// The parser cannot always tell the employer; the role is still a fact.
			if strings.TrimSpace(x.Company) == "" {
				warnings = append(warnings, fmt.Sprintf("Role %d has no employer, so no company is excluded for it.", i+1))
			}
			if x.StartDate == "" || (!x.Current && x.EndDate == "") {
				warnings = append(warnings, fmt.Sprintf("Role %d has unknown dates; review before applying.", i+1))
			}
		}
	} else {
		warnings = append(warnings, "No parsed roles: current roles will be kept.")
	}
	if len(parsed.Skills) > 0 {
		p.Skills = parsed.Skills
		found = true
		seen := map[string]bool{}
		for i := range p.Skills {
			x := &p.Skills[i]
			x.ID = ""
			if strings.TrimSpace(x.Name) == "" || seen[x.Name] {
				return before, nil, repairInvalid(fmt.Sprintf("Skill %d of the CV's stored parse is empty or listed twice.", i+1))
			}
			seen[x.Name] = true
		}
	} else {
		warnings = append(warnings, "No parsed skills: current skills will be kept.")
	}
	if !found {
		return before, nil, repairNoFacts
	}
	return after, warnings, nil
}

// retainedExclusions lists the employers that leave the role list and are not
// excluded some other way. Matching rejects a job by a role's company name or
// by its domain, so a role known only by its domain is kept too, named by it.
func retainedExclusions(before, after ProfileSnapshot, now time.Time) []models.BlacklistEntry {
	key := func(name, domain string) string {
		return company.Normalize(name) + "|" + company.NormalizeDomain(domain)
	}
	excluded, domains := map[string]bool{}, map[string]bool{}
	keep := func(name, domain string) {
		excluded[key(name, domain)] = true
		domains[company.NormalizeDomain(domain)] = true
	}
	for _, b := range after.Blacklist {
		keep(b.CompanyName, b.CompanyDomain)
	}
	for _, x := range after.Profile.Experiences {
		keep(x.Company, x.CompanyDomain)
	}
	var out []models.BlacklistEntry
	for _, x := range before.Profile.Experiences {
		name, domain := strings.TrimSpace(x.Company), company.NormalizeDomain(x.CompanyDomain)
		if company.Normalize(name) == "" {
			// Nothing but a domain to exclude by, and only if nothing else has it.
			if domain == "" || domains[domain] {
				continue
			}
			name = domain
		} else if excluded[key(name, domain)] {
			continue
		}
		keep(name, domain)
		out = append(out, models.BlacklistEntry{CompanyName: name, CompanyDomain: domain, NormalizedName: company.Normalize(name), Reason: "Employer exclusion retained by profile repair", CreatedAt: now})
	}
	return out
}

// sourceFingerprint covers what a proposal is built from: the stored parse and
// text. Approving or relabelling the CV afterwards does not change it.
func sourceFingerprint(d models.CVDocument) string {
	return repairHash(struct {
		ID, Kind, Filename, Format, ExtractedText string
		Parsed                                    map[string]any
	}{d.ID, d.Kind, d.Filename, d.Format, d.ExtractedText, d.Parsed})
}

func (s *Store) ProposeProfileRepair(ctx context.Context, u, sourceID, proposedBy string) (ProfileRepair, error) {
	r := ProfileRepair{ProposedBy: proposedBy}
	ctx, tx, cleanup, e := repairTx(ctx, s, u)
	if e != nil {
		return r, e
	}
	defer cleanup()
	// At most 20 pending proposals per user; retained decisions are never pruned.
	var count int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM profile_repairs WHERE user_id=$1 AND status='pending'`, u).Scan(&count)
	if e != nil {
		return r, e
	}
	if count >= repairMaxPending {
		return r, repairTooManyOpen
	}
	// Cheap bounds refuse an oversized CV or profile before it is loaded. The
	// check of the finished record below is the one that decides.
	var fits, counted bool
	e = tx.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM work_experiences WHERE user_id=$1) <= $3
  AND (SELECT count(*) FROM skills WHERE user_id=$1) <= $4,
  octet_length(extracted_text)+octet_length(parsed::text) <= $5
  AND coalesce((SELECT octet_length(to_jsonb(p)::text) FROM user_profiles p WHERE user_id=$1),0)
    + coalesce((SELECT sum(octet_length(to_jsonb(x)::text)) FROM work_experiences x WHERE user_id=$1),0)
    + coalesce((SELECT sum(octet_length(to_jsonb(x)::text)) FROM skills x WHERE user_id=$1),0)
    + coalesce((SELECT sum(octet_length(to_jsonb(x)::text)) FROM projects x WHERE user_id=$1),0)
    + coalesce((SELECT sum(octet_length(to_jsonb(x)::text)) FROM company_blacklist x WHERE user_id=$1),0) <= $5
  FROM cv_documents WHERE user_id=$1 AND id=$2 FOR UPDATE`, u, sourceID, repairMaxRoles, repairMaxSkills, repairMaxBytes).Scan(&counted, &fits)
	if e != nil {
		return r, e
	}
	if !counted {
		return r, repairTooMany
	}
	if !fits {
		return r, repairTooLarge
	}
	r.Source, r.SourceSHA256, e = repairSource(ctx, tx, u, sourceID)
	if e != nil {
		return r, e
	}
	r.Before, e = snapshot(ctx, tx, u)
	if e != nil {
		return r, e
	}
	if len(r.Before.Profile.Experiences) > repairMaxRoles || len(r.Before.Profile.Skills) > repairMaxSkills {
		return r, repairTooMany
	}
	r.After, r.Warnings, e = repairCandidate(r.Before, r.Source)
	if e != nil {
		return r, e
	}
	// New IDs are allocated in the proposal, so the exact reviewed state can be
	// applied and later fingerprinted without a second interpretation of facts.
	var ids []*string
	for i := range r.After.Profile.Experiences {
		if r.After.Profile.Experiences[i].ID == "" {
			ids = append(ids, &r.After.Profile.Experiences[i].ID)
		}
	}
	for i := range r.After.Profile.Skills {
		if r.After.Profile.Skills[i].ID == "" {
			ids = append(ids, &r.After.Profile.Skills[i].ID)
		}
	}
	r.After.Blacklist = append(r.After.Blacklist, retainedExclusions(r.Before, r.After, time.Now().UTC().Truncate(time.Microsecond))...)
	// Append blacklist pointers only after all appends (which can reallocate).
	for i := range r.After.Blacklist {
		if r.After.Blacklist[i].ID == "" {
			ids = append(ids, &r.After.Blacklist[i].ID)
		}
	}
	if len(ids) > 0 {
		rows, err := tx.Query(ctx, `SELECT gen_random_uuid()::text FROM generate_series(1,$1::int)`, len(ids))
		if err != nil {
			return r, err
		}
		allocated, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return r, err
		}
		for i := range ids {
			*ids[i] = allocated[i]
		}
	}
	// A field diff plus complete before/after lists exposes removed/corrupt rows.
	var b, a map[string]any
	raw, _ := json.Marshal(r.Before.Profile)
	_ = json.Unmarshal(raw, &b)
	raw, _ = json.Marshal(r.After.Profile)
	_ = json.Unmarshal(raw, &a)
	r.Changes = []RepairChange{}
	for k, v := range a {
		if repairHash(v) != repairHash(b[k]) {
			r.Changes = append(r.Changes, RepairChange{k, b[k], v})
		}
	}
	if repairHash(r.Before.Blacklist) != repairHash(r.After.Blacklist) {
		r.Changes = append(r.Changes, RepairChange{"blacklist", r.Before.Blacklist, r.After.Blacklist})
	}
	sort.Slice(r.Changes, func(i, j int) bool { return r.Changes[i].Field < r.Changes[j].Field })
	r.BeforeFingerprint = repairHash(r.Before)
	r.SourceFingerprint = sourceFingerprint(r.Source)
	// The record outlives the CV, so it keeps the parsed facts and not the text.
	r.Source.ExtractedText = ""
	delete(r.Source.Parsed, "text")
	r.Status = "pending"
	r.CreatedAt = time.Now().UTC()
	raw, e = json.Marshal(r)
	if e != nil {
		return r, e
	}
	if len(raw) > repairMaxBytes {
		return r, repairTooLarge
	}
	e = tx.QueryRow(ctx, `INSERT INTO profile_repairs(user_id,proposal) VALUES($1,$2) RETURNING id`, u, raw).Scan(&r.ID)
	if e != nil {
		return r, e
	}
	return r, tx.Commit(ctx)
}

func readRepair(row pgx.Row) (ProfileRepair, error) {
	var r ProfileRepair
	var raw []byte
	var id, status string
	e := row.Scan(&id, &status, &raw)
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(raw, &r)
	r.ID = id
	r.Status = status
	return r, e
}
func (s *Store) ProfileRepair(ctx context.Context, u, id string) (ProfileRepair, error) {
	return readRepair(s.DB.QueryRow(ctx, `SELECT id,status,proposal FROM profile_repairs WHERE user_id=$1 AND id=$2`, u, id))
}

type ProfileRepairSummary struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ProposedBy string    `json:"proposed_by"`
}

func (s *Store) ProfileRepairs(ctx context.Context, u string) ([]ProfileRepairSummary, error) {
	rows, e := s.DB.Query(ctx, `SELECT id,status,created_at,coalesce(proposal->>'proposed_by','') FROM profile_repairs WHERE user_id=$1 ORDER BY created_at DESC LIMIT 100`, u)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ProfileRepairSummary])
}

// All writes and the decision are committed together. The snapshots are never
// updated or deleted; only status and the fingerprint of the applied state change.
func (s *Store) DecideProfileRepair(ctx context.Context, u, id, decision, expected string) (ProfileRepair, error) {
	var r ProfileRepair
	ctx, tx, cleanup, e := repairTx(ctx, s, u)
	if e != nil {
		return r, e
	}
	defer cleanup()
	r, e = readRepair(tx.QueryRow(ctx, `SELECT id,status,proposal FROM profile_repairs WHERE user_id=$1 AND id=$2 FOR UPDATE`, u, id))
	if e != nil {
		return r, e
	}
	if decision == "reject" {
		if r.Status != "pending" || expected != r.BeforeFingerprint {
			return r, ErrRepairConflict
		}
		r.Status = "rejected"
	} else {
		wanted := r.BeforeFingerprint
		target := r.After
		if decision == "restore" {
			if r.Status != "applied" {
				return r, ErrRepairConflict
			}
			wanted = r.AppliedFingerprint
			target = r.Before
		} else if decision != "apply" || r.Status != "pending" {
			return r, ErrRepairConflict
		}
		current, err := snapshot(ctx, tx, u)
		if err != nil {
			return r, err
		}
		if expected == "" || expected != wanted || repairHash(current) != wanted {
			return r, ErrRepairConflict
		}
		if decision == "apply" {
			source, hash, err := repairSource(ctx, tx, u, r.Source.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return r, ErrRepairConflict
			}
			if err != nil {
				return r, err
			}
			if hash != r.SourceSHA256 || sourceFingerprint(source) != r.SourceFingerprint {
				return r, ErrRepairConflict
			}
		}
		if e = writeRepairSnapshot(ctx, tx, u, target); e != nil {
			return r, e
		}
		if decision == "apply" {
			actual, err := snapshot(ctx, tx, u)
			if err != nil {
				return r, err
			}
			r.AppliedFingerprint = repairHash(actual)
			r.Status = "applied"
		} else {
			r.Status = "restored"
		}
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return r, e
	}
	_, e = tx.Exec(ctx, `UPDATE profile_repairs SET status=$3,proposal=$4 WHERE user_id=$1 AND id=$2`, u, id, r.Status, raw)
	if e != nil {
		return r, e
	}
	return r, tx.Commit(ctx)
}

// writeRepairSnapshot replaces the profile's fields, roles, skills and excluded
// companies with the snapshot's, ids included. Each list is one statement, so a
// long list costs one round trip, not one per row.
func writeRepairSnapshot(ctx context.Context, tx pgx.Tx, u string, target ProfileSnapshot) error {
	if e := (&profileStore{tx}).SaveProfile(ctx, u, target.Profile); e != nil {
		return e
	}
	for _, table := range []string{"work_experiences", "skills", "company_blacklist"} {
		if _, e := tx.Exec(ctx, `DELETE FROM `+table+` WHERE user_id=$1`, u); e != nil {
			return e
		}
	}
	roles := target.Profile.Experiences
	ids, a, b, c, d, f, g := make([]string, len(roles)), make([]string, len(roles)), make([]string, len(roles)), make([]string, len(roles)), make([]string, len(roles)), make([]string, len(roles)), make([]string, len(roles))
	flags := make([]bool, len(roles))
	for i, x := range roles {
		ids[i], a[i], b[i], c[i], d[i], f[i], flags[i], g[i] = x.ID, x.Company, x.CompanyDomain, x.Title, x.StartDate, x.EndDate, x.Current, x.Description
	}
	if _, e := tx.Exec(ctx, `INSERT INTO work_experiences(id,user_id,company,company_domain,title,start_date,end_date,current,description)
SELECT id,$1,company,company_domain,title,start_date,end_date,current,description
FROM unnest($2::uuid[],$3::text[],$4::text[],$5::text[],$6::text[],$7::text[],$8::boolean[],$9::text[]) AS t(id,company,company_domain,title,start_date,end_date,current,description)`, u, ids, a, b, c, d, f, flags, g); e != nil {
		return e
	}
	skills := target.Profile.Skills
	ids, a, b = make([]string, len(skills)), make([]string, len(skills)), make([]string, len(skills))
	years := make([]float64, len(skills))
	for i, x := range skills {
		ids[i], a[i], b[i], years[i] = x.ID, x.Name, x.Level, x.Years
	}
	if _, e := tx.Exec(ctx, `INSERT INTO skills(id,user_id,name,level,years)
SELECT id,$1,name,level,years FROM unnest($2::uuid[],$3::text[],$4::text[],$5::numeric[]) AS t(id,name,level,years)`, u, ids, a, b, years); e != nil {
		return e
	}
	list := target.Blacklist
	ids, a, b, c, d = make([]string, len(list)), make([]string, len(list)), make([]string, len(list)), make([]string, len(list)), make([]string, len(list))
	created := make([]time.Time, len(list))
	for i, x := range list {
		ids[i], a[i], b[i], c[i], d[i], created[i] = x.ID, x.CompanyName, x.CompanyDomain, x.NormalizedName, x.Reason, x.CreatedAt
	}
	_, e := tx.Exec(ctx, `INSERT INTO company_blacklist(id,user_id,company_name,company_domain,normalized_name,reason,created_at)
SELECT id,$1,company_name,company_domain,normalized_name,reason,created_at
FROM unnest($2::uuid[],$3::text[],$4::text[],$5::text[],$6::text[],$7::timestamptz[]) AS t(id,company_name,company_domain,normalized_name,reason,created_at)`, u, ids, a, b, c, d, created)
	return e
}
