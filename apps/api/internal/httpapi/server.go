package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/llm"
	mailer "jobpilot/core/api/internal/mail"
	"jobpilot/core/api/internal/matching"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// Contract is the version of the behaviour a client may rely on, reported by
// GET /api/health so a client built for a newer API can notice an older one
// instead of failing quietly. Raise it when a change alters what an existing
// request does, not when a route is added.
//
//	1: a status-only PATCH on an application keeps its notes; apply_url is
//	   withheld for rejected matches; agent tokens with read and write scopes.
//	2: composing an application refuses a closed posting with 409; jobs carry
//	   closed_at and closed_reason.
//	3: approving an outbox entry needs a browser session (403 for agent
//	   tokens), and re-checks the entry's status, its CV and its required
//	   drafted answers at that moment, refusing with 409.
//	4: detecting a board resolves a careers-site link to a Greenhouse job
//	   (gh_jid) by asking Greenhouse, instead of refusing it with 422.
//	5: outbox entries carry cv, the summary of the CV they carry as it is now
//	   (absent when it was deleted), and the web app relies on it.
//	6: answers carry scope (global, role_family or job) and job_id, which
//	   POST and PATCH /api/answers honour; resolving prefers the narrowest
//	   scope. An older API ignores them and keeps every answer per role family.
//	7: where the server sends (health reports sending: true), an approved
//	   outbox entry goes to the employer's form and ends sent or failed, with
//	   the reason in error; while it is sending, composing it, approving it
//	   and cancelling it are refused with 409.
//	8: registering needs an invite unless the server allows open sign-up
//	   (403 otherwise); login and registration answer 429 with Retry-After
//	   when rate-limited.
//	9: with ADMIN_EMAILS set, discovery and the AI status answer 403 to
//	   other accounts, which also draft without the AI provider and hold
//	   agent tokens only as USER_AGENT_TOKENS allows; GET /api/me says which
//	   applies to the caller.
//	10: users carry email_verified, and GET /api/me says whether the
//	   caller's address is verified and whether this server requires it;
//	   while the server sends email, an unverified account is refused with
//	   403 on running discovery,
//	   generating, composing or tailoring documents, resolving a job's
//	   answers, creating agent tokens, and linking a Google identity.
//	11: quiet notifications and stored follow-up drafts, capped at two.
const Contract = 11

// ApprovalNeedsSession is the refusal an agent token gets from the approve route.
const ApprovalNeedsSession = "Approve applications in the web app: approving one is your consent to send it, which an agent token cannot give."

type Server struct {
	Store       *store.Store
	Discovery   *discovery.Service
	LLM         *llm.Client
	ATS         *ats.Reader
	Secret      string
	TokenTTL    time.Duration
	FrontendURL string
	// Google is nil unless sign-in with Google is configured. GoogleSignup
	// lets it create an account for someone who has none.
	Google       *auth.OIDC
	GoogleSignup bool
	handoff      *auth.Handoff
	// Sender sends approved outbox entries; nil when sending is off.
	Sender interface{ Kick() }

	// Registration is config.RegistrationInvite (also when empty), Open or
	// Closed; AdminEmails may manage invites; ClientIPHeader is
	// config.Config's, the proxy header rate limits read the address from.
	Registration string
	AdminEmails  []string
	// UserAgentTokens is what agent tokens members may hold and use:
	// TokensOff (the default), TokensRead or TokensWrite. Admins hold any.
	UserAgentTokens string
	ClientIPHeader  string
	// Mailer sends verification and reset emails; nil sends nothing, and
	// then no account has to verify its address.
	Mailer mailer.Sender

	limits   *authLimits
	mailJobs chan mailJob
}

func (s *Server) Handler() http.Handler {
	if s.limits == nil {
		s.limits = newAuthLimits()
	}
	if s.mailJobs == nil {
		s.startMail()
	}
	auth.PrepareLogin()
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"status": "ok", "contract": Contract, "sending": s.Sender != nil})
	})
	m.HandleFunc("POST /api/auth/register", s.register)
	m.HandleFunc("POST /api/auth/login", s.login)
	m.HandleFunc("GET /api/auth/registration", s.registration)
	s.handoff = auth.NewHandoff()
	m.HandleFunc("GET /api/auth/providers", s.authProviders)
	m.HandleFunc("GET /api/auth/google/start", s.googleStart)
	m.HandleFunc("GET /api/auth/google/callback", s.googleCallback)
	m.HandleFunc("POST /api/auth/google/exchange", s.googleExchange)
	m.HandleFunc("POST /api/auth/verify-email", s.verifyEmail)
	m.HandleFunc("POST /api/auth/password-reset", s.requestPasswordReset)
	m.HandleFunc("POST /api/auth/password-reset/confirm", s.confirmPasswordReset)
	// p: any authenticated caller. w: agent tokens need the write scope.
	// session: browser logins only, so a leaked agent token cannot mint
	// siblings or revoke itself. consent: browser logins only too, because
	// approving an application is the user's consent to send it, which no
	// agent gives, whatever its scopes.
	p := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, s.agentPolicy(false, h))
	}
	w := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, auth.RequireScope(auth.ScopeWrite, s.agentPolicy(true, h)))
	}
	// ap and aw: admins only (#128), signed in or through their agent tokens,
	// so an admin's MCP server keeps running discovery; aw also needs the
	// write scope. With no ADMIN_EMAILS every account passes.
	ap := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, s.adminOnly(needsAdmin, h))
	}
	aw := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, auth.RequireScope(auth.ScopeWrite, s.adminOnly(needsAdmin, h)))
	}
	session := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, auth.RequireSession("Use the web app to manage agent tokens.", h))
	}
	consent := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, auth.RequireSession(ApprovalNeedsSession, h))
	}
	// admin: browser logins of the accounts ADMIN_EMAILS names, for invites.
	admin := func(h http.HandlerFunc) http.Handler {
		return auth.Middleware(s.Secret, s.Store, auth.RequireSession("Use the web app to manage invites.", s.requireAdmin(h)))
	}
	m.Handle("POST /api/account/verification", w(s.resendVerification))
	m.Handle("GET /api/ai/status", ap(s.aiStatus))
	m.Handle("GET /api/me", p(s.me))
	m.Handle("GET /api/answers", p(s.listAnswers))
	m.Handle("POST /api/answers", w(s.saveAnswer))
	m.Handle("PATCH /api/answers/{id}", w(s.updateAnswer))
	m.Handle("DELETE /api/answers/{id}", w(s.deleteAnswer))
	m.Handle("POST /api/jobs/{id}/answers", w(s.requireVerified(s.resolveJobAnswers)))
	m.Handle("POST /api/cv", w(s.uploadCV))
	m.Handle("GET /api/cv", p(s.listCVs))
	m.Handle("GET /api/cv/{id}", p(s.getCV))
	m.Handle("GET /api/cv/{id}/file", p(s.downloadCV))
	m.Handle("POST /api/cv/{id}/approve", w(s.approveCV))
	m.Handle("POST /api/cv/{id}/import", w(s.importCV))
	m.Handle("DELETE /api/cv/{id}", w(s.deleteCV))
	m.Handle("POST /api/jobs/{id}/cv", w(s.requireVerified(s.cvForJob)))
	m.Handle("POST /api/jobs/{id}/compose", w(s.requireVerified(s.compose)))
	m.Handle("GET /api/outbox", p(s.listOutbox))
	m.Handle("GET /api/outbox/{id}", p(s.getOutboxEntry))
	m.Handle("POST /api/outbox/{id}/approve", consent(s.approveOutboxEntry))
	m.Handle("POST /api/outbox/{id}/cancel", w(s.cancelOutboxEntry))
	m.Handle("GET /api/profile", p(s.getProfile))
	m.Handle("PUT /api/profile", w(s.putProfile))
	m.Handle("GET /api/experiences", p(s.listExperiences))
	m.Handle("POST /api/experiences", w(s.addExperience))
	m.Handle("DELETE /api/experiences/{id}", w(s.deleteExperience))
	m.Handle("GET /api/skills", p(s.listSkills))
	m.Handle("GET /api/skills/suggestions", p(s.skillSuggestions))
	m.Handle("POST /api/skills", w(s.addSkill))
	m.Handle("DELETE /api/skills/{id}", w(s.deleteSkill))
	m.Handle("GET /api/projects", p(s.listProjects))
	m.Handle("POST /api/projects", w(s.addProject))
	m.Handle("DELETE /api/projects/{id}", w(s.deleteProject))
	m.Handle("GET /api/blacklist", p(s.listBlacklist))
	m.Handle("POST /api/blacklist", w(s.addBlacklist))
	m.Handle("DELETE /api/blacklist/{id}", w(s.deleteBlacklist))
	m.Handle("GET /api/filters", p(s.listFilters))
	m.Handle("POST /api/filters", w(s.saveFilter))
	m.Handle("PUT /api/filters/{id}", w(s.saveFilter))
	m.Handle("DELETE /api/filters/{id}", w(s.deleteFilter))
	m.Handle("GET /api/jobs", p(s.listJobs))
	m.Handle("POST /api/jobs/find", w(s.findJobs))
	m.Handle("GET /api/catalog/summary", p(s.catalogSummary))
	m.Handle("POST /api/jobs", w(s.importJob))
	m.Handle("GET /api/jobs/{id}", p(s.getJob))
	m.Handle("POST /api/jobs/{id}/report", w(s.reportJob))
	m.Handle("DELETE /api/jobs/{id}/report", w(s.withdrawReport))
	m.Handle("POST /api/jobs/{id}/generate", w(s.requireVerified(s.generate)))
	m.Handle("GET /api/matches", p(s.listMatches))
	m.Handle("GET /api/matches/{id}", p(s.getMatch))
	m.Handle("GET /api/applications", p(s.listApplications))
	m.Handle("POST /api/applications", w(s.createApplication))
	m.Handle("GET /api/applications/{id}", p(s.getApplication))
	m.Handle("PATCH /api/applications/{id}", w(s.updateApplication))
	m.Handle("POST /api/applications/{id}/follow-up", w(s.requireVerified(s.draftFollowUp)))
	m.Handle("POST /api/applications/{id}/dismiss-follow-up", w(s.dismissQuietApplication))
	m.Handle("GET /api/dashboard", p(s.dashboard))
	m.Handle("GET /api/notifications", p(s.listNotifications))
	m.Handle("GET /api/discovery/settings", ap(s.discoverySettings))
	m.Handle("PUT /api/discovery/settings", aw(s.saveDiscoverySettings))
	m.Handle("GET /api/discovery/sources", ap(s.discoverySources))
	m.Handle("POST /api/discovery/sources", aw(s.addDiscoverySource))
	m.Handle("PUT /api/discovery/sources/{id}", aw(s.updateDiscoverySource))
	m.Handle("POST /api/discovery/validate", ap(s.validateDiscoverySource))
	m.Handle("DELETE /api/discovery/sources/{id}", aw(s.deleteDiscoverySource))
	m.Handle("GET /api/discovery/runs", ap(s.discoveryRuns))
	m.Handle("POST /api/discovery/run", aw(s.requireVerified(s.runDiscovery)))
	m.Handle("POST /api/discovery/recommended", aw(s.requireVerified(s.runRecommendedDiscovery)))
	m.Handle("GET /api/discovery/suggestions", ap(s.boardSuggestions))
	m.Handle("POST /api/discovery/detect", ap(s.detectBoard))
	m.Handle("POST /api/jobs/{id}/outbound", w(s.outbound))
	m.Handle("GET /api/tokens", session(s.listTokens))
	m.Handle("POST /api/tokens", session(s.requireVerified(s.createToken)))
	m.Handle("DELETE /api/tokens/{id}", session(s.revokeToken))
	// Linking adds a lasting way in, which a password reset does not remove,
	// so it waits for a verified address.
	m.Handle("POST /api/auth/google/link", session(s.requireVerified(s.googleLink)))
	m.Handle("GET /api/invites", admin(s.listInvites))
	m.Handle("POST /api/invites", admin(s.createInvite))
	m.Handle("DELETE /api/invites/{id}", admin(s.deleteInvite))
	return s.cors(m)
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.FrontendURL)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		// The web job list reads how many jobs match from this header.
		w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
		w.Header().Set("Vary", "Origin")
		// Redeeming a Google handoff code needs its binding cookie, so the
		// web app sends these two with credentials. No other route reads a
		// cookie.
		if r.URL.Path == "/api/auth/google/exchange" || r.URL.Path == "/api/auth/google/link" {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
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
	// Invite is the sign-up code; login ignores it.
	Invite string `json:"invite"`
}

// register and login are rate-limited by client address before the body is
// read and by email after, both before any bcrypt work.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if limited(w, s.limits.registerIP, s.clientIP(r)) {
		return
	}
	var x credentials
	if !decode(w, r, &x) {
		return
	}
	x.Email = strings.TrimSpace(strings.ToLower(x.Email))
	if _, err := mail.ParseAddress(x.Email); err != nil || len(x.Password) < 8 {
		problem(w, 422, "Enter a valid email and a password of at least 8 characters.")
		return
	}
	if limited(w, s.limits.registerEmail, emailKey(x.Email)) {
		return
	}
	if err := s.checkSignUp(r.Context(), x.Invite); err != nil {
		if !signUpRefused(w, err) {
			internal(w, err)
		}
		return
	}
	hash, err := auth.Hash(x.Password)
	if err != nil {
		internal(w, err)
		return
	}
	u, err := s.createAccount(r.Context(), x.Email, hash, x.Invite)
	if signUpRefused(w, err) {
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	// The account exists either way; a failure here leaves it unverified,
	// and Settings can send another link.
	if err := s.sendVerification(r.Context(), u); err != nil {
		slog.Error("creating a verification link failed", "error", err)
	}
	tok, _ := auth.Token(u.ID, u.Email, s.Secret, s.TokenTTL)
	write(w, 201, map[string]any{"token": tok, "user": u})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if limited(w, s.limits.loginIP, s.clientIP(r)) {
		return
	}
	var x credentials
	if !decode(w, r, &x) {
		return
	}
	email := strings.TrimSpace(strings.ToLower(x.Email))
	if limited(w, s.limits.loginEmail, emailKey(email)) {
		return
	}
	// An unknown email runs bcrypt too and gets the same answer, so neither
	// the time nor the text tells whether it has an account.
	u, hash, err := s.Store.Credentials(r.Context(), email)
	if !auth.CheckLogin(hash, err == nil, x.Password) {
		problem(w, 401, "Invalid email or password.")
		return
	}
	s.limits.loginEmail.Reset(emailKey(email))
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
	if e == nil {
		e = s.reevaluateJobs(r)
	}
	respondCreated(w, x, e)
}
func (s *Server) deleteExperience(w http.ResponseWriter, r *http.Request) {
	s.deleteAndReevaluate(w, r, "work_experiences", "Experience not found.")
}
func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.Skills(r.Context(), auth.UserID(r.Context()))
	respond(w, x, e)
}

// suggestionSample is how many of the user's best matches are read to count
// which technologies postings ask for. Enough to rank by demand, small enough
// to answer while the user is typing.
const suggestionSample = 150

func (s *Server) skillSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), auth.UserID(r.Context())
	skills, err := s.Store.Skills(ctx, uid)
	if err != nil {
		respond(w, nil, err)
		return
	}
	held := make([]string, 0, len(skills))
	for _, skill := range skills {
		held = append(held, skill.Name)
	}
	fromCV, err := s.Store.BaseCVTechnologies(ctx, uid)
	if err != nil {
		respond(w, nil, err)
		return
	}
	texts, err := s.Store.MatchTexts(ctx, uid, suggestionSample)
	respond(w, answers.SuggestSkills(held, fromCV, texts), err)
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
	if e == nil {
		e = s.reevaluateJobs(r)
	}
	respondCreated(w, x, e)
}
func (s *Server) deleteSkill(w http.ResponseWriter, r *http.Request) {
	s.deleteAndReevaluate(w, r, "skills", "Skill not found.")
}
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
	if e == nil {
		e = s.reevaluateJobs(r)
	}
	respondCreated(w, x, e)
}
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.deleteAndReevaluate(w, r, "projects", "Project not found.")
}
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
	s.deleteAndReevaluate(w, r, "company_blacklist", "Excluded company not found.")
}
func (s *Server) deleteAndReevaluate(w http.ResponseWriter, r *http.Request, table, notFoundMessage string) {
	err := s.Store.DeleteOwned(r.Context(), table, auth.UserID(r.Context()), r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, notFoundMessage)
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
	s.deleteAndReevaluate(w, r, "filter_presets", "Filter not found.")
}

// reevaluateJobs re-scores every match after a profile change. The change is
// already saved when this runs, so the re-score must finish even if the client
// goes away: a page reload mid-request would otherwise leave every match scored
// against the old profile.
func (s *Server) reevaluateJobs(r *http.Request) error {
	// After the change is stored and before reading it back, so a catalog
	// pass that read the old exclusions cannot save after this one. The
	// deadline starts once the lock is held, so waiting for a long pass
	// cannot leave this repair without time to run.
	defer s.Discovery.LockMatching(auth.UserID(r.Context()))()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
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
	jobs, _, err := s.Store.Jobs(ctx, uid, store.JobQuery{})
	if err != nil {
		return err
	}
	now := time.Now()
	evaluations := make([]store.JobEvaluation, 0, len(jobs))
	for _, item := range jobs {
		evaluations = append(evaluations, store.JobEvaluation{JobID: item.Job.ID, Evaluation: matching.Evaluate(profile, blacklist, filter, item.Job.JobInput, now)})
	}
	return s.Store.SaveJobMatches(ctx, uid, filterID, evaluations)
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
	// This response is assembled here rather than read back through
	// scanJobMatch, so it has to withhold the apply URL itself.
	if evaluation.Rejected {
		job.ApplyURL = ""
	}
	respondCreated(w, models.JobWithMatch{Job: job, Match: evaluation, MatchID: matchID}, getErr)
}
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	s.respondJobs(w, r, "")
}

// respondJobs serves a job list narrowed by the optional parameters jobQuery
// reads, with the count before paging in X-Total-Count. fixedState is the
// state the route implies, if any.
func (s *Server) respondJobs(w http.ResponseWriter, r *http.Request, fixedState string) {
	q, message := jobQuery(r, fixedState)
	if message != "" {
		problem(w, 422, message)
		return
	}
	jobs, total, err := s.Store.Jobs(r.Context(), auth.UserID(r.Context()), q)
	if err != nil {
		internal(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	write(w, 200, jobs)
}

// jobQuery reads a job list's optional parameters: state, min_score, open, q,
// fields=summary, limit and offset. Without any, the list is whole, as it was
// before they existed. A listing of every job was 14.4 MB on a full account,
// most of it descriptions, so a client that pages should also ask for the
// summary and fetch one job for its text.
func jobQuery(r *http.Request, fixedState string) (store.JobQuery, string) {
	values := r.URL.Query()
	q := store.JobQuery{State: fixedState}
	if state := values.Get("state"); state != "" {
		switch {
		case fixedState != "" && state != fixedState:
			return q, "This list holds " + fixedState + " jobs only; use /api/jobs with state for others."
		case state != "accepted" && state != "rejected" && state != "all":
			return q, "state must be accepted, rejected, or all."
		}
		q.State = state
	}
	number := func(name string, min, max int) (int, string) {
		raw := values.Get(name)
		if raw == "" {
			return 0, ""
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < min || n > max {
			return 0, fmt.Sprintf("%s must be a whole number from %d to %d.", name, min, max)
		}
		return n, ""
	}
	var message string
	if q.MinScore, message = number("min_score", 0, 100); message != "" {
		return q, message
	}
	if q.Limit, message = number("limit", 1, 500); message != "" {
		return q, message
	}
	if q.Offset, message = number("offset", 0, 1<<30); message != "" {
		return q, message
	}
	if q.Offset > 0 && q.Limit == 0 {
		return q, "offset needs a limit."
	}
	switch values.Get("fields") {
	case "":
	case "summary":
		q.Summary = true
	default:
		return q, "fields must be summary, or left out for whole jobs."
	}
	if q.Search = strings.TrimSpace(values.Get("q")); len([]rune(q.Search)) > 60 {
		return q, "q must be at most 60 characters."
	}
	switch values.Get("open") {
	case "", "false":
	case "true":
		q.OpenOnly = true
	default:
		return q, "open must be true or false."
	}
	return q, ""
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserID(ctx)
	x, e := s.Store.Job(ctx, uid, r.PathValue("id"))
	if e == nil {
		x.Reported, e = s.Store.HasReported(ctx, uid, x.Job.ID)
	}
	respond(w, x, e)
}
func (s *Server) listMatches(w http.ResponseWriter, r *http.Request) {
	s.respondJobs(w, r, "accepted")
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
	followUps, e := s.Store.FollowUps(ctx, uid, a.ID)
	if e != nil {
		internal(w, e)
		return
	}
	write(w, 200, map[string]any{"application": a, "documents": d, "follow_ups": followUps, "blocked": job.Match.Rejected, "block_reasons": job.Match.RejectionReasons})
}

type applicationUpdate struct {
	Status string `json:"status"`
	// Notes is a pointer so an omitted field keeps the stored notes while an
	// explicit empty string clears them.
	Notes *string `json:"notes"`
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
	// Both fields are optional individually, but a body carrying neither
	// would touch the row and report success without changing anything.
	if x.Status == "" && x.Notes == nil {
		problem(w, 422, "Provide a status, notes, or both.")
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
	if x.QuietApplicationDays < 0 || x.QuietApplicationDays > 365 {
		problem(w, 422, "Quiet application days must be from 1 to 365.")
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
	respondDiscoverySource(w, x, e, http.StatusCreated)
}
func (s *Server) updateDiscoverySource(w http.ResponseWriter, r *http.Request) {
	var x models.DiscoverySource
	if !decode(w, r, &x) {
		return
	}
	if message := normalizeDiscoverySource(&x); message != "" {
		problem(w, 422, message)
		return
	}
	x, e := s.Store.UpdateDiscoverySource(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), x)
	respondDiscoverySource(w, x, e, http.StatusOK)
}
func respondDiscoverySource(w http.ResponseWriter, x models.DiscoverySource, err error, status int) {
	if errors.Is(err, store.ErrDuplicateDiscoverySource) {
		problem(w, 409, err.Error())
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Source not found.")
		return
	}
	if err != nil {
		internal(w, err)
		return
	}
	write(w, status, x)
}
func normalizeDiscoverySource(x *models.DiscoverySource) string {
	x.Provider = strings.ToLower(strings.TrimSpace(x.Provider))
	x.Name = strings.TrimSpace(x.Name)
	x.Identifier = strings.TrimSpace(x.Identifier)
	x.SearchQuery = strings.TrimSpace(x.SearchQuery)
	x.ExcludedSearchQuery = strings.TrimSpace(x.ExcludedSearchQuery)
	// Remotive searches on its own side and has nothing to exclude with.
	if x.Provider == "remotive" {
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
