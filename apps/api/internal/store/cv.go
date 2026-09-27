package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/models"
)

// cvColumns deliberately omits the file bytes: listings and detail views never
// need them, and loading megabytes per row to render a list would be wasteful.
const cvColumns = `id,kind,label,filename,format,content_type,size_bytes,extracted_text,parsed,role_family,technologies,approved,approved_at,parent_id,fingerprint,created_at,updated_at`

func scanCV(row pgx.Row) (models.CVDocument, error) {
	var doc models.CVDocument
	var parsed []byte
	var parent *string
	err := row.Scan(&doc.ID, &doc.Kind, &doc.Label, &doc.Filename, &doc.Format, &doc.ContentType,
		&doc.SizeBytes, &doc.ExtractedText, &parsed, &doc.RoleFamily, &doc.Technologies,
		&doc.Approved, &doc.ApprovedAt, &parent, &doc.Fingerprint, &doc.CreatedAt, &doc.UpdatedAt)
	if err != nil {
		return doc, err
	}
	if parent != nil {
		doc.ParentID = *parent
	}
	if len(parsed) > 0 {
		_ = json.Unmarshal(parsed, &doc.Parsed)
	}
	return doc, nil
}

// SaveCV stores an uploaded or generated CV. Re-uploading a byte-identical file
// refreshes the existing row rather than creating a duplicate to review, but it
// clears approval, because the parse may now differ.
func (s *Store) SaveCV(ctx context.Context, userID string, doc models.CVDocument, data []byte) (models.CVDocument, error) {
	parsed, err := json.Marshal(doc.Parsed)
	if err != nil {
		return models.CVDocument{}, err
	}
	if doc.Technologies == nil {
		doc.Technologies = []string{}
	}
	var parent any
	if doc.ParentID != "" {
		parent = doc.ParentID
	}
	return scanCV(s.DB.QueryRow(ctx, `INSERT INTO cv_documents
		(user_id,kind,label,filename,format,content_type,size_bytes,data,extracted_text,parsed,role_family,technologies,parent_id,fingerprint,approved)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (user_id,fingerprint) WHERE fingerprint <> '' DO UPDATE SET
			label=EXCLUDED.label, filename=EXCLUDED.filename, format=EXCLUDED.format,
			content_type=EXCLUDED.content_type, size_bytes=EXCLUDED.size_bytes, data=EXCLUDED.data,
			extracted_text=EXCLUDED.extracted_text, parsed=EXCLUDED.parsed,
			role_family=EXCLUDED.role_family, technologies=EXCLUDED.technologies,
			approved=false, approved_at=NULL, updated_at=now()
		RETURNING `+cvColumns,
		userID, doc.Kind, doc.Label, doc.Filename, doc.Format, doc.ContentType, doc.SizeBytes,
		data, doc.ExtractedText, parsed, doc.RoleFamily, doc.Technologies, parent, doc.Fingerprint, doc.Approved))
}

func (s *Store) ListCVs(ctx context.Context, userID string) ([]models.CVDocument, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+cvColumns+` FROM cv_documents WHERE user_id=$1 ORDER BY approved DESC, created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CVDocument{}
	for rows.Next() {
		doc, scanErr := scanCV(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		// A listing shows metadata only; the full text belongs to the detail view.
		doc.ExtractedText = ""
		out = append(out, doc)
	}
	return out, rows.Err()
}

// BaseCVTechnologies returns the technologies found in the user's newest base
// CV, preferring an approved one, or nothing when no CV has been uploaded.
func (s *Store) BaseCVTechnologies(ctx context.Context, userID string) ([]string, error) {
	var technologies []string
	err := s.DB.QueryRow(ctx, `SELECT technologies FROM cv_documents WHERE user_id=$1 AND kind='base' ORDER BY approved DESC, created_at DESC LIMIT 1`, userID).Scan(&technologies)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return technologies, err
}

func (s *Store) CV(ctx context.Context, userID, id string) (models.CVDocument, error) {
	return scanCV(s.DB.QueryRow(ctx, `SELECT `+cvColumns+` FROM cv_documents WHERE user_id=$1 AND id=$2`, userID, id))
}

// CVFile returns the original bytes for download or for attaching to an
// application.
func (s *Store) CVFile(ctx context.Context, userID, id string) (models.CVDocument, []byte, error) {
	var doc models.CVDocument
	var data []byte
	err := s.DB.QueryRow(ctx, `SELECT id,filename,content_type,format,approved FROM cv_documents WHERE user_id=$1 AND id=$2`, userID, id).
		Scan(&doc.ID, &doc.Filename, &doc.ContentType, &doc.Format, &doc.Approved)
	if err != nil {
		return doc, nil, err
	}
	err = s.DB.QueryRow(ctx, `SELECT data FROM cv_documents WHERE user_id=$1 AND id=$2`, userID, id).Scan(&data)
	return doc, data, err
}

// SetCVApproval records the user's decision. Approval is what makes a CV
// eligible to be sent, so it is stored explicitly with its timestamp.
func (s *Store) SetCVApproval(ctx context.Context, userID, id string, approved bool) (models.CVDocument, error) {
	doc, err := scanCV(s.DB.QueryRow(ctx, `UPDATE cv_documents
		SET approved=$3, approved_at=CASE WHEN $3 THEN now() ELSE NULL END, updated_at=now()
		WHERE user_id=$1 AND id=$2 RETURNING `+cvColumns, userID, id, approved))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CVDocument{}, pgx.ErrNoRows
	}
	return doc, err
}

func (s *Store) DeleteCV(ctx context.Context, userID, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM cv_documents WHERE user_id=$1 AND id=$2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ApprovedBaseCV returns the CV to attach when nothing more specific applies.
// LatestBaseCV returns the user's uploaded CV that applications draw on: the
// most recently approved one, or else the newest. An unapproved CV can still
// supply facts for a tailored CV, which waits for approval itself; only an
// approved one may be sent as it is.
func (s *Store) LatestBaseCV(ctx context.Context, userID string) (models.CVDocument, error) {
	return scanCV(s.DB.QueryRow(ctx, `SELECT `+cvColumns+` FROM cv_documents
		WHERE user_id=$1 AND kind='base' ORDER BY approved DESC, approved_at DESC NULLS LAST, created_at DESC LIMIT 1`, userID))
}

// ApprovedTailoredCVs returns approved tailored CVs for reuse, the ones written
// for the same kind of role first. Only approved rows are returned, so an
// unreviewed draft can never be attached to an application.
func (s *Store) ApprovedTailoredCVs(ctx context.Context, userID, roleFamily string, limit int) ([]models.CVDocument, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cvColumns+` FROM cv_documents
		WHERE user_id=$1 AND kind='tailored' AND approved
		ORDER BY (role_family=$2) DESC, approved_at DESC NULLS LAST LIMIT $3`, userID, roleFamily, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CVDocument{}
	for rows.Next() {
		doc, scanErr := scanCV(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		doc.ExtractedText = ""
		out = append(out, doc)
	}
	return out, rows.Err()
}

// CVByFingerprint finds a stored CV by its exact bytes. Regenerating a CV that
// is byte-identical to one already approved must reuse that approval rather
// than reset it.
func (s *Store) CVByFingerprint(ctx context.Context, userID, fingerprint string) (models.CVDocument, bool, error) {
	if fingerprint == "" {
		return models.CVDocument{}, false, nil
	}
	doc, err := scanCV(s.DB.QueryRow(ctx, `SELECT `+cvColumns+` FROM cv_documents WHERE user_id=$1 AND fingerprint=$2`, userID, fingerprint))
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CVDocument{}, false, nil
	}
	if err != nil {
		return models.CVDocument{}, false, err
	}
	return doc, true, nil
}
