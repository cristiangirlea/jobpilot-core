package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/models"
)

var ErrHistoryConflict = errors.New("history import needs reconciliation; preview again and review candidates; nothing was overwritten")

// ErrHistoryCursor is a paging position this API did not hand out.
var ErrHistoryCursor = errors.New("after must be the next value of the previous page")

// historyDate reports whether s is a bare date, which names a day in no zone.
func historyDate(s string) (t time.Time, day bool, e error) {
	if s == "" {
		return t, false, nil
	}
	if t, e = time.Parse("2006-01-02", s); e == nil {
		return t, true, nil
	}
	t, e = time.Parse(time.RFC3339, s)
	return t, false, e
}

// outcomePrecedes is true only when the two values establish that order: a bare
// date beside a timestamp may lie in any zone, so it counts as its whole day.
func outcomePrecedes(a, o time.Time, aDay, oDay bool) bool {
	switch {
	case aDay == oDay:
		return o.Before(a)
	case oDay:
		return !o.Add(36 * time.Hour).After(a)
	default:
		return o.Before(a.Add(-14 * time.Hour))
	}
}

func ValidateHistory(in models.ExternalApplicationInput) error {
	for _, v := range []string{in.ExternalKey, in.Employer, in.Source, in.EvidenceRef, in.Status, in.EvidenceKind} {
		if strings.TrimSpace(v) == "" {
			return errors.New("external_key, employer, status, source, evidence_ref and evidence_kind are required")
		}
	}
	for _, v := range []string{in.ExternalKey, in.Employer, in.Title, in.Requisition, in.CanonicalURL, in.AppliedAt, in.OutcomeAt, in.Status, in.Source, in.EvidenceRef, in.EvidenceKind, in.CVVersion} {
		if len(v) > 2000 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("history fields must be single-line references of at most 2000 bytes; do not include email bodies or credentials")
		}
	}
	if company.Normalize(in.Employer) == "" {
		return errors.New("employer must contain a name")
	}
	if in.CanonicalURL != "" {
		// ParseRequestURI leaves a fragment inside the path, so look for it here.
		u, e := url.ParseRequestURI(in.CanonicalURL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.Contains(in.CanonicalURL, "#") {
			return errors.New("canonical_url must be an http(s) URL without credentials or a fragment")
		}
	}
	a, aDay, e := historyDate(in.AppliedAt)
	if e != nil {
		return errors.New("applied_at must be empty, YYYY-MM-DD or RFC3339")
	}
	o, oDay, e := historyDate(in.OutcomeAt)
	if e != nil {
		return errors.New("outcome_at must be empty, YYYY-MM-DD or RFC3339")
	}
	if in.AppliedAt != "" && in.OutcomeAt != "" && outcomePrecedes(a, o, aDay, oDay) {
		return errors.New("outcome_at cannot precede applied_at")
	}
	switch in.Status {
	case "unknown", "applied", "interview", "rejected", "offer", "withdrawn":
	default:
		return errors.New("historical status must be unknown, applied, interview, rejected, offer or withdrawn")
	}
	switch in.EvidenceKind {
	case "generic_receipt", "alert":
		if in.Status != "unknown" || in.AppliedAt != "" || in.OutcomeAt != "" {
			return errors.New("generic receipts and alerts require unknown status and unknown dates; they do not prove an application")
		}
	case "application_receipt":
		if in.Status != "applied" {
			return errors.New("an application receipt supports applied only; use outcome evidence for a later status")
		}
	case "rejection", "interview", "offer":
		want := map[string]string{"rejection": "rejected", "interview": "interview", "offer": "offer"}[in.EvidenceKind]
		if in.Status != want {
			return fmt.Errorf("%s evidence requires %s status", in.EvidenceKind, want)
		}
	case "user_confirmation":
	default:
		return errors.New("evidence_kind must be application_receipt, rejection, interview, offer, user_confirmation, generic_receipt or alert")
	}
	return nil
}

const historyColumns = `id,record,imported_at`

func scanHistory(row scanner) (models.ExternalApplication, error) {
	var x models.ExternalApplication
	var raw []byte
	if e := row.Scan(&x.ID, &raw, &x.ImportedAt); e != nil {
		return x, e
	}
	e := json.Unmarshal(raw, &x.ExternalApplicationInput)
	return x, e
}

func (s *Store) ExternalHistory(ctx context.Context, user, after string, limit int) (models.HistoryPage, error) {
	out := models.HistoryPage{Items: []models.ExternalApplication{}}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var cursor pgtype.UUID // NULL starts at the first page
	if after != "" {
		if e := cursor.Scan(after); e != nil {
			return out, ErrHistoryCursor
		}
	}
	rows, e := s.DB.Query(ctx, `SELECT `+historyColumns+` FROM external_application_history WHERE user_id=$1 AND ($2::uuid IS NULL OR id > $2) ORDER BY id LIMIT $3`, user, cursor, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		x, e := scanHistory(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, x)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.Next = out.Items[limit-1].ID
	}
	return out, nil
}

// ImportExternalHistory serializes commits per tenant and recomputes the preview
// inside the same transaction. No path here calls discovery, outbox or outbound.
// key signs the preview token, so only a preview this server answered can commit.
func (s *Store) ImportExternalHistory(ctx context.Context, user string, in models.HistoryImport, key []byte) (models.HistoryPreview, error) {
	out := models.HistoryPreview{Action: "create", Candidates: []models.HistoryCandidate{}}
	if len(key) == 0 {
		return out, errors.New("history preview key is not set")
	}
	if e := ValidateHistory(in.Record); e != nil {
		return out, e
	}
	if in.Mode != "preview" && in.Mode != "commit" {
		return out, errors.New("mode must be preview or commit")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if in.Mode == "commit" {
		if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,169))`, user); e != nil {
			return out, e
		}
	}
	r := in.Record
	rows, e := tx.Query(ctx, `SELECT `+historyColumns+` FROM external_application_history WHERE user_id=$1 AND (external_key=$2 OR employer_normalized=$3 OR (canonical_url<>'' AND canonical_url=$4)) ORDER BY (external_key=$2) DESC,id LIMIT 101`, user, r.ExternalKey, company.Normalize(r.Employer), r.CanonicalURL)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		x, err := scanHistory(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		kind := "possible"
		if x.ExternalKey == r.ExternalKey || (r.Requisition != "" && x.Requisition == r.Requisition && company.Normalize(x.Employer) == company.Normalize(r.Employer)) || (r.CanonicalURL != "" && x.CanonicalURL == r.CanonicalURL) {
			kind = "exact"
		}
		out.Candidates = append(out.Candidates, models.HistoryCandidate{Record: x, Kind: kind})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, c := range out.Candidates {
		if c.Record.ExternalKey == r.ExternalKey {
			out.Record = &c.Record
			if c.Record.ExternalApplicationInput == r {
				out.Action = "unchanged"
			} else {
				out.Action = "conflict"
			}
			break
		}
		if c.Kind == "exact" {
			out.Action = "conflict"
		} else if out.Action != "conflict" {
			out.Action = "review"
		}
	}
	// Too many related records cannot be safely reviewed in a bounded response.
	if len(out.Candidates) > 100 && out.Action != "unchanged" && out.Action != "conflict" {
		out.Action = "review"
	}
	raw, _ := json.Marshal(struct {
		User       string
		Record     models.ExternalApplicationInput
		Action     string
		Candidates []models.HistoryCandidate
	}{user, r, out.Action, out.Candidates})
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("history-preview\x00"))
	mac.Write(raw)
	out.PreviewToken = hex.EncodeToString(mac.Sum(nil))
	if in.Mode == "preview" {
		return out, nil
	}
	if out.Action == "unchanged" {
		return out, nil
	} // a retry after a successful commit
	if (out.Action != "create" && !(out.Action == "review" && in.AllowPossibleDuplicates && len(out.Candidates) <= 100)) || !hmac.Equal([]byte(in.PreviewToken), []byte(out.PreviewToken)) {
		return out, ErrHistoryConflict
	}
	raw, _ = json.Marshal(r)
	x, e := scanHistory(tx.QueryRow(ctx, `INSERT INTO external_application_history(user_id,external_key,employer_normalized,requisition,canonical_url,record,employer_keys) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+historyColumns, user, r.ExternalKey, company.Normalize(r.Employer), r.Requisition, r.CanonicalURL, raw, employerKeys(r.Employer)))
	if e != nil {
		return out, e
	}
	out.Record = &x
	return out, tx.Commit(ctx)
}

// BackfillHistoryEmployerKeys gives records imported before employer_keys
// existed every name their employer was stated under. The migration could only
// copy the whole normalized name; later runs find nothing to do.
func (s *Store) BackfillHistoryEmployerKeys(ctx context.Context) (int, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, record->>'employer' FROM external_application_history
		WHERE employer_keys = ARRAY[employer_normalized] AND record->>'employer' ~* '\s(aka|a/k/a|dba|d/b/a|formerly)\s'`)
	if err != nil {
		return 0, err
	}
	stated := map[string]string{}
	for rows.Next() {
		var id, employer string
		if err = rows.Scan(&id, &employer); err != nil {
			rows.Close()
			return 0, err
		}
		stated[id] = employer
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	done := 0
	for id, employer := range stated {
		keys := employerKeys(employer)
		if len(keys) < 2 {
			continue
		}
		if _, err = s.DB.Exec(ctx, `UPDATE external_application_history SET employer_keys=$2 WHERE id=$1`, id, keys); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func employerKeys(name string) []string {
	keys := []string{}
	seen := map[string]bool{}
	for _, key := range append(company.Aliases(name), company.Normalize(name)) {
		if key != "" && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}
