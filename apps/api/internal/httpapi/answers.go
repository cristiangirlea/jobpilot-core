package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// answerService builds the resolver on demand so the store stays the single
// owner of persistence and the handler stays stateless.
func (s *Server) answerService() *answers.Service {
	return &answers.Service{Bank: s.Store, LLM: s.LLM}
}

func (s *Server) listAnswers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	x, e := s.Store.ListAnswers(r.Context(), auth.UserID(r.Context()), status)
	respond(w, x, e)
}

type answerWrite struct {
	QuestionText string   `json:"question_text"`
	Answer       string   `json:"answer"`
	RoleFamily   string   `json:"role_family"`
	Scope        string   `json:"scope"`
	JobID        string   `json:"job_id"`
	Technologies []string `json:"technologies"`
	Approved     *bool    `json:"approved"`
}

// answerScope checks a requested scope and resolves its job. An empty scope is
// returned as is, for the caller's default. A job is looked up through the
// user's own matches, so a job they cannot see cannot be named; the answer
// then stores the job's id even when the caller gave its slug. ok is false
// once a problem has been written.
func (s *Server) answerScope(w http.ResponseWriter, r *http.Request, scope, jobRef string) (string, string, bool) {
	scope = strings.TrimSpace(scope)
	switch scope {
	case "", models.AnswerScopeGlobal, models.AnswerScopeRoleFamily, models.AnswerScopeJob:
	default:
		problem(w, 400, "scope must be global, role_family or job.")
		return "", "", false
	}
	jobRef = strings.TrimSpace(jobRef)
	if jobRef == "" {
		return scope, "", true
	}
	job, err := s.Store.Job(r.Context(), auth.UserID(r.Context()), jobRef)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "Job not found.")
		return "", "", false
	}
	if err != nil {
		internal(w, err)
		return "", "", false
	}
	return scope, job.Job.ID, true
}

// answerProblem writes the answer bank's own refusals in its words.
func answerProblem(w http.ResponseWriter, v any, err error, created bool) {
	switch {
	case errors.Is(err, store.ErrAnswerScopeNeedsJob):
		problem(w, 400, err.Error())
	case errors.Is(err, store.ErrAnswerScopeTaken):
		problem(w, 409, err.Error())
	case created:
		respondCreated(w, v, err)
	default:
		respond(w, v, err)
	}
}

// saveAnswer stores an answer the user wrote themselves. A hand-written answer
// is approved unless the caller explicitly says otherwise, because the user
// authoring it is the approval.
func (s *Server) saveAnswer(w http.ResponseWriter, r *http.Request) {
	var body answerWrite
	if !decode(w, r, &body) {
		return
	}
	question := strings.TrimSpace(body.QuestionText)
	answer := strings.TrimSpace(body.Answer)
	if question == "" {
		problem(w, 400, "A question is required.")
		return
	}
	if answer == "" {
		problem(w, 400, "An answer is required.")
		return
	}
	approved := true
	if body.Approved != nil {
		approved = *body.Approved
	}
	family := strings.TrimSpace(body.RoleFamily)
	if family == "" {
		family = "general"
	}
	scope, jobID, ok := s.answerScope(w, r, body.Scope, body.JobID)
	if !ok {
		return
	}
	entry := models.AnswerEntry{
		QuestionKey:        answers.Classify(question),
		QuestionText:       question,
		QuestionNormalized: answers.Normalize(question),
		Answer:             answer,
		RoleFamily:         family,
		Scope:              scope,
		JobID:              jobID,
		Technologies:       body.Technologies,
		Source:             "human",
		Approved:           approved,
	}
	x, e := s.Store.SaveAnswer(r.Context(), auth.UserID(r.Context()), entry)
	answerProblem(w, x, e, true)
}

type answerPatch struct {
	Answer   string `json:"answer"`
	Approved *bool  `json:"approved"`
	Scope    string `json:"scope"`
	JobID    string `json:"job_id"`
}

// updateAnswer is the review action: edit the wording, approve it, choose
// where it is reused, or all of these. Approving is what makes an entry
// reusable for future applications.
func (s *Server) updateAnswer(w http.ResponseWriter, r *http.Request) {
	var body answerPatch
	if !decode(w, r, &body) {
		return
	}
	if body.Approved == nil && strings.TrimSpace(body.Answer) == "" && strings.TrimSpace(body.Scope) == "" {
		problem(w, 400, "Provide an answer, an approval decision, a scope, or any of them.")
		return
	}
	scope, jobID, ok := s.answerScope(w, r, body.Scope, body.JobID)
	if !ok {
		return
	}
	approved := true
	if body.Approved != nil {
		approved = *body.Approved
	}
	x, e := s.Store.UpdateAnswer(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), strings.TrimSpace(body.Answer), approved, scope, jobID)
	answerProblem(w, x, e, false)
}

func (s *Server) deleteAnswer(w http.ResponseWriter, r *http.Request) {
	if e := s.Store.DeleteAnswer(r.Context(), auth.UserID(r.Context()), r.PathValue("id")); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			problem(w, 404, "Answer not found.")
			return
		}
		internal(w, e)
		return
	}
	w.WriteHeader(204)
}

type resolveRequest struct {
	Questions []answers.Question `json:"questions"`
}

type resolveResponse struct {
	AIEnabled   bool                 `json:"ai_enabled"`
	Provider    string               `json:"provider"`
	NeedsReview int                  `json:"needs_review"`
	Resolutions []answers.Resolution `json:"resolutions"`
}

// resolveJobAnswers answers a set of application questions for one job. It is
// the endpoint the apply engine will call, and it refuses rejected jobs for the
// same reason document generation does: the backend, not the UI, enforces the
// application lock.
func (s *Server) resolveJobAnswers(w http.ResponseWriter, r *http.Request) {
	var body resolveRequest
	if !decode(w, r, &body) {
		return
	}
	if len(body.Questions) == 0 {
		problem(w, 400, "At least one question is required.")
		return
	}
	if len(body.Questions) > 50 {
		problem(w, 400, "At most 50 questions can be resolved in one request.")
		return
	}
	ctx := r.Context()
	uid := auth.UserID(ctx)
	job, err := s.Store.Job(ctx, uid, r.PathValue("id"))
	if err != nil {
		respond(w, job, err)
		return
	}
	if job.Match.Rejected {
		problem(w, 409, store.ErrApplicationBlocked.Error())
		return
	}
	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}
	subject := answers.Subject{
		Profile:   profile,
		Job:       job.Job,
		Match:     job.Match,
		Documents: generator.Generate(profile, job.Job, job.Match),
	}
	resolutions := s.answerService().ResolveAll(ctx, uid, body.Questions, subject)
	pending := 0
	for _, resolution := range resolutions {
		if resolution.NeedsReview {
			pending++
		}
	}
	write(w, 200, resolveResponse{
		AIEnabled:   s.LLM.Enabled(),
		Provider:    s.LLM.Describe(),
		NeedsReview: pending,
		Resolutions: resolutions,
	})
}
