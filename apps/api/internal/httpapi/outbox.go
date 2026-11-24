package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"jobpilot/core/api/internal/answers"
	"jobpilot/core/api/internal/ats"
	"jobpilot/core/api/internal/auth"
	"jobpilot/core/api/internal/discovery"
	"jobpilot/core/api/internal/generator"
	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
	"jobpilot/core/api/internal/store"

	"github.com/jackc/pgx/v5"
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
	if job.Job.ClosedAt != nil {
		problem(w, 409, closedMessage(job.Job.ClosedReason))
		return
	}

	// The employer's form, where it is published, read before anything costly
	// is produced: it is also the best evidence that the posting still exists.
	// A form that reads proves it is open; the ATS saying the posting is gone
	// closes it. Only when there is no form to read is the link itself
	// checked, since postings close between the background checks.
	//
	// A posting found through an aggregator is read at the employer. While
	// that is not known yet, the link is checked first, which also asks the
	// aggregator where the posting applies; the check stores that page, and
	// the job carries it from here on.
	link := store.LinkToCheck{ID: job.Job.ID, ApplyURL: job.Job.ApplyURL, SourceProvider: job.Job.SourceProvider, EmployerURL: job.Job.EmployerURL}
	canCheck := link.ApplyURL != "" && s.Discovery != nil
	checked, closedNow, reason := false, false, ""
	if canCheck && link.EmployerURL == "" && discovery.RedirectsToEmployer(link.SourceProvider) {
		result := s.Discovery.CheckJob(ctx, link, 8*time.Second)
		checked, closedNow, reason, job.Job.EmployerURL = true, result.Closed, result.Reason, result.EmployerURL
	}
	var form ats.Form
	var formErr error
	if !closedNow {
		form, formErr = s.ATS.Fetch(ctx, job.Job.FormTarget())
		switch {
		case errors.Is(formErr, ats.ErrNotFound):
			closedNow, reason = true, discovery.ReasonBoard
			if err := s.Store.RecordLinkCheck(ctx, job.Job.ID, true, reason, ""); err != nil {
				internal(w, err)
				return
			}
		case errors.Is(formErr, ats.ErrUnsupported) && canCheck && !checked:
			result := s.Discovery.CheckJob(ctx, link, 8*time.Second)
			closedNow, reason = result.Closed, result.Reason
		}
	}
	if closedNow {
		problem(w, 409, closedMessage(reason))
		return
	}

	profile, err := s.Store.Profile(ctx, uid)
	if err != nil {
		internal(w, err)
		return
	}

	entry := models.OutboxEntry{JobID: job.Job.ID, Channel: models.ChannelManual, Blockers: []string{}, Notices: []string{}}

	// The CV. A generated one arrives unapproved, which is itself a blocker.
	decision, kept, err := s.keptCV(ctx, uid, job.Job.ID)
	if err != nil {
		internal(w, err)
		return
	}
	if !kept {
		decision, err = s.decideCV(ctx, uid, job, profile)
		if err != nil {
			internal(w, err)
			return
		}
	}
	entry.CVDocumentID, entry.CVDecision = decision.CV.ID, decision.Decision
	if !decision.Approved {
		entry.Blockers = append(entry.Blockers, store.CVBlocker)
	}

	composeCoverLetter(ctx, s.llmFor(ctx), &entry, profile, job)

	// The form read above decides the channel and the answers.
	entry.ATSProvider = form.Provider
	switch {
	case formErr == nil:
		entry.Channel = models.ChannelATSForm
		entry.Answers, entry.NeedsReview = s.answerForm(ctx, uid, form, profile, job)
	case errors.Is(formErr, ats.ErrUnsupported):
		entry.Blockers = append(entry.Blockers, unsupportedReason(form.Provider))
	default:
		entry.Blockers = append(entry.Blockers, "The employer's application form could not be read: "+formErr.Error())
	}
	if formErr == nil {
		entry.Form = formToMap(form)
		declarations := 0
		for _, answer := range entry.Answers {
			if answer.Source == "declaration" {
				declarations++
				// A declaration is the applicant's own statement. It blocks, but
				// saying it "has no answer" would misdescribe why.
				if answer.Required {
					entry.Blockers = append(entry.Blockers, fmt.Sprintf("You have to make this declaration yourself on the employer's form: %q", answer.Label))
				}
				continue
			}
			if answer.Source == "upload" {
				if answer.Required {
					entry.Blockers = append(entry.Blockers, fmt.Sprintf("You have to upload this yourself on the employer's form: %q", answer.Label))
				}
				continue
			}
			if answer.Source == "personal" {
				if answer.Required {
					entry.Blockers = append(entry.Blockers, fmt.Sprintf("Only you can choose this, on the employer's form: %q", answer.Label))
				}
				continue
			}
			if answer.Required && strings.TrimSpace(answer.Answer) == "" {
				entry.Blockers = append(entry.Blockers, fmt.Sprintf("A required question has no answer yet: %q", answer.Label))
			}
		}
		if declarations > 0 {
			entry.Notices = append(entry.Notices, fmt.Sprintf("%d question(s) ask you to declare something personally, such as confirming the wording is your own. Read the letter and answers before agreeing.", declarations))
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
	if errors.Is(err, store.ErrOutboxSent) || errors.Is(err, store.ErrOutboxSending) {
		problem(w, 409, err.Error())
		return
	}
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

func composeCoverLetter(ctx context.Context, ai *llm.Client, entry *models.OutboxEntry, profile models.Profile, job models.JobWithMatch) {
	entry.CoverLetter, entry.CoverLetterSource = generator.CoverLetter(ctx, ai, profile, job.Job, job.Match)
	switch entry.CoverLetterSource {
	case generator.LetterSourceAIBackup:
		entry.Notices = append(entry.Notices, "The backup AI model wrote this cover letter because the main one was unavailable. Read it closely before you approve this application.")
	case generator.LetterSourceTemplate:
		if ai.Enabled() {
			entry.Notices = append(entry.Notices, "The cover letter fell back to the deterministic template because the AI provider did not respond.")
		}
	}
}

// answerForm resolves every question on a published form. Resume and cover
// letter fields are filled from the composed application rather than the answer
// bank, and demographic questions never reach here at all.
func (s *Server) answerForm(ctx context.Context, uid string, form ats.Form, profile models.Profile, job models.JobWithMatch) ([]models.OutboxAnswer, int) {
	service := s.answerService(ctx)
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
		switch {
		case field.Role == ats.RoleResume:
			answer.Answer, answer.Source = "(the approved CV is attached)", "cv"
		case field.Role == ats.RoleCoverLetter:
			answer.Answer, answer.Source = "(the composed cover letter is attached)", "cover_letter"
		case field.Role == ats.RoleDeclaration:
			// An undertaking the applicant makes personally. Ticking it for them
			// would be making a statement on their behalf.
			answer.Source, answer.NeedsReview = "declaration", true
		case field.Role == ats.RolePersonal:
			// A choice that states something about the applicant, such as a
			// salutation stating gender, is never inferred for them.
			answer.Source, answer.NeedsReview = "personal", true
		case field.Type == ats.TypeFile:
			// Any other upload, such as a transcript, is a document only the
			// applicant has. A drafted sentence is not a file, and answering it
			// with one would hide that the form still needs something.
			answer.Source, answer.NeedsReview = "upload", true
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
	// Aggregator pages end here, and their feeds do not say where the
	// employer's own form is. The fix is a source whose links are forms.
	return "This apply link is not an application form this software can read, so it has to be submitted by hand. If the employer has a Greenhouse, Lever or Ashby board, add it as a discovery source: its postings link straight to forms this software can read."
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
// Only a browser session reaches it (the consent wrapper).
func (s *Server) approveOutboxEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.Store.ApproveOutboxEntry(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	if err == nil && entry.Status == models.OutboxApproved && s.Sender != nil {
		s.Sender.Kick()
	}
	respondOutbox(w, entry, err)
}

func (s *Server) cancelOutboxEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := s.Store.CancelOutboxEntry(r.Context(), auth.UserID(r.Context()), r.PathValue("id"))
	respondOutbox(w, entry, err)
}

// respondOutbox answers a refused approval or cancellation with 409: the
// user's problem to fix, not a server fault.
func respondOutbox(w http.ResponseWriter, entry models.OutboxEntry, err error) {
	var refusal store.OutboxRefusal
	if errors.As(err, &refusal) {
		problem(w, 409, refusal.Error())
		return
	}
	respond(w, entry, err)
}

// keptCV is the tailored CV an application already carries, once the user has
// approved it. Preparing the application again keeps it: tailoring afresh can
// produce a different draft (an AI-written summary never repeats exactly), and
// a new draft would undo the approval the user just gave.
//
// It is kept only while it was built from the CV the user uploaded last, so a
// newer upload still reaches the application. Withdrawing the approval is the
// way to ask for a fresh one.
func (s *Server) keptCV(ctx context.Context, uid, jobID string) (cvDecision, bool, error) {
	previous, err := s.Store.OutboxEntryForJob(ctx, uid, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cvDecision{}, false, nil
	}
	if err != nil || previous.CVDocumentID == "" {
		return cvDecision{}, false, err
	}
	doc, err := s.Store.CV(ctx, uid, previous.CVDocumentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cvDecision{}, false, nil
	}
	if err != nil || !doc.Approved || doc.Kind != "tailored" {
		return cvDecision{}, false, err
	}
	baseID := ""
	base, err := s.Store.LatestBaseCV(ctx, uid)
	switch {
	case err == nil:
		baseID = base.ID
	case !errors.Is(err, pgx.ErrNoRows):
		return cvDecision{}, false, err
	}
	if doc.ParentID != baseID {
		return cvDecision{}, false, nil
	}
	return cvDecision{Decision: "reused", Reason: "Keeping the CV you approved for this application.", CV: doc, Approved: true}, true, nil
}

// closedMessage is the refusal for a posting that has closed.
func closedMessage(reason string) string {
	if reason == "" {
		reason = "it is no longer available"
	}
	return "This posting has closed: " + reason + ". It can no longer be applied to."
}
