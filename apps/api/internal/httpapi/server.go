package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/matching"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

type Server struct {
	Store       *store.Store
	Discovery   *discovery.Service
	LLM         *llm.Client
	Secret      string
	TokenTTL    time.Duration
	FrontendURL string
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("POST /api/auth/register", s.register)
	m.HandleFunc("POST /api/auth/login", s.login)
	p := func(h http.HandlerFunc) http.Handler { return auth.Middleware(s.Secret, h) }
	m.Handle("GET /api/ai/status", p(s.aiStatus))
	m.Handle("GET /api/answers", p(s.listAnswers))
	m.Handle("POST /api/answers", p(s.saveAnswer))
	m.Handle("PATCH /api/answers/{id}", p(s.updateAnswer))
	m.Handle("DELETE /api/answers/{id}", p(s.deleteAnswer))
	m.Handle("POST /api/jobs/{id}/answers", p(s.resolveJobAnswers))
	m.Handle("GET /api/profile", p(s.getProfile))
	m.Handle("PUT /api/profile", p(s.putProfile))
	m.Handle("GET /api/experiences", p(s.listExperiences))
	m.Handle("POST /api/experiences", p(s.addExperience))
	m.Handle("DELETE /api/experiences/{id}", p(s.deleteExperience))
	m.Handle("GET /api/skills", p(s.listSkills))
	m.Handle("POST /api/skills", p(s.addSkill))
	m.Handle("DELETE /api/skills/{id}", p(s.deleteSkill))
	m.Handle("GET /api/projects", p(s.listProjects))
	m.Handle("POST /api/projects", p(s.addProject))
	m.Handle("DELETE /api/projects/{id}", p(s.deleteProject))
	m.Handle("GET /api/blacklist", p(s.listBlacklist))
	m.Handle("POST /api/blacklist", p(s.addBlacklist))
	m.Handle("DELETE /api/blacklist/{id}", p(s.deleteBlacklist))
	m.Handle("GET /api/filters", p(s.listFilters))
	m.Handle("POST /api/filters", p(s.saveFilter))
	m.Handle("PUT /api/filters/{id}", p(s.saveFilter))
	m.Handle("DELETE /api/filters/{id}", p(s.deleteFilter))
	m.Handle("GET /api/jobs", p(s.listJobs))
	m.Handle("POST /api/jobs", p(s.importJob))
	m.Handle("GET /api/jobs/{id}", p(s.getJob))
	m.Handle("POST /api/jobs/{id}/generate", p(s.generate))
	m.Handle("GET /api/matches", p(s.listMatches))
	m.Handle("GET /api/matches/{id}", p(s.getMatch))
	m.Handle("GET /api/applications", p(s.listApplications))
	m.Handle("POST /api/applications", p(s.createApplication))
	m.Handle("GET /api/applications/{id}", p(s.getApplication))
	m.Handle("PATCH /api/applications/{id}", p(s.updateApplication))
	m.Handle("GET /api/dashboard", p(s.dashboard))
	m.Handle("GET /api/discovery/settings", p(s.discoverySettings))
	m.Handle("PUT /api/discovery/settings", p(s.saveDiscoverySettings))
	m.Handle("GET /api/discovery/sources", p(s.discoverySources))
	m.Handle("POST /api/discovery/sources", p(s.addDiscoverySource))
	m.Handle("POST /api/discovery/validate", p(s.validateDiscoverySource))
	m.Handle("DELETE /api/discovery/sources/{id}", p(s.deleteDiscoverySource))
	m.Handle("GET /api/discovery/runs", p(s.discoveryRuns))
	m.Handle("POST /api/discovery/run", p(s.runDiscovery))
	m.Handle("POST /api/discovery/recommended", p(s.runRecommendedDiscovery))
	m.Handle("POST /api/jobs/{id}/outbound", p(s.outbound))
	return s.cors(m)
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.FrontendURL)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var x credentials
	if !decode(w, r, &x) {
		return
	}
	x.Email = strings.TrimSpace(strings.ToLower(x.Email))
	if _, err := mail.ParseAddress(x.Email); err != nil || len(x.Password) < 8 {
		problem(w, 422, "Enter a valid email and a password of at least 8 characters.")
		return
	}
	hash, err := auth.Hash(x.Password)
	if err != nil {
		internal(w, err)
		return
	}
	u, err := s.Store.CreateUser(r.Context(), x.Email, hash)
	if err != nil {
		problem(w, 409, "An account with this email already exists.")
		return
	}
	tok, _ := auth.Token(u.ID, u.Email, s.Secret, s.TokenTTL)
	write(w, 201, map[string]any{"token": tok, "user": u})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var x credentials
	if !decode(w, r, &x) {
		return
	}
	u, hash, err := s.Store.Credentials(r.Context(), x.Email)
	if err != nil || !auth.Verify(hash, x.Password) {
		problem(w, 401, "Invalid email or password.")
		return
	}
	tok, _ := auth.Token(u.ID, u.Email, s.Secret, s.TokenTTL)
	write(w, 200, map[string]any{"token": tok, "user": u})
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	x, err := s.Store.Profile(r.Context(), auth.UserID(r.Context()))
	respond(w, x, err)
}
func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	var x models.Profile
	if !decode(w, r, &x) {
		return
	}
	if x.Email != "" {
		if _, err := mail.ParseAddress(x.Email); err != nil {
			problem(w, 422, "Profile email is invalid.")
			return
		}
	}
	if x.YearsExperience < 0 || x.SalaryExpectation < 0 || x.RateExpectation < 0 {
		problem(w, 422, "Experience and compensation values cannot be negative.")
		return
	}
	x.DesiredTitles = nonNil(x.DesiredTitles)
	if len(x.Seniorities) == 0 && strings.TrimSpace(x.Seniority) != "" {
		x.Seniorities = []string{x.Seniority}
	}
	allowedSeniorities := map[string]bool{"intern": true, "junior": true, "mid": true, "senior": true, "lead": true, "staff": true, "principal": true, "manager": true, "director": true}
	seenSeniorities := map[string]bool{}
	normalizedSeniorities := make([]string, 0, len(x.Seniorities))
	for _, value := range x.Seniorities {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowedSeniorities[value] {
			problem(w, 422, "Choose one or more valid seniority levels.")
			return
		}
		if !seenSeniorities[value] {
			seenSeniorities[value] = true
			normalizedSeniorities = append(normalizedSeniorities, value)
		}
	}
	x.Seniorities = normalizedSeniorities
	x.Seniority = ""
	if len(x.Seniorities) > 0 {
		x.Seniority = x.Seniorities[0]
	}
	x.Certifications = nonNil(x.Certifications)
	x.Languages = nonNil(x.Languages)
	x.WorkPreference = nonNil(x.WorkPreference)
	x.PreferredCountries = nonNil(x.PreferredCountries)
	x.PreferredTimezones = nonNil(x.PreferredTimezones)
	x.IndustryPreferences = nonNil(x.IndustryPreferences)
	err := s.Store.SaveProfile(r.Context(), auth.UserID(r.Context()), x)
	if err == nil {
		err = s.reevaluateJobs(r)
	}
	if err == nil {
		write(w, 200, x)
	} else {
		internal(w, err)
	}
}
func (s *Server) listExperiences(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Experiences(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) addExperience(w http.ResponseWriter, r *http.Request) {
	var x models.WorkExperience
	if !decode(w, r, &x) {
		return
	}
	if strings.TrimSpace(x.Company) == "" || strings.TrimSpace(x.Title) == "" {
		problem(w, 422, "Company and title are required.")
		return
	}
	x, e := s.Store.AddExperience(r.Context(), auth.UserID(r.Context()), x)
	respondCreated(w, x, e)
}
func (s *Server) deleteExperience(w http.ResponseWriter, r *http.Request) {
	s.delete(w, r, "work_experiences")
}
func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Skills(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) addSkill(w http.ResponseWriter, r *http.Request) {
	var x models.Skill
	if !decode(w, r, &x) {
		return
	}
	if strings.TrimSpace(x.Name) == "" || x.Years < 0 {
		problem(w, 422, "Skill name is required and years cannot be negative.")
		return
	}
	x, e := s.Store.AddSkill(r.Context(), auth.UserID(r.Context()), x)
	respondCreated(w, x, e)
}
func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) { s.delete(w, r, "skills") }
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Projects(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	var x models.Project
	if !decode(w, r, &x) {
		return
	}
	if strings.TrimSpace(x.Name) == "" {
		problem(w, 422, "Project name is required.")
		return
	}
	x.Skills = nonNil(x.Skills)
	x, e := s.Store.AddProject(r.Context(), auth.UserID(r.Context()), x)
	respondCreated(w, x, e)
}
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) { s.delete(w, r, "projects") }
func (s *Server) listBlacklist(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Blacklist(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) addBlacklist(w http.ResponseWriter, r *http.Request) {
	var x models.BlacklistEntry
	if !decode(w, r, &x) {
		return
	}
	if strings.TrimSpace(x.CompanyName) == "" {
		problem(w, 422, "Company name is required.")
		return
	}
	x, e := s.Store.AddBlacklist(r.Context(), auth.UserID(r.Context()), x)
	if e == nil {
		e = s.reevaluateJobs(r)
	}
	respondCreated(w, x, e)
}
func (s *Server) deleteBlacklist(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteOwned(r.Context(), "company_blacklist", auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Excluded company not found.")
		return
	}
	if err == nil {
		err = s.reevaluateJobs(r)
	}
	if err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) delete(w http.ResponseWriter, r *http.Request, table string) {
	err := s.Store.DeleteOwned(r.Context(), table, auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Record not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listFilters(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Filters(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) saveFilter(w http.ResponseWriter, r *http.Request) {
	var x models.FilterPreset
	if !decode(w, r, &x) {
		return
	}
	x.ID = r.PathValue("id")
	if strings.TrimSpace(x.Name) == "" || x.MinSalary < 0 || x.MaxAgeDays < 0 {
		problem(w, 422, "Filter name is required and numeric limits cannot be negative.")
		return
	}
	x.IncludedTitles = nonNil(x.IncludedTitles)
	x.ExcludedTitleWords = nonNil(x.ExcludedTitleWords)
	x.Locations = nonNil(x.Locations)
	x.RemoteTypes = nonNil(x.RemoteTypes)
	x.EmploymentTypes = nonNil(x.EmploymentTypes)
	x.ExcludedIndustries = nonNil(x.ExcludedIndustries)
	x.RequiredSkills = nonNil(x.RequiredSkills)
	x.IncludedTechnologies = nonNil(x.IncludedTechnologies)
	x.ExcludedTechnologies = nonNil(x.ExcludedTechnologies)
	x, e := s.Store.SaveFilter(r.Context(), auth.UserID(r.Context()), x)
	if e == nil {
		e = s.reevaluateJobs(r)
	}
	if r.Method == http.MethodPost {
		respondCreated(w, x, e)
	} else {
		respond(w, x, e)
	}
}
func (s *Server) deleteFilter(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteOwned(r.Context(), "filter_presets", auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Filter not found.")
		return
	}
	if err == nil {
		err = s.reevaluateJobs(r)
	}
	if err != nil {
		internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) reevaluateJobs(r *http.Request) error {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		return err
	}
	blacklist, err := s.Store.Blacklist(ctx, uid)
	if err != nil {
		return err
	}
	filter, err := s.Store.ActiveFilter(ctx, uid)
	if err != nil {
		return err
	}
	filterID := ""
	if filter != nil {
		filterID = filter.ID
	}
	jobs, err := s.Store.Jobs(ctx, uid, false)
	if err != nil {
		return err
	}
	for _, item := range jobs {
		evaluation := matching.Evaluate(profile, blacklist, filter, item.Job.JobInput, time.Now())
		if _, err = s.Store.SaveJobMatch(ctx, uid, item.Job.ID, evaluation, filterID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) importJob(w http.ResponseWriter, r *http.Request) {
	var x models.JobInput
	if !decode(w, r, &x) {
		return
	}
	if strings.TrimSpace(x.Title) == "" || strings.TrimSpace(x.Company) == "" || strings.TrimSpace(x.Description) == "" {
		problem(w, 422, "Title, company, and job description are required.")
		return
	}
	if x.ApplyURL != "" {
		u, e := url.ParseRequestURI(x.ApplyURL)
		if e != nil || !(u.Scheme == "http" || u.Scheme == "https") {
			problem(w, 422, "Apply URL must be an http or https URL.")
			return
		}
	}
	if x.SalaryMin < 0 || x.SalaryMax < 0 || x.SalaryMax > 0 && x.SalaryMin > x.SalaryMax {
		problem(w, 422, "Salary range is invalid.")
		return
	}
	ctx := r.Context()
	uid := auth.UserID(ctx)
	p, e := s.Store.Profile(ctx, uid)
	if e != nil {
		internal(w, e)
		return
	}
	black, e := s.Store.Blacklist(ctx, uid)
	if e != nil {
		internal(w, e)
		return
	}
	filter, e := s.Store.ActiveFilter(ctx, uid)
	if e != nil {
		internal(w, e)
		return
	}
	evaluation := matching.Evaluate(p, black, filter, x, time.Now())
	fid := ""
	if filter != nil {
		fid = filter.ID
	}
	fingerprint := discovery.Fingerprint(x)
	job, getErr := s.Store.CatalogJobByFingerprint(ctx, fingerprint)
	if errors.Is(getErr, pgx.ErrNoRows) {
		job, getErr = s.Store.CreateDiscoveredJob(ctx, uid, "", "", "", "", fingerprint, x)
	}
	if getErr != nil {
		internal(w, getErr)
		return
	}
	matchID, getErr := s.Store.SaveJobMatch(ctx, uid, job.ID, evaluation, fid)
	respondCreated(w, models.JobWithMatch{Job: job, Match: evaluation, MatchID: matchID}, getErr)
}
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Jobs(r.Context(), auth.UserID(r.Context()), false)
	respond(w, x, e)
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Job(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respond(w, x, e)
}
func (s *Server) listMatches(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Jobs(r.Context(), auth.UserID(r.Context()), true)
	respond(w, x, e)
}
func (s *Server) getMatch(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Match(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respond(w, x, e)
}
func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	x, e := s.Store.Job(ctx, uid, r.PathValue("id"))
	if e != nil {
		respond(w, x, e)
		return
	}
	if x.Match.Rejected {
		problem(w, 409, store.ErrApplicationBlocked.Error())
		return
	}
	p, e := s.Store.Profile(ctx, uid)
	if e != nil {
		internal(w, e)
		return
	}
	d := generator.Generate(p, x.Job, x.Match)
	if e = s.Store.SaveDocuments(ctx, uid, x.Job.ID, d); e != nil {
		internal(w, e)
		return
	}
	write(w, 200, d)
}

type applicationCreate struct {
	JobID string `json:"job_id"`
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Applications(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var x applicationCreate
	if !decode(w, r, &x) {
		return
	}
	if x.JobID == "" {
		problem(w, 422, "Job ID is required.")
		return
	}
	a, e := s.Store.CreateApplication(r.Context(), auth.UserID(r.Context()), x.JobID)
	respondCreated(w, a, e)
}
func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	a, e := s.Store.Application(ctx, uid, r.PathValue("id"))
	if e != nil {
		respond(w, a, e)
		return
	}
	d, e := s.Store.Documents(ctx, uid, a.JobID)
	if e != nil {
		internal(w, e)
		return
	}
	job, e := s.Store.Job(ctx, uid, a.JobID)
	if e != nil {
		internal(w, e)
		return
	}
	write(w, 200, map[string]any{"application": a, "documents": d, "blocked": job.Match.Rejected, "block_reasons": job.Match.RejectionReasons})
}

type applicationUpdate struct {
	Status string `json:"status"`
	Notes  string `json:"notes"`
}

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request) {
	var x applicationUpdate
	if !decode(w, r, &x) {
		return
	}
	allowed := map[string]bool{"": true, "draft": true, "ready": true, "applied": true, "interview": true, "rejected": true, "offer": true, "withdrawn": true}
	if !allowed[x.Status] {
		problem(w, 422, "Invalid application status.")
		return
	}
	a, e := s.Store.UpdateApplication(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), x.Status, x.Notes)
	respond(w, a, e)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Dashboard(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}

func (s *Server) discoverySettings(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.DiscoverySettings(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) saveDiscoverySettings(w http.ResponseWriter, r *http.Request) {
	var x models.DiscoverySettings
	if !decode(w, r, &x) {
		return
	}
	if x.IntervalMinutes < 360 || x.MinScore < 0 || x.MinScore > 100 || x.MaxJobsPerRun < 1 || x.MaxJobsPerRun > 500 {
		problem(w, 422, "Interval must be at least 360 minutes; score 0–100; run limit 1–500.")
		return
	}
	x, e := s.Store.SaveDiscoverySettings(r.Context(), auth.UserID(r.Context()), x)
	respond(w, x, e)
}
func (s *Server) discoverySources(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.DiscoverySources(r.Context(), auth.UserID(r.Context()), false)
	respond(w, x, e)
}
func (s *Server) addDiscoverySource(w http.ResponseWriter, r *http.Request) {
	var x models.DiscoverySource
	if !decode(w, r, &x) {
		return
	}
	if message := normalizeDiscoverySource(&x); message != "" {
		problem(w, 422, message)
		return
	}
	x, e := s.Store.AddDiscoverySource(r.Context(), auth.UserID(r.Context()), x)
	respondCreated(w, x, e)
}
func normalizeDiscoverySource(x *models.DiscoverySource) string {
	x.Provider = strings.ToLower(strings.TrimSpace(x.Provider))
	x.Name = strings.TrimSpace(x.Name)
	x.Identifier = strings.TrimSpace(x.Identifier)
	x.SearchQuery = strings.TrimSpace(x.SearchQuery)
	x.ExcludedSearchQuery = strings.TrimSpace(x.ExcludedSearchQuery)
	if x.Provider != "arbeitnow" {
		x.ExcludedSearchQuery = ""
	}
	allowed := map[string]bool{"remotive": true, "greenhouse": true, "lever": true, "ashby": true, "smartrecruiters": true, "arbeitnow": true}
	if !allowed[x.Provider] || x.Name == "" {
		return "Choose a supported provider and enter a source name."
	}
	if x.Provider == "remotive" && x.SearchQuery == "" {
		return "Remotive sources require a focused search query."
	}
	if x.Provider != "remotive" && x.Provider != "arbeitnow" && x.Identifier == "" {
		return "Employer-direct sources require a board or company identifier."
	}
	return ""
}
func (s *Server) validateDiscoverySource(w http.ResponseWriter, r *http.Request) {
	var x models.DiscoverySource
	if !decode(w, r, &x) {
		return
	}
	if message := normalizeDiscoverySource(&x); message != "" {
		problem(w, 422, message)
		return
	}
	if s.Discovery == nil {
		problem(w, 503, "Discovery service is unavailable.")
		return
	}
	count, sample, err := s.Discovery.ValidateSource(r.Context(), x)
	if err != nil {
		problem(w, 422, "Source could not be read: "+err.Error())
		return
	}
	write(w, 200, map[string]any{"count": count, "sample_title": sample})
}
func (s *Server) deleteDiscoverySource(w http.ResponseWriter, r *http.Request) {
	e := s.Store.DeleteDiscoverySource(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(e, pgx.ErrNoRows) {
		problem(w, 404, "Source not found.")
		return
	}
	if e != nil {
		internal(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) discoveryRuns(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.DiscoveryRuns(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) runDiscovery(w http.ResponseWriter, r *http.Request) {
	if s.Discovery == nil {
		problem(w, 503, "Discovery service is unavailable.")
		return
	}
	x, e := s.Discovery.RunUser(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}
func (s *Server) runRecommendedDiscovery(w http.ResponseWriter, r *http.Request) {
	if s.Discovery == nil {
		problem(w, 503, "Discovery service is unavailable.")
		return
	}
	x, e := s.Discovery.RunRecommended(r.Context(), auth.UserID(r.Context()))
	if errors.Is(e, discovery.ErrMissingDesiredTitle) {
		problem(w, 422, e.Error())
		return
	}
	respond(w, x, e)
}
func (s *Server) outbound(w http.ResponseWriter, r *http.Request) {
	destination, partner, disclosure, e := s.Store.RecordOutbound(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if e != nil {
		respond(w, map[string]string{}, e)
		return
	}
	write(w, 200, map[string]string{"url": destination, "partner": partner, "disclosure": disclosure})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, "Invalid JSON request: "+err.Error())
		return false
	}
	return true
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, message string) {
	write(w, status, map[string]string{"error": message})
}
func internal(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	problem(w, 500, "An internal error occurred.")
}
func respond(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, store.ErrApplicationBlocked) {
		problem(w, 409, err.Error())
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Record not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 200, v)
}
func respondCreated(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, store.ErrApplicationBlocked) {
		problem(w, 409, err.Error())
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Record not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, 201, v)
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// aiStatus reports whether an optional AI provider is wired up. Deterministic
// behaviour never depends on this; it exists so a test run can confirm which
// provider, if any, is actually in use.
func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"enabled": s.LLM.Enabled(), "provider": s.LLM.Describe()})
}
