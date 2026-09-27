package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobpilot/core/api/internal/company"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/slug"
	"regexp"
	"sort"
	"time"
)

type Store struct{ DB *pgxpool.Pool }

var ErrDuplicateJob = errors.New("discovered job already exists")
var ErrApplicationBlocked = errors.New("application actions are blocked for this rejected job")
var ErrDuplicateDiscoverySource = errors.New("a discovery source with the same provider, identifier, and query already exists")

func New(db *pgxpool.Pool) *Store { return &Store{DB: db} }

func (s *Store) CreateUser(ctx context.Context, email, hash string) (models.User, error) {
	var u models.User
	err := s.DB.QueryRow(ctx, `INSERT INTO users(email,password_hash) VALUES(lower($1),$2) RETURNING id,email,created_at`, email, hash).Scan(&u.ID, &u.Email, &u.CreatedAt)
	return u, err
}
func (s *Store) Credentials(ctx context.Context, email string) (models.User, string, error) {
	var u models.User
	var hash string
	err := s.DB.QueryRow(ctx, `SELECT id,email,created_at,password_hash FROM users WHERE email=lower($1)`, email).Scan(&u.ID, &u.Email, &u.CreatedAt, &hash)
	return u, hash, err
}

func (s *Store) Profile(ctx context.Context, userID string) (models.Profile, error) {
	var p models.Profile
	err := s.DB.QueryRow(ctx, `SELECT name,given_name,family_name,email,phone,location,linkedin,github,portfolio,current_title,desired_titles,seniority,seniorities,years_experience,certifications,languages,salary_expectation,rate_expectation,currency,work_preference,preferred_countries,preferred_timezones,work_authorization,notice_period,industry_preferences,summary FROM user_profiles WHERE user_id=$1`, userID).Scan(&p.Name, &p.GivenName, &p.FamilyName, &p.Email, &p.Phone, &p.Location, &p.LinkedIn, &p.GitHub, &p.Portfolio, &p.CurrentTitle, &p.DesiredTitles, &p.Seniority, &p.Seniorities, &p.YearsExperience, &p.Certifications, &p.Languages, &p.SalaryExpectation, &p.RateExpectation, &p.Currency, &p.WorkPreference, &p.PreferredCountries, &p.PreferredTimezones, &p.WorkAuthorization, &p.NoticePeriod, &p.IndustryPreferences, &p.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.DB.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&p.Email)
		if err != nil {
			return p, err
		}
	} else if err != nil {
		return p, err
	}
	if len(p.Seniorities) == 0 && p.Seniority != "" {
		p.Seniorities = []string{p.Seniority}
	}
	if p.Experiences, err = s.Experiences(ctx, userID); err != nil {
		return p, err
	}
	if p.Skills, err = s.Skills(ctx, userID); err != nil {
		return p, err
	}
	if p.Projects, err = s.Projects(ctx, userID); err != nil {
		return p, err
	}
	return p, nil
}
func (s *Store) SaveProfile(ctx context.Context, u string, p models.Profile) error {
	// The profile's list columns are NOT NULL. A caller that builds a profile in
	// Go rather than decoding one from JSON leaves these nil, so they are
	// normalised here instead of at every call site.
	for _, list := range []*[]string{&p.DesiredTitles, &p.Seniorities, &p.Certifications, &p.Languages,
		&p.WorkPreference, &p.PreferredCountries, &p.PreferredTimezones, &p.IndustryPreferences} {
		if *list == nil {
			*list = []string{}
		}
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO user_profiles(user_id,name,given_name,family_name,email,phone,location,linkedin,github,portfolio,current_title,desired_titles,seniority,seniorities,years_experience,certifications,languages,salary_expectation,rate_expectation,currency,work_preference,preferred_countries,preferred_timezones,work_authorization,notice_period,industry_preferences,summary) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27) ON CONFLICT(user_id) DO UPDATE SET name=EXCLUDED.name,given_name=EXCLUDED.given_name,family_name=EXCLUDED.family_name,email=EXCLUDED.email,phone=EXCLUDED.phone,location=EXCLUDED.location,linkedin=EXCLUDED.linkedin,github=EXCLUDED.github,portfolio=EXCLUDED.portfolio,current_title=EXCLUDED.current_title,desired_titles=EXCLUDED.desired_titles,seniority=EXCLUDED.seniority,seniorities=EXCLUDED.seniorities,years_experience=EXCLUDED.years_experience,certifications=EXCLUDED.certifications,languages=EXCLUDED.languages,salary_expectation=EXCLUDED.salary_expectation,rate_expectation=EXCLUDED.rate_expectation,currency=EXCLUDED.currency,work_preference=EXCLUDED.work_preference,preferred_countries=EXCLUDED.preferred_countries,preferred_timezones=EXCLUDED.preferred_timezones,work_authorization=EXCLUDED.work_authorization,notice_period=EXCLUDED.notice_period,industry_preferences=EXCLUDED.industry_preferences,summary=EXCLUDED.summary,updated_at=now()`, u, p.Name, p.GivenName, p.FamilyName, p.Email, p.Phone, p.Location, p.LinkedIn, p.GitHub, p.Portfolio, p.CurrentTitle, p.DesiredTitles, p.Seniority, p.Seniorities, p.YearsExperience, p.Certifications, p.Languages, p.SalaryExpectation, p.RateExpectation, p.Currency, p.WorkPreference, p.PreferredCountries, p.PreferredTimezones, p.WorkAuthorization, p.NoticePeriod, p.IndustryPreferences, p.Summary)
	return err
}

func (s *Store) Experiences(ctx context.Context, u string) ([]models.WorkExperience, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,company,company_domain,title,start_date,end_date,current,description FROM work_experiences WHERE user_id=$1 ORDER BY current DESC,start_date DESC`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.WorkExperience{}
	for rows.Next() {
		var x models.WorkExperience
		if err = rows.Scan(&x.ID, &x.Company, &x.CompanyDomain, &x.Title, &x.StartDate, &x.EndDate, &x.Current, &x.Description); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) AddExperience(ctx context.Context, u string, x models.WorkExperience) (models.WorkExperience, error) {
	err := s.DB.QueryRow(ctx, `INSERT INTO work_experiences(user_id,company,company_domain,title,start_date,end_date,current,description) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, u, x.Company, x.CompanyDomain, x.Title, x.StartDate, x.EndDate, x.Current, x.Description).Scan(&x.ID)
	return x, err
}

func (s *Store) Skills(ctx context.Context, u string) ([]models.Skill, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,level,years FROM skills WHERE user_id=$1 ORDER BY lower(name)`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Skill{}
	for rows.Next() {
		var x models.Skill
		if err = rows.Scan(&x.ID, &x.Name, &x.Level, &x.Years); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// MatchTexts returns the title and description of the user's best matches,
// accepted ones first, for counting which technologies postings ask for.
func (s *Store) MatchTexts(ctx context.Context, u string, limit int) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT j.title||' '||j.description FROM job_matches m JOIN jobs j ON j.id=m.job_id WHERE m.user_id=$1 ORDER BY m.rejected ASC,m.score DESC,j.created_at DESC LIMIT $2`, u, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
func (s *Store) AddSkill(ctx context.Context, u string, x models.Skill) (models.Skill, error) {
	err := s.DB.QueryRow(ctx, `INSERT INTO skills(user_id,name,level,years) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,name) DO UPDATE SET level=EXCLUDED.level,years=EXCLUDED.years RETURNING id`, u, x.Name, x.Level, x.Years).Scan(&x.ID)
	return x, err
}

func (s *Store) Projects(ctx context.Context, u string) ([]models.Project, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,description,url,skills FROM projects WHERE user_id=$1 ORDER BY name`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Project{}
	for rows.Next() {
		var x models.Project
		if err = rows.Scan(&x.ID, &x.Name, &x.Description, &x.URL, &x.Skills); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) AddProject(ctx context.Context, u string, x models.Project) (models.Project, error) {
	err := s.DB.QueryRow(ctx, `INSERT INTO projects(user_id,name,description,url,skills) VALUES($1,$2,$3,$4,$5) RETURNING id`, u, x.Name, x.Description, x.URL, x.Skills).Scan(&x.ID)
	return x, err
}

func (s *Store) Blacklist(ctx context.Context, u string) ([]models.BlacklistEntry, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,company_name,company_domain,normalized_name,reason,created_at FROM company_blacklist WHERE user_id=$1 ORDER BY company_name`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.BlacklistEntry{}
	for rows.Next() {
		var x models.BlacklistEntry
		if err = rows.Scan(&x.ID, &x.CompanyName, &x.CompanyDomain, &x.NormalizedName, &x.Reason, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) AddBlacklist(ctx context.Context, u string, x models.BlacklistEntry) (models.BlacklistEntry, error) {
	x.NormalizedName = company.Normalize(x.CompanyName)
	err := s.DB.QueryRow(ctx, `INSERT INTO company_blacklist(user_id,company_name,company_domain,normalized_name,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id,normalized_name,company_domain) DO UPDATE SET company_name=EXCLUDED.company_name,reason=EXCLUDED.reason RETURNING id,created_at`, u, x.CompanyName, company.NormalizeDomain(x.CompanyDomain), x.NormalizedName, x.Reason).Scan(&x.ID, &x.CreatedAt)
	return x, err
}
func (s *Store) DeleteOwned(ctx context.Context, table, u, id string) error {
	allowed := map[string]bool{"work_experiences": true, "skills": true, "projects": true, "company_blacklist": true, "filter_presets": true}
	if !allowed[table] {
		return errors.New("invalid table")
	}
	tag, err := s.DB.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=$1 AND user_id=$2`, table), id, u)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) Filters(ctx context.Context, u string) ([]models.FilterPreset, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,name,active,included_titles,excluded_title_words,locations,remote_types,min_salary,currency,employment_types,excluded_industries,required_skills,included_technologies,excluded_technologies,max_age_days FROM filter_presets WHERE user_id=$1 ORDER BY created_at DESC`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.FilterPreset{}
	for rows.Next() {
		var x models.FilterPreset
		if err = rows.Scan(&x.ID, &x.Name, &x.Active, &x.IncludedTitles, &x.ExcludedTitleWords, &x.Locations, &x.RemoteTypes, &x.MinSalary, &x.Currency, &x.EmploymentTypes, &x.ExcludedIndustries, &x.RequiredSkills, &x.IncludedTechnologies, &x.ExcludedTechnologies, &x.MaxAgeDays); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) ActiveFilter(ctx context.Context, u string) (*models.FilterPreset, error) {
	rows, err := s.Filters(ctx, u)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Active {
			return &rows[i], nil
		}
	}
	return nil, nil
}
func (s *Store) SaveFilter(ctx context.Context, u string, x models.FilterPreset) (models.FilterPreset, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return x, err
	}
	defer tx.Rollback(ctx)
	if x.Active {
		if _, err = tx.Exec(ctx, `UPDATE filter_presets SET active=false WHERE user_id=$1`, u); err != nil {
			return x, err
		}
	}
	if x.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO filter_presets(user_id,name,active,included_titles,excluded_title_words,locations,remote_types,min_salary,currency,employment_types,excluded_industries,required_skills,included_technologies,excluded_technologies,max_age_days) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`, u, x.Name, x.Active, x.IncludedTitles, x.ExcludedTitleWords, x.Locations, x.RemoteTypes, x.MinSalary, x.Currency, x.EmploymentTypes, x.ExcludedIndustries, x.RequiredSkills, x.IncludedTechnologies, x.ExcludedTechnologies, x.MaxAgeDays).Scan(&x.ID)
	} else {
		tag, e := tx.Exec(ctx, `UPDATE filter_presets SET name=$3,active=$4,included_titles=$5,excluded_title_words=$6,locations=$7,remote_types=$8,min_salary=$9,currency=$10,employment_types=$11,excluded_industries=$12,required_skills=$13,included_technologies=$14,excluded_technologies=$15,max_age_days=$16 WHERE id=$1 AND user_id=$2`, x.ID, u, x.Name, x.Active, x.IncludedTitles, x.ExcludedTitleWords, x.Locations, x.RemoteTypes, x.MinSalary, x.Currency, x.EmploymentTypes, x.ExcludedIndustries, x.RequiredSkills, x.IncludedTechnologies, x.ExcludedTechnologies, x.MaxAgeDays)
		err = e
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
	}
	if err != nil {
		return x, err
	}
	err = tx.Commit(ctx)
	return x, err
}

func (s *Store) InsertJob(ctx context.Context, u string, in models.JobInput, e models.Evaluation, filterID string) (models.JobWithMatch, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.JobWithMatch{}, err
	}
	defer tx.Rollback(ctx)
	normalized := company.Normalize(in.Company)
	domain := company.NormalizeDomain(in.CompanyDomain)
	var companyID string
	err = tx.QueryRow(ctx, `INSERT INTO companies(name,normalized_name,domain) VALUES($1,$2,$3) ON CONFLICT(normalized_name,domain) DO UPDATE SET name=EXCLUDED.name RETURNING id`, in.Company, normalized, domain).Scan(&companyID)
	if err != nil {
		return models.JobWithMatch{}, err
	}
	var j models.Job
	j.JobInput = in
	j.CompanyNormalized = normalized
	j.CompanyDomain = domain
	err = tx.QueryRow(ctx, `INSERT INTO jobs(user_id,company_id,title,company,company_normalized,company_domain,location,remote_type,salary_min,salary_max,currency,employment_type,industry,description,apply_url,posted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id,created_at`, u, companyID, in.Title, in.Company, normalized, domain, in.Location, in.RemoteType, in.SalaryMin, in.SalaryMax, in.Currency, in.EmploymentType, in.Industry, in.Description, in.ApplyURL, in.PostedAt).Scan(&j.ID, &j.CreatedAt)
	if err != nil {
		return models.JobWithMatch{}, err
	}
	if j.Slug, err = nameJob(ctx, tx, j.ID, slug.Job(in.ApplyURL, "", in.Company, in.Title)); err != nil {
		return models.JobWithMatch{}, err
	}
	rej, _ := json.Marshal(e.RejectionReasons)
	warn, _ := json.Marshal(e.Warnings)
	reasons, _ := json.Marshal(e.Reasons)
	matched, _ := json.Marshal(e.MatchedSkills)
	missing, _ := json.Marshal(e.MissingSkills)
	red, _ := json.Marshal(e.RedFlags)
	breakdown, _ := json.Marshal(e.Breakdown)
	var matchID string
	var fid any = nil
	if filterID != "" {
		fid = filterID
	}
	err = tx.QueryRow(ctx, `INSERT INTO job_matches(user_id,job_id,filter_id,rejected,score,rejection_reasons,warnings,reasons,matched_skills,missing_skills,red_flags,breakdown) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, u, j.ID, fid, e.Rejected, e.Score, rej, warn, reasons, matched, missing, red, breakdown).Scan(&matchID)
	if err != nil {
		return models.JobWithMatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return models.JobWithMatch{}, err
	}
	return models.JobWithMatch{Job: j, Match: e, MatchID: matchID}, nil
}

// CatalogJob reads a job from the shared catalog by id, without a per-user
// match. Callers that show a job alongside user-scoped data use it to label
// that data; visibility itself is still governed by the user's job_matches row.
func (s *Store) CatalogJob(ctx context.Context, id string) (models.Job, error) {
	var j models.Job
	err := s.DB.QueryRow(ctx, `SELECT id,title,company,company_normalized,company_domain,location,remote_type,salary_min,salary_max,currency,employment_type,industry,description,apply_url,posted_at,created_at,COALESCE(source_id::text,''),external_id,discovered_at,referral_partner,referral_disclosure,source_name,source_provider,slug,closed_at,closed_reason FROM jobs WHERE id=$1`, id).Scan(&j.ID, &j.Title, &j.Company, &j.CompanyNormalized, &j.CompanyDomain, &j.Location, &j.RemoteType, &j.SalaryMin, &j.SalaryMax, &j.Currency, &j.EmploymentType, &j.Industry, &j.Description, &j.ApplyURL, &j.PostedAt, &j.CreatedAt, &j.SourceID, &j.ExternalID, &j.DiscoveredAt, &j.ReferralPartner, &j.ReferralDisclosure, &j.SourceName, &j.SourceProvider, &j.Slug, &j.ClosedAt, &j.ClosedReason)
	return j, err
}

func (s *Store) CatalogJobByFingerprint(ctx context.Context, fingerprint string) (models.Job, error) {
	var j models.Job
	err := s.DB.QueryRow(ctx, `SELECT id,title,company,company_normalized,company_domain,location,remote_type,salary_min,salary_max,currency,employment_type,industry,description,apply_url,posted_at,created_at,COALESCE(source_id::text,''),external_id,discovered_at,referral_partner,referral_disclosure,source_name,source_provider,slug,closed_at,closed_reason FROM jobs WHERE fingerprint=$1`, fingerprint).Scan(&j.ID, &j.Title, &j.Company, &j.CompanyNormalized, &j.CompanyDomain, &j.Location, &j.RemoteType, &j.SalaryMin, &j.SalaryMax, &j.Currency, &j.EmploymentType, &j.Industry, &j.Description, &j.ApplyURL, &j.PostedAt, &j.CreatedAt, &j.SourceID, &j.ExternalID, &j.DiscoveredAt, &j.ReferralPartner, &j.ReferralDisclosure, &j.SourceName, &j.SourceProvider, &j.Slug, &j.ClosedAt, &j.ClosedReason)
	return j, err
}
func (s *Store) CreateDiscoveredJob(ctx context.Context, createdBy, sourceID, sourceProvider, sourceName, externalID, fingerprint string, in models.JobInput) (models.Job, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.Job{}, err
	}
	defer tx.Rollback(ctx)
	normalized := company.Normalize(in.Company)
	domain := company.NormalizeDomain(in.CompanyDomain)
	var companyID string
	if err = tx.QueryRow(ctx, `INSERT INTO companies(name,normalized_name,domain) VALUES($1,$2,$3) ON CONFLICT(normalized_name,domain) DO UPDATE SET name=EXCLUDED.name RETURNING id`, in.Company, normalized, domain).Scan(&companyID); err != nil {
		return models.Job{}, err
	}
	var sid any
	if sourceID != "" {
		sid = sourceID
	}
	var j models.Job
	j.JobInput = in
	j.CompanyNormalized = normalized
	j.CompanyDomain = domain
	j.SourceID = sourceID
	j.SourceProvider = sourceProvider
	j.SourceName = sourceName
	j.ExternalID = externalID
	err = tx.QueryRow(ctx, `INSERT INTO jobs(user_id,company_id,title,company,company_normalized,company_domain,location,remote_type,salary_min,salary_max,currency,employment_type,industry,description,apply_url,posted_at,source_id,external_id,fingerprint,discovered_at,source_provider,source_name) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,CASE WHEN $17::uuid IS NULL THEN NULL ELSE now() END,$20,$21) RETURNING id,created_at,discovered_at`, createdBy, companyID, in.Title, in.Company, normalized, domain, in.Location, in.RemoteType, in.SalaryMin, in.SalaryMax, in.Currency, in.EmploymentType, in.Industry, in.Description, in.ApplyURL, in.PostedAt, sid, externalID, fingerprint, sourceProvider, sourceName).Scan(&j.ID, &j.CreatedAt, &j.DiscoveredAt)
	if err != nil {
		return j, err
	}
	if j.Slug, err = nameJob(ctx, tx, j.ID, slug.Job(in.ApplyURL, sourceProvider, in.Company, in.Title)); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}

// RefreshDescription stores a provider's current text for a job already in
// the catalog.
func (s *Store) RefreshDescription(ctx context.Context, id, description string) error {
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET description=$2 WHERE id=$1 AND description<>$2`, id, description)
	return err
}

// LinkToCheck is a job whose posting link is due a liveness check.
type LinkToCheck struct{ ID, ApplyURL string }

// LinksToCheck returns open jobs that somebody still matches, least recently
// checked first. Jobs nobody could apply to anyway are not worth a request.
func (s *Store) LinksToCheck(ctx context.Context, olderThan time.Duration, limit int) ([]LinkToCheck, error) {
	rows, err := s.DB.Query(ctx, `SELECT j.id,j.apply_url FROM jobs j WHERE j.closed_at IS NULL AND j.apply_url<>''
		AND (j.last_checked_at IS NULL OR j.last_checked_at < now()-$1::interval)
		AND EXISTS(SELECT 1 FROM job_matches m WHERE m.job_id=j.id AND NOT m.rejected)
		ORDER BY j.last_checked_at NULLS FIRST LIMIT $2`, fmt.Sprintf("%d seconds", int(olderThan.Seconds())), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LinkToCheck])
}

// RecordLinkCheck notes that a job's link was checked and, when the check was
// definite, closes the job with the reason the user will see.
func (s *Store) RecordLinkCheck(ctx context.Context, id string, closed bool, reason string) error {
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET last_checked_at=now(),
		closed_at=CASE WHEN $2 THEN COALESCE(closed_at,now()) ELSE closed_at END,
		closed_reason=CASE WHEN $2 AND closed_at IS NULL THEN $3 ELSE closed_reason END WHERE id=$1`, id, closed, reason)
	return err
}

// CloseUnlisted closes the jobs of an employer board that the board's full
// feed no longer lists. It only applies to providers whose feed is the whole
// board; any user's source for the same board counts, since the catalog is
// shared. An empty listing closes nothing: that is more likely a glitch than
// an employer closing every role at once.
func (s *Store) CloseUnlisted(ctx context.Context, provider, identifier string, listed []string, reason string) (int64, error) {
	if len(listed) == 0 {
		return 0, nil
	}
	tag, err := s.DB.Exec(ctx, `UPDATE jobs SET closed_at=now(), closed_reason=$4
		WHERE closed_at IS NULL AND source_provider=$1 AND external_id<>'' AND NOT (external_id = ANY($3))
		AND source_id IN (SELECT id FROM discovery_sources WHERE provider=$1 AND lower(identifier)=lower($2))`,
		provider, identifier, listed, reason)
	return tag.RowsAffected(), err
}

// MarkSeen records that a feed listed the job just now. A job closed only
// because its board stopped listing it is reopened; one whose link returned
// 404 or 410 stays closed.
func (s *Store) MarkSeen(ctx context.Context, id, unlistedReason string) error {
	_, err := s.DB.Exec(ctx, `UPDATE jobs SET last_seen_at=now(),
		closed_at=CASE WHEN closed_reason=$2 THEN NULL ELSE closed_at END,
		closed_reason=CASE WHEN closed_reason=$2 THEN '' ELSE closed_reason END WHERE id=$1`, id, unlistedReason)
	return err
}

// RewriteMarkedUpDescriptions passes every stored description that still holds
// HTML tags or entities through rewrite, and reports how many changed. It
// walks the table once by id, so a text rewrite leaves marked up cannot make
// it loop.
func (s *Store) RewriteMarkedUpDescriptions(ctx context.Context, rewrite func(string) string) (int, error) {
	changed, after := 0, "00000000-0000-0000-0000-000000000000"
	for {
		rows, err := s.DB.Query(ctx, `SELECT id,description FROM jobs WHERE id>$1 AND (description ~ '<[a-zA-Z/!][^>]*>' OR description ~ '&([a-zA-Z]+|#[0-9]+);') ORDER BY id LIMIT 500`, after)
		if err != nil {
			return changed, err
		}
		batch, seen := &pgx.Batch{}, 0
		for rows.Next() {
			var id, description string
			if err = rows.Scan(&id, &description); err != nil {
				rows.Close()
				return changed, err
			}
			after, seen = id, seen+1
			if clean := rewrite(description); clean != description {
				batch.Queue(`UPDATE jobs SET description=$2 WHERE id=$1`, id, clean)
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return changed, err
		}
		if batch.Len() > 0 {
			if err = s.DB.SendBatch(ctx, batch).Close(); err != nil {
				return changed, err
			}
			changed += batch.Len()
		}
		if seen < 500 {
			return changed, nil
		}
	}
}

// nameJobSQL gives a job the first free name of: the slug, the slug with the
// start of the job's id, the slug with the whole id. The last contains the
// job's own id, so it is always free.
const nameJobSQL = `UPDATE jobs SET slug=(SELECT c FROM unnest(ARRAY[$2, $2||'-'||left($1::text,8), $2||'-'||$1::text]) WITH ORDINALITY AS t(c,n) WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE slug=t.c AND id<>$1) ORDER BY n LIMIT 1) WHERE id=$1 RETURNING slug`

// nameJob names a job inside the transaction that stores it. Another
// transaction can take the same name between the check and the write; the
// savepoint lets this one fall back to the id-bearing name instead of failing
// the whole insert.
func nameJob(ctx context.Context, tx pgx.Tx, id, candidate string) (string, error) {
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return "", err
	}
	var name string
	if err = savepoint.QueryRow(ctx, nameJobSQL, id, candidate).Scan(&name); err == nil {
		return name, savepoint.Commit(ctx)
	}
	_ = savepoint.Rollback(ctx)
	if !isUniqueViolation(err) {
		return "", err
	}
	name = candidate + "-" + id
	_, err = tx.Exec(ctx, `UPDATE jobs SET slug=$2 WHERE id=$1`, id, name)
	return name, err
}

// NameUnnamedJobs gives a slug to every job stored before slugs existed, in
// batches, and reports how many it named. It runs at startup; once every job
// is named it costs one empty query.
func (s *Store) NameUnnamedJobs(ctx context.Context) (int, error) {
	named := 0
	for {
		rows, err := s.DB.Query(ctx, `SELECT id,apply_url,source_provider,company,title FROM jobs WHERE slug='' ORDER BY created_at LIMIT 500`)
		if err != nil {
			return named, err
		}
		batch := &pgx.Batch{}
		for rows.Next() {
			var id, applyURL, provider, companyName, title string
			if err = rows.Scan(&id, &applyURL, &provider, &companyName, &title); err != nil {
				rows.Close()
				return named, err
			}
			batch.Queue(nameJobSQL, id, slug.Job(applyURL, provider, companyName, title))
		}
		rows.Close()
		if err = rows.Err(); err != nil || batch.Len() == 0 {
			return named, err
		}
		// One implicit transaction per batch, so each name sees the ones
		// given before it and a duplicate gets its id suffix.
		if err = s.DB.SendBatch(ctx, batch).Close(); err != nil {
			return named, err
		}
		named += batch.Len()
	}
}

const upsertJobMatch = `INSERT INTO job_matches(user_id,job_id,filter_id,rejected,score,rejection_reasons,warnings,reasons,matched_skills,missing_skills,red_flags,breakdown) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(user_id,job_id) DO UPDATE SET filter_id=EXCLUDED.filter_id,rejected=EXCLUDED.rejected,score=EXCLUDED.score,rejection_reasons=EXCLUDED.rejection_reasons,warnings=EXCLUDED.warnings,reasons=EXCLUDED.reasons,matched_skills=EXCLUDED.matched_skills,missing_skills=EXCLUDED.missing_skills,red_flags=EXCLUDED.red_flags,breakdown=EXCLUDED.breakdown,created_at=now() RETURNING id`

func jobMatchArgs(u, jobID string, e models.Evaluation, filterID string) []any {
	rej, _ := json.Marshal(e.RejectionReasons)
	warn, _ := json.Marshal(e.Warnings)
	reasons, _ := json.Marshal(e.Reasons)
	matched, _ := json.Marshal(e.MatchedSkills)
	missing, _ := json.Marshal(e.MissingSkills)
	red, _ := json.Marshal(e.RedFlags)
	breakdown, _ := json.Marshal(e.Breakdown)
	var fid any
	if filterID != "" {
		fid = filterID
	}
	return []any{u, jobID, fid, e.Rejected, e.Score, rej, warn, reasons, matched, missing, red, breakdown}
}

func (s *Store) SaveJobMatch(ctx context.Context, u, jobID string, e models.Evaluation, filterID string) (string, error) {
	var id string
	err := s.DB.QueryRow(ctx, upsertJobMatch, jobMatchArgs(u, jobID, e, filterID)...).Scan(&id)
	return id, err
}

// JobEvaluation pairs a job with the evaluation to store for it.
type JobEvaluation struct {
	JobID      string
	Evaluation models.Evaluation
}

// SaveJobMatches stores many evaluations in one round trip. Re-scoring after a
// profile change rewrites every match the user has, and a statement per job
// made adding one skill take seconds once a user had a few thousand matches.
// A batch runs as one implicit transaction, so the scores change together.
// It holds every row until it commits, so rows are written in job id order:
// two re-scores of one user then queue behind each other instead of each
// holding a row the other needs.
func (s *Store) SaveJobMatches(ctx context.Context, u, filterID string, evaluations []JobEvaluation) error {
	sort.Slice(evaluations, func(i, j int) bool { return evaluations[i].JobID < evaluations[j].JobID })
	batch := &pgx.Batch{}
	for _, item := range evaluations {
		batch.Queue(upsertJobMatch, jobMatchArgs(u, item.JobID, item.Evaluation, filterID)...)
	}
	return s.DB.SendBatch(ctx, batch).Close()
}

func (s *Store) Jobs(ctx context.Context, u string, onlyMatches bool) ([]models.JobWithMatch, error) {
	q := `SELECT j.id,j.title,j.company,j.company_normalized,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at,j.created_at,COALESCE(j.source_id::text,''),j.external_id,j.discovered_at,j.referral_partner,j.referral_disclosure,j.source_name,j.source_provider,m.id,m.rejected,m.score,m.rejection_reasons,m.warnings,m.reasons,m.matched_skills,m.missing_skills,m.red_flags,m.breakdown,j.slug,j.closed_at,j.closed_reason FROM jobs j JOIN job_matches m ON m.job_id=j.id WHERE m.user_id=$1`
	if onlyMatches {
		q += ` AND m.rejected=false`
	}
	// Closed postings sink to the bottom: they stay visible for the record but
	// can no longer be applied to.
	q += ` ORDER BY (j.closed_at IS NOT NULL) ASC,m.rejected ASC,m.score DESC,j.created_at DESC`
	rows, err := s.DB.Query(ctx, q, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.JobWithMatch{}
	for rows.Next() {
		x, err := scanJobMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// uuidPattern tells a job id from a slug in a URL; ids keep working so older
// links and bookmarks still open.
var uuidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// jobRefColumn is the column a job reference from a URL is looked up by.
func jobRefColumn(ref string) string {
	if uuidPattern.MatchString(ref) {
		return "j.id"
	}
	return "j.slug"
}

// Job finds one of the user's jobs by id or by slug.
func (s *Store) Job(ctx context.Context, u, ref string) (models.JobWithMatch, error) {
	row := s.DB.QueryRow(ctx, `SELECT j.id,j.title,j.company,j.company_normalized,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at,j.created_at,COALESCE(j.source_id::text,''),j.external_id,j.discovered_at,j.referral_partner,j.referral_disclosure,j.source_name,j.source_provider,m.id,m.rejected,m.score,m.rejection_reasons,m.warnings,m.reasons,m.matched_skills,m.missing_skills,m.red_flags,m.breakdown,j.slug,j.closed_at,j.closed_reason FROM jobs j JOIN job_matches m ON m.job_id=j.id WHERE m.user_id=$1 AND `+jobRefColumn(ref)+`=$2`, u, ref)
	return scanJobMatch(row)
}

func (s *Store) Match(ctx context.Context, u, id string) (models.JobWithMatch, error) {
	row := s.DB.QueryRow(ctx, `SELECT j.id,j.title,j.company,j.company_normalized,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at,j.created_at,COALESCE(j.source_id::text,''),j.external_id,j.discovered_at,j.referral_partner,j.referral_disclosure,j.source_name,j.source_provider,m.id,m.rejected,m.score,m.rejection_reasons,m.warnings,m.reasons,m.matched_skills,m.missing_skills,m.red_flags,m.breakdown,j.slug,j.closed_at,j.closed_reason FROM jobs j JOIN job_matches m ON m.job_id=j.id WHERE m.user_id=$1 AND m.id=$2`, u, id)
	return scanJobMatch(row)
}

type scanner interface{ Scan(...any) error }

func scanJobMatch(row scanner) (models.JobWithMatch, error) {
	var x models.JobWithMatch
	var rejection, warnings, reasons, matched, missing, red, breakdown []byte
	err := row.Scan(&x.Job.ID, &x.Job.Title, &x.Job.Company, &x.Job.CompanyNormalized, &x.Job.CompanyDomain, &x.Job.Location, &x.Job.RemoteType, &x.Job.SalaryMin, &x.Job.SalaryMax, &x.Job.Currency, &x.Job.EmploymentType, &x.Job.Industry, &x.Job.Description, &x.Job.ApplyURL, &x.Job.PostedAt, &x.Job.CreatedAt, &x.Job.SourceID, &x.Job.ExternalID, &x.Job.DiscoveredAt, &x.Job.ReferralPartner, &x.Job.ReferralDisclosure, &x.Job.SourceName, &x.Job.SourceProvider, &x.MatchID, &x.Match.Rejected, &x.Match.Score, &rejection, &warnings, &reasons, &matched, &missing, &red, &breakdown, &x.Job.Slug, &x.Job.ClosedAt, &x.Job.ClosedReason)
	if err != nil {
		return x, err
	}
	json.Unmarshal(rejection, &x.Match.RejectionReasons)
	json.Unmarshal(warnings, &x.Match.Warnings)
	json.Unmarshal(reasons, &x.Match.Reasons)
	json.Unmarshal(matched, &x.Match.MatchedSkills)
	json.Unmarshal(missing, &x.Match.MissingSkills)
	json.Unmarshal(red, &x.Match.RedFlags)
	json.Unmarshal(breakdown, &x.Match.Breakdown)
	// Matches scored before the matcher stopped producing nil lists hold JSON
	// null, which decodes to nil and would be served as null again.
	for _, list := range []*[]string{&x.Match.RejectionReasons, &x.Match.Warnings, &x.Match.Reasons,
		&x.Match.MatchedSkills, &x.Match.MissingSkills, &x.Match.RedFlags} {
		if *list == nil {
			*list = []string{}
		}
	}
	// A rejected match cannot be applied to, so the apply URL is withheld
	// here rather than in each client. The value returned by this helper is
	// therefore a read model: never write it back to the catalog row.
	if x.Match.Rejected {
		x.Job.ApplyURL = ""
		// The slug is made from the end of that URL, so it goes too; the job
		// stays reachable by id.
		x.Job.Slug = ""
	}
	return x, nil
}

func (s *Store) Applications(ctx context.Context, u string) ([]models.Application, error) {
	rows, err := s.DB.Query(ctx, `SELECT a.id,a.job_id,a.status,a.notes,a.created_at,a.updated_at,j.id,j.title,j.company,j.company_normalized,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at,j.created_at,COALESCE(m.rejected,false) FROM applications a JOIN jobs j ON j.id=a.job_id LEFT JOIN job_matches m ON m.job_id=a.job_id AND m.user_id=a.user_id WHERE a.user_id=$1 ORDER BY a.updated_at DESC`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Application{}
	for rows.Next() {
		x, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) Application(ctx context.Context, u, id string) (models.Application, error) {
	return scanApplication(s.DB.QueryRow(ctx, `SELECT a.id,a.job_id,a.status,a.notes,a.created_at,a.updated_at,j.id,j.title,j.company,j.company_normalized,j.company_domain,j.location,j.remote_type,j.salary_min,j.salary_max,j.currency,j.employment_type,j.industry,j.description,j.apply_url,j.posted_at,j.created_at,COALESCE(m.rejected,false) FROM applications a JOIN jobs j ON j.id=a.job_id LEFT JOIN job_matches m ON m.job_id=a.job_id AND m.user_id=a.user_id WHERE a.user_id=$1 AND a.id=$2`, u, id))
}
func scanApplication(row scanner) (models.Application, error) {
	var a models.Application
	var j models.Job
	var rejected bool
	err := row.Scan(&a.ID, &a.JobID, &a.Status, &a.Notes, &a.CreatedAt, &a.UpdatedAt, &j.ID, &j.Title, &j.Company, &j.CompanyNormalized, &j.CompanyDomain, &j.Location, &j.RemoteType, &j.SalaryMin, &j.SalaryMax, &j.Currency, &j.EmploymentType, &j.Industry, &j.Description, &j.ApplyURL, &j.PostedAt, &j.CreatedAt, &rejected)
	if rejected {
		j.ApplyURL = ""
	}
	a.Job = &j
	return a, err
}
func (s *Store) CreateApplication(ctx context.Context, u, jobID string) (models.Application, error) {
	var a models.Application
	err := s.DB.QueryRow(ctx, `INSERT INTO applications(user_id,job_id,status) SELECT $1,$2,'draft' WHERE EXISTS(SELECT 1 FROM job_matches WHERE job_id=$2 AND user_id=$1 AND rejected=false) ON CONFLICT(user_id,job_id) DO UPDATE SET updated_at=applications.updated_at RETURNING id,job_id,status,notes,created_at,updated_at`, u, jobID).Scan(&a.ID, &a.JobID, &a.Status, &a.Notes, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var rejected bool
		checkErr := s.DB.QueryRow(ctx, `SELECT rejected FROM job_matches WHERE user_id=$1 AND job_id=$2`, u, jobID).Scan(&rejected)
		if checkErr == nil && rejected {
			return a, ErrApplicationBlocked
		}
		return a, pgx.ErrNoRows
	}
	if err == nil {
		_, _ = s.DB.Exec(ctx, `INSERT INTO application_events(user_id,application_id,event_type,to_status) VALUES($1,$2,'created','draft')`, u, a.ID)
	}
	return a, err
}

// UpdateApplication changes an application's status and, when notes is
// non-nil, replaces its notes. A nil notes keeps the stored text, so a caller
// that only moves the status cannot silently erase what the user wrote.
func (s *Store) UpdateApplication(ctx context.Context, u, id, status string, notes *string) (models.Application, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return models.Application{}, err
	}
	defer tx.Rollback(ctx)
	var old string
	if err = tx.QueryRow(ctx, `SELECT status FROM applications WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, u).Scan(&old); err != nil {
		return models.Application{}, err
	}
	if status == "" {
		status = old
	}
	if status == "applied" && old != "applied" {
		var eligible bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM applications a JOIN job_matches m ON m.job_id=a.job_id AND m.user_id=a.user_id WHERE a.id=$1 AND a.user_id=$2 AND m.rejected=false)`, id, u).Scan(&eligible); err != nil {
			return models.Application{}, err
		}
		if !eligible {
			return models.Application{}, ErrApplicationBlocked
		}
	}
	var a models.Application
	err = tx.QueryRow(ctx, `UPDATE applications SET status=$3,notes=COALESCE($4,notes),updated_at=now() WHERE id=$1 AND user_id=$2 RETURNING id,job_id,status,notes,created_at,updated_at`, id, u, status, notes).Scan(&a.ID, &a.JobID, &a.Status, &a.Notes, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	if old != status {
		_, err = tx.Exec(ctx, `INSERT INTO application_events(user_id,application_id,event_type,from_status,to_status) VALUES($1,$2,'status_changed',$3,$4)`, u, id, old, status)
	}
	if err != nil {
		return a, err
	}
	return a, tx.Commit(ctx)
}

func (s *Store) SaveDocuments(ctx context.Context, u, jobID string, d models.Documents) error {
	all := map[string]string{"recruiter_message": d.RecruiterMessage, "cover_letter": d.CoverLetter, "candidate_summary": d.CandidateSummary}
	answers, _ := json.Marshal(d.Answers)
	all["common_answers"] = string(answers)
	for typ, content := range all {
		_, err := s.DB.Exec(ctx, `INSERT INTO application_documents(user_id,application_id,job_id,document_type,content) VALUES($1,(SELECT id FROM applications WHERE user_id=$1 AND job_id=$2),$2,$3,$4) ON CONFLICT(user_id,job_id,document_type) DO UPDATE SET content=EXCLUDED.content,application_id=EXCLUDED.application_id,updated_at=now()`, u, jobID, typ, content)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Documents(ctx context.Context, u, jobID string) (models.Documents, error) {
	rows, err := s.DB.Query(ctx, `SELECT document_type,content FROM application_documents WHERE user_id=$1 AND job_id=$2`, u, jobID)
	if err != nil {
		return models.Documents{}, err
	}
	defer rows.Close()
	d := models.Documents{Answers: map[string]string{}}
	for rows.Next() {
		var typ, content string
		if err = rows.Scan(&typ, &content); err != nil {
			return d, err
		}
		switch typ {
		case "recruiter_message":
			d.RecruiterMessage = content
		case "cover_letter":
			d.CoverLetter = content
		case "candidate_summary":
			d.CandidateSummary = content
		case "common_answers":
			json.Unmarshal([]byte(content), &d.Answers)
		}
	}
	return d, rows.Err()
}

func (s *Store) RecordOutbound(ctx context.Context, u, jobID string) (string, string, string, error) {
	var destination, partner, disclosure string
	err := s.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(j.partner_apply_url,''),j.apply_url),j.referral_partner,j.referral_disclosure FROM jobs j JOIN job_matches m ON m.job_id=j.id WHERE j.id=$1 AND m.user_id=$2 AND m.rejected=false`, jobID, u).Scan(&destination, &partner, &disclosure)
	if errors.Is(err, pgx.ErrNoRows) {
		var rejected bool
		checkErr := s.DB.QueryRow(ctx, `SELECT rejected FROM job_matches WHERE user_id=$1 AND job_id=$2`, u, jobID).Scan(&rejected)
		if checkErr == nil && rejected {
			return "", "", "", ErrApplicationBlocked
		}
	}
	if err != nil {
		return "", "", "", err
	}
	if destination == "" {
		return "", "", "", errors.New("this job has no apply URL")
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO outbound_clicks(user_id,job_id,application_id,destination_url,referral_partner) VALUES($1,$2,(SELECT id FROM applications WHERE user_id=$1 AND job_id=$2),$3,$4)`, u, jobID, destination, partner)
	return destination, partner, disclosure, err
}

func (s *Store) DiscoverySettings(ctx context.Context, u string) (models.DiscoverySettings, error) {
	var x models.DiscoverySettings
	err := s.DB.QueryRow(ctx, `SELECT enabled,interval_minutes,auto_prepare,min_score,max_jobs_per_run,last_run_at,next_run_at FROM discovery_settings WHERE user_id=$1`, u).Scan(&x.Enabled, &x.IntervalMinutes, &x.AutoPrepare, &x.MinScore, &x.MaxJobsPerRun, &x.LastRunAt, &x.NextRunAt)
	if errors.Is(err, pgx.ErrNoRows) {
		x = models.DiscoverySettings{Enabled: false, IntervalMinutes: 360, AutoPrepare: true, MinScore: 75, MaxJobsPerRun: 100}
		err = nil
	}
	return x, err
}
func (s *Store) SaveDiscoverySettings(ctx context.Context, u string, x models.DiscoverySettings) (models.DiscoverySettings, error) {
	err := s.DB.QueryRow(ctx, `INSERT INTO discovery_settings(user_id,enabled,interval_minutes,auto_prepare,min_score,max_jobs_per_run,next_run_at) VALUES($1,$2,$3,$4,$5,$6,CASE WHEN $2 THEN now() ELSE NULL END) ON CONFLICT(user_id) DO UPDATE SET enabled=EXCLUDED.enabled,interval_minutes=EXCLUDED.interval_minutes,auto_prepare=EXCLUDED.auto_prepare,min_score=EXCLUDED.min_score,max_jobs_per_run=EXCLUDED.max_jobs_per_run,next_run_at=CASE WHEN EXCLUDED.enabled THEN COALESCE(discovery_settings.next_run_at,now()) ELSE NULL END,updated_at=now() RETURNING enabled,interval_minutes,auto_prepare,min_score,max_jobs_per_run,last_run_at,next_run_at`, u, x.Enabled, x.IntervalMinutes, x.AutoPrepare, x.MinScore, x.MaxJobsPerRun).Scan(&x.Enabled, &x.IntervalMinutes, &x.AutoPrepare, &x.MinScore, &x.MaxJobsPerRun, &x.LastRunAt, &x.NextRunAt)
	return x, err
}
func (s *Store) ClaimDueDiscoveryUsers(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.DB.Query(ctx, `WITH due AS (SELECT user_id FROM discovery_settings WHERE enabled=true AND COALESCE(next_run_at,now())<=now() ORDER BY next_run_at NULLS FIRST FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE discovery_settings s SET next_run_at=now()+(s.interval_minutes||' minutes')::interval FROM due WHERE s.user_id=due.user_id RETURNING s.user_id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) TouchDiscoverySchedule(ctx context.Context, u string) error {
	_, err := s.DB.Exec(ctx, `UPDATE discovery_settings SET last_run_at=now(),next_run_at=CASE WHEN enabled THEN now()+(interval_minutes||' minutes')::interval ELSE NULL END,updated_at=now() WHERE user_id=$1`, u)
	return err
}
func (s *Store) DiscoverySources(ctx context.Context, u string, activeOnly bool) ([]models.DiscoverySource, error) {
	q := `SELECT id,provider,name,identifier,search_query,excluded_search_query,active,last_crawled_at,last_error,created_at FROM discovery_sources WHERE user_id=$1`
	if activeOnly {
		q += ` AND active=true`
	}
	q += ` ORDER BY created_at`
	rows, err := s.DB.Query(ctx, q, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DiscoverySource{}
	for rows.Next() {
		var x models.DiscoverySource
		if err = rows.Scan(&x.ID, &x.Provider, &x.Name, &x.Identifier, &x.SearchQuery, &x.ExcludedSearchQuery, &x.Active, &x.LastCrawledAt, &x.LastError, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) AddDiscoverySource(ctx context.Context, u string, x models.DiscoverySource) (models.DiscoverySource, error) {
	err := s.DB.QueryRow(ctx, `INSERT INTO discovery_sources(user_id,provider,name,identifier,search_query,excluded_search_query,active) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id,provider,identifier,search_query,excluded_search_query) DO UPDATE SET name=EXCLUDED.name,active=EXCLUDED.active,last_error='' RETURNING id,provider,name,identifier,search_query,excluded_search_query,active,last_crawled_at,last_error,created_at`, u, x.Provider, x.Name, x.Identifier, x.SearchQuery, x.ExcludedSearchQuery, x.Active).Scan(&x.ID, &x.Provider, &x.Name, &x.Identifier, &x.SearchQuery, &x.ExcludedSearchQuery, &x.Active, &x.LastCrawledAt, &x.LastError, &x.CreatedAt)
	if isUniqueViolation(err) {
		return x, ErrDuplicateDiscoverySource
	}
	return x, err
}
func (s *Store) UpdateDiscoverySource(ctx context.Context, u, id string, x models.DiscoverySource) (models.DiscoverySource, error) {
	err := s.DB.QueryRow(ctx, `UPDATE discovery_sources SET provider=$3,name=$4,identifier=$5,search_query=$6,excluded_search_query=$7,active=$8,last_error='' WHERE id=$1 AND user_id=$2 RETURNING id,provider,name,identifier,search_query,excluded_search_query,active,last_crawled_at,last_error,created_at`, id, u, x.Provider, x.Name, x.Identifier, x.SearchQuery, x.ExcludedSearchQuery, x.Active).Scan(&x.ID, &x.Provider, &x.Name, &x.Identifier, &x.SearchQuery, &x.ExcludedSearchQuery, &x.Active, &x.LastCrawledAt, &x.LastError, &x.CreatedAt)
	if isUniqueViolation(err) {
		return x, ErrDuplicateDiscoverySource
	}
	return x, err
}
func (s *Store) EnsureRecommendedSource(ctx context.Context, u, query string) (models.DiscoverySource, error) {
	x := models.DiscoverySource{Provider: "remotive", Name: "Profile search: " + query, SearchQuery: query, Active: true}
	err := s.DB.QueryRow(ctx, `INSERT INTO discovery_sources(user_id,provider,name,identifier,search_query,excluded_search_query,active) VALUES($1,'remotive',$2,'',$3,'',true) ON CONFLICT(user_id,provider,identifier,search_query,excluded_search_query) DO UPDATE SET name=EXCLUDED.name,active=true RETURNING id,provider,name,identifier,search_query,excluded_search_query,active,last_crawled_at,last_error,created_at`, u, x.Name, query).Scan(&x.ID, &x.Provider, &x.Name, &x.Identifier, &x.SearchQuery, &x.ExcludedSearchQuery, &x.Active, &x.LastCrawledAt, &x.LastError, &x.CreatedAt)
	return x, err
}
func (s *Store) DeleteDiscoverySource(ctx context.Context, u, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM discovery_sources WHERE id=$1 AND user_id=$2`, id, u)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}
func (s *Store) UpdateDiscoverySourceResult(ctx context.Context, u, id, message string) error {
	_, err := s.DB.Exec(ctx, `UPDATE discovery_sources SET last_crawled_at=now(),last_error=$3 WHERE id=$1 AND user_id=$2`, id, u, message)
	return err
}
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
func (s *Store) StartDiscoveryRun(ctx context.Context, u string) (models.DiscoveryRun, error) {
	var x models.DiscoveryRun
	err := s.DB.QueryRow(ctx, `INSERT INTO discovery_runs(user_id,status) VALUES($1,'running') RETURNING id,status,jobs_seen,jobs_added,jobs_rejected,applications_prepared,duplicates_skipped,error_message,started_at,completed_at`, u).Scan(&x.ID, &x.Status, &x.JobsSeen, &x.JobsAdded, &x.JobsRejected, &x.ApplicationsPrepared, &x.DuplicatesSkipped, &x.ErrorMessage, &x.StartedAt, &x.CompletedAt)
	return x, err
}
func (s *Store) FinishDiscoveryRun(ctx context.Context, u string, x models.DiscoveryRun, runErr error) (models.DiscoveryRun, error) {
	status := "completed"
	message := ""
	if runErr != nil {
		status = "failed"
		message = runErr.Error()
	}
	err := s.DB.QueryRow(ctx, `UPDATE discovery_runs SET status=$3,jobs_seen=$4,jobs_added=$5,jobs_rejected=$6,applications_prepared=$7,duplicates_skipped=$8,error_message=$9,completed_at=now() WHERE id=$1 AND user_id=$2 RETURNING status,error_message,completed_at`, x.ID, u, status, x.JobsSeen, x.JobsAdded, x.JobsRejected, x.ApplicationsPrepared, x.DuplicatesSkipped, message).Scan(&x.Status, &x.ErrorMessage, &x.CompletedAt)
	return x, err
}
func (s *Store) DiscoveryRuns(ctx context.Context, u string) ([]models.DiscoveryRun, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,status,jobs_seen,jobs_added,jobs_rejected,applications_prepared,duplicates_skipped,error_message,started_at,completed_at FROM discovery_runs WHERE user_id=$1 ORDER BY started_at DESC LIMIT 20`, u)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DiscoveryRun{}
	for rows.Next() {
		var x models.DiscoveryRun
		if err = rows.Scan(&x.ID, &x.Status, &x.JobsSeen, &x.JobsAdded, &x.JobsRejected, &x.ApplicationsPrepared, &x.DuplicatesSkipped, &x.ErrorMessage, &x.StartedAt, &x.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Dashboard(ctx context.Context, u string) (map[string]any, error) {
	out := map[string]any{}
	var profileFields, total, blacklistRejected, filterRejected, good, draft, applied int
	err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM job_matches WHERE user_id=$1),(SELECT count(*) FROM job_matches WHERE user_id=$1 AND rejected=true AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(rejection_reasons) x WHERE x ILIKE '%employer%' OR x ILIKE '%blacklist%' OR x ILIKE '%domain%')),(SELECT count(*) FROM job_matches WHERE user_id=$1 AND rejected=true AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(rejection_reasons) x WHERE x ILIKE '%employer%' OR x ILIKE '%blacklist%' OR x ILIKE '%domain%')),(SELECT count(*) FROM job_matches WHERE user_id=$1 AND rejected=false AND score>=70),(SELECT count(*) FROM applications WHERE user_id=$1 AND status IN ('draft','ready')),(SELECT count(*) FROM applications WHERE user_id=$1 AND status IN ('applied','interview','offer','rejected'))`, u).Scan(&total, &blacklistRejected, &filterRejected, &good, &draft, &applied)
	if err != nil {
		return nil, err
	}
	p, err := s.Profile(ctx, u)
	if err != nil {
		return nil, err
	}
	checks := []bool{p.Name != "", p.Email != "", p.Phone != "", p.Location != "", p.CurrentTitle != "", len(p.DesiredTitles) > 0, len(p.Seniorities) > 0, p.YearsExperience > 0, len(p.Skills) > 0, len(p.Experiences) > 0, p.WorkAuthorization != "", p.NoticePeriod != ""}
	for _, ok := range checks {
		if ok {
			profileFields++
		}
	}
	matches, err := s.Jobs(ctx, u, true)
	if err != nil {
		return nil, err
	}
	// Recommendations are for jobs that can still be applied to.
	jobs := []models.JobWithMatch{}
	for _, x := range matches {
		if x.Job.ClosedAt == nil && len(jobs) < 5 {
			jobs = append(jobs, x)
		}
	}
	out["profile_completeness"] = profileFields * 100 / len(checks)
	out["jobs_imported"] = total
	out["jobs_rejected_blacklist"] = blacklistRejected
	out["jobs_rejected_filters"] = filterRejected
	out["good_matches"] = good
	out["applications_drafted"] = draft
	out["applications_applied"] = applied
	out["top_jobs"] = jobs
	actions := []string{}
	if profileFields < len(checks) {
		actions = append(actions, "Complete your candidate profile")
	}
	if total == 0 {
		actions = append(actions, "Import your first job")
	}
	if good > 0 {
		actions = append(actions, "Review your strongest job matches")
	}
	if draft > 0 {
		actions = append(actions, "Finish and submit drafted applications")
	}
	if len(actions) == 0 {
		actions = append(actions, "Import another role to keep your pipeline moving")
	}
	out["next_actions"] = actions
	return out, nil
}

func (s *Store) SeedFirstUser(ctx context.Context) error {
	var u string
	if err := s.DB.QueryRow(ctx, `SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&u); err != nil {
		return fmt.Errorf("register a user before seeding: %w", err)
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO user_profiles(user_id,name,email,current_title,desired_titles,seniority,years_experience,currency,work_preference,preferred_countries,work_authorization,notice_period) SELECT id,'Demo Candidate',email,'Backend Engineer',ARRAY['Backend Engineer','Go Developer'],'senior',7,'EUR',ARRAY['remote'],ARRAY['Romania','Germany'],'EU citizen','30 days' FROM users WHERE id=$1 ON CONFLICT(user_id) DO NOTHING`, u)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO skills(user_id,name,level,years) VALUES($1,'Go','advanced',5),($1,'PostgreSQL','advanced',5),($1,'TypeScript','intermediate',3) ON CONFLICT DO NOTHING`, u)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `INSERT INTO work_experiences(user_id,company,title,current,description) SELECT $1,'CurrentCo','Senior Backend Engineer',true,'Built reliable backend services.' WHERE NOT EXISTS(SELECT 1 FROM work_experiences WHERE user_id=$1)`, u)
	return err
}

// LookupCompletion implements llm.Cache. A miss is not an error.
func (s *Store) LookupCompletion(ctx context.Context, key string) (string, bool, error) {
	var response string
	err := s.DB.QueryRow(ctx, `UPDATE llm_cache SET hits=hits+1,last_used_at=now() WHERE cache_key=$1 RETURNING response`, key).Scan(&response)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return response, true, nil
}

// StoreCompletion implements llm.Cache.
func (s *Store) StoreCompletion(ctx context.Context, key, task, provider, model, response string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO llm_cache(cache_key,task,provider,model,response,hits) VALUES($1,$2,$3,$4,$5,1)
		ON CONFLICT (cache_key) DO UPDATE SET response=EXCLUDED.response,last_used_at=now()`, key, task, provider, model, response)
	return err
}
