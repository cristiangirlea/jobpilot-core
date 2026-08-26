package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"
)

// compose builds a complete application and queues it for approval.
//
// Composition never sends. It assembles the CV, the letter and every form
// answer, then records what is still missing, so the user reviews exactly what
// an employer would receive before anything leaves.
func (s *Server) compose(w http.ResponseWriter, r *http.Request) {
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

	entry := models.OutboxEntry{JobID: job.Job.ID, Channel: models.ChannelManual, Blockers: []string{}, Notices: []string{}}

	// The CV. A generated one arrives unapproved, which is itself a blocker.
	decision, err := s.decideCV(ctx, uid, job, profile)
	if err != nil {
		internal(w, err)
		return
	}
	entry.CVDocumentID, entry.CVDecision = decision.CV.ID, decision.Decision
	if !decision.Approved {
		entry.Blockers = append(entry.Blockers, "A tailored CV was prepared for this role and needs your approval before it can be sent.")
	}

	// The letter.
	entry.CoverLetter, entry.CoverLetterSource = generator.CoverLetter(ctx, s.LLM, profile, job.Job, job.Match)
	if entry.CoverLetterSource == generator.LetterSourceTemplate && s.LLM.Enabled() {
		// Worth knowing, but the letter is still usable, so it does not block.
		entry.Notices = append(entry.Notices, "The cover letter fell back to the deterministic template because the AI provider did not respond.")
	}

	// The employer's form, where it is published.
	form, formErr := s.ATS.Fetch(ctx, job.Job.ApplyURL)
	entry.ATSProvider = form.Provider
	switch {
	case formErr == nil:
		entry.Channel = models.ChannelATSForm
		entry.Answers, entry.NeedsReview = s.answerForm(ctx, uid, form, profile, job)
	case errors.Is(formErr, ats.ErrUnsupported):
		entry.Blockers = append(entry.Blockers, unsupportedReason(form.Provider))
	case errors.Is(formErr, ats.ErrNotFound):
		entry.Blockers = append(entry.Blockers, "The employer's posting could not be found; it may have been closed.")
	default:
		entry.Blockers = append(entry.Blockers, "The employer's application form could not be read: "+formErr.Error())
	}
	if formErr == nil {
		entry.Form = formToMap(form)
		for _, answer := range entry.Answers {
			if answer.Required && strings.TrimSpace(answer.Answer) == "" {
				entry.Blockers = append(entry.Blockers, fmt.Sprintf("A required question has no answer yet: %q", answer.Label))
			}
		}
		if len(form.Skipped) > 0 {
			// These are voluntary and are the user's alone to answer. Treating
			// them as blockers would make the application permanently
			// unapprovable, since there is nothing here for the user to fix.
			entry.Notices = append(entry.Notices, fmt.Sprintf("%d voluntary equal-opportunity or demographic questions were left blank; this software does not answer them. Complete them on the employer's form if you wish.", len(form.Skipped)))
		}
	}

	// Contact details an employer will require regardless of channel.
	if strings.TrimSpace(profile.Email) == "" {
		entry.Blockers = append(entry.Blockers, "Your profile has no email address.")
	}
	if strings.TrimSpace(profile.Name) == "" {
		entry.Blockers = append(entry.Blockers, "Your profile has no name.")
	}

	// Status follows the blockers alone. NeedsReview also counts optional
	// questions left blank, and an optional field is not a reason to hold an
	// application back - it is only a reason to show the user where to look.
	entry.Status = models.OutboxReady
	if len(entry.Blockers) > 0 {
		entry.Status = models.OutboxNeedsReview
	}

	// The queue mirrors the application pipeline, so a queued application shows
	// up in tracking rather than existing only in the outbox.
	if application, appErr := s.Store.CreateApplication(ctx, uid, job.Job.ID); appErr == nil {
		entry.ApplicationID = application.ID
	}

	saved, err := s.Store.SaveOutboxEntry(ctx, uid, entry)
	if err != nil {
		internal(w, err)
		return
	}
	if decision.Preview != nil {
		// Surfaced so a review can see what changed in the tailored CV without
		// a second request.
		saved.Form = withPreview(saved.Form, decision)
	}
	write(w, 201, saved)
}

// answerForm resolves every question on a published form. Resume and cover
// letter fields are filled from the composed application rather than the answer
// bank, and demographic questions never reach here at all.
func (s *Server) answerForm(ctx context.Context, uid string, form ats.Form, profile models.Profile, job models.JobWithMatch) ([]models.OutboxAnswer, int) {
	service := s.answerService()
	subject := answers.Subject{
		Profile:   profile,
		Job:       job.Job,
		Match:     job.Match,
		Documents: generator.Generate(profile, job.Job, job.Match),
	}

	out := make([]models.OutboxAnswer, 0, len(form.Fields))
	pending := 0
	for _, field := range form.Fields {
		answer := models.OutboxAnswer{
			Field: field.Name, Label: field.Label, Type: field.Type,
			Required: field.Required, Options: field.Options,
		}
		switch field.Role {
		case ats.RoleResume:
			answer.Answer, answer.Source = "(the approved CV is attached)", "cv"
		case ats.RoleCoverLetter:
			answer.Answer, answer.Source = "(the composed cover letter is attached)", "cover_letter"
		default:
			resolution, err := service.Resolve(ctx, uid, answers.Question{
				Text: field.Label, Kind: field.Type, Options: field.Options, MaxLength: field.MaxLength,
			}, subject)
			if err != nil {
				resolution.Source = answers.SourceUnanswered
				resolution.NeedsReview = true
			}
			answer.Answer, answer.Source = resolution.Answer, resolution.Source
			answer.NeedsReview, answer.EntryID = resolution.NeedsReview, resolution.EntryID
		}
		if answer.NeedsReview {
			pending++
		}
		out = append(out, answer)
	}
	return out, pending
}

func unsupportedReason(provider string) string {
	if provider != "" {
		return fmt.Sprintf("This employer uses %s, whose application form is not published, so this one has to be submitted by hand for now.", provider)
	}
	return "This apply link is not an application form this software can read, so it has to be submitted by hand."
}

func formToMap(form ats.Form) map[string]any {
	raw, err := json.Marshal(form)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func withPreview(form map[string]any, decision cvDecision) map[string]any {
	if form == nil {
		form = map[string]any{}
	}
	form["cv_preview"] = map[string]any{
		"reason":      decision.Reason,
		"alignment":   decision.Alignment,
		"highlighted": decision.Preview.Highlighted,
		"missing":     decision.Preview.Missing,
	}
	return form
}

func (s *Server) listOutbox(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.OutboxEntries(r.Context(), auth.UserID(r.Context()), r.URL.Query().Get("status"))
	respond(w, x, e)
}

func (s *Server) getOutboxEntry(w http.ResponseWriter, r *http.Request) {
	x, e := s.Store.OutboxEntry(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respond(w, x, e)
}

// approveOutboxEntry releases an application for sending. It does not send it:
// no sender exists yet, and when one does it will pick up approved entries.
func (s *Server) approveOutboxEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.Store.SetOutboxStatus(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), models.OutboxApproved)
	if err != nil && !isNotFound(err) {
		// A refused approval is the user's problem to fix, not a server fault.
		problem(w, 409, err.Error())
		return
	}
	respond(w, entry, err)
}

func (s *Server) cancelOutboxEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.Store.SetOutboxStatus(r.Context(), auth.UserID(r.Context()), r.PathValue("id"), models.OutboxCancelled)
	if err != nil && !isNotFound(err) {
		problem(w, 409, err.Error())
		return
	}
	respond(w, entry, err)
}
