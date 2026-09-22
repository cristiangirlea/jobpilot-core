package jobpilot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxQuestions matches the API's own cap on one resolve call.
const maxQuestions = 50

type answerStatusInput struct {
	Status string `json:"status,omitempty" jsonschema:"pending for answers awaiting the user's approval, approved for reusable ones, or all"`
}

type saveAnswerInput struct {
	QuestionText string   `json:"question_text" jsonschema:"The question as the employer asked it"`
	Answer       string   `json:"answer" jsonschema:"The answer text, built only from facts in the profile or CV"`
	RoleFamily   string   `json:"role_family,omitempty" jsonschema:"Which kind of role this answer suits; defaults to general"`
	Technologies []string `json:"technologies,omitempty" jsonschema:"Technologies this answer is specific to, which decides where it is reused"`
	Approved     bool     `json:"approved,omitempty" jsonschema:"Set true only when the user has read this exact wording and confirmed it. Default false"`
}

type approveAnswerInput struct {
	AnswerID string `json:"answer_id" jsonschema:"The answer bank entry, from list_answers or a resolution's entry_id"`
	Approved *bool  `json:"approved,omitempty" jsonschema:"Defaults to true; pass false to withdraw approval"`
	Answer   string `json:"answer,omitempty" jsonschema:"Replacement wording, when the user edited it"`
}

type resolveInput struct {
	JobID     string     `json:"job_id"`
	Questions []Question `json:"questions" jsonschema:"The screening questions to answer, at most 50"`
}

type cvInput struct {
	CVID string `json:"cv_id" jsonschema:"A CV document id, from list_cvs"`
}

type approveCVInput struct {
	CVID     string `json:"cv_id"`
	Approved *bool  `json:"approved,omitempty" jsonschema:"Defaults to true; pass false to withdraw approval"`
}

type outboxInput struct {
	OutboxID string `json:"outbox_id" jsonschema:"An outbox entry id, from list_outbox"`
}

type outboxStatusInput struct {
	Status string `json:"status,omitempty" jsonschema:"needs_review, ready, approved, sent, failed, or cancelled; omit for all"`
}

type detectBoardInput struct {
	URL string `json:"url" jsonschema:"A link to a company's job board or to one of its postings"`
}

// addApplyTools registers the answer bank, CV and outbox tools.
//
// approve_outbox_entry is deliberately absent. Approving an entry is consent
// to send it, and the product's boundary is that the user releases their own
// applications. Approving an answer or a CV is different: the agent can show
// the exact text it is asking about, so the user is approving something they
// have read.
func addApplyTools(s *mcp.Server, c *Client) {
	addTool(s, &mcp.Tool{
		Name:        "get_ai_status",
		Title:       "Get AI adapter status",
		Description: "Report whether an AI provider is configured. When it is not, every answer and cover letter is deterministic, so no text is model-written and nothing carries the ai source.",
		Annotations: readOnly("Get AI adapter status"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, AIStatus, error) {
		out, err := c.AIStatus(ctx)
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:  "list_answers",
		Title: "List saved answers",
		Description: "Return the reusable answer bank. Pending answers are drafts the user has not approved yet and are never reused until they do; approved ones are reused for similar questions. " +
			"Show a pending answer's exact wording when asking the user to approve it.",
		Annotations: readOnly("List saved answers"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in answerStatusInput) (*mcp.CallToolResult, AnswerList, error) {
		status := strings.ToLower(strings.TrimSpace(in.Status))
		switch status {
		case "", "all":
			status = ""
		case "pending", "approved":
		default:
			return nil, AnswerList{}, fmt.Errorf("status must be pending, approved, or all, not %q", in.Status)
		}
		entries, err := c.Answers(ctx, status)
		if err != nil {
			return nil, AnswerList{}, toolError(err)
		}
		if entries == nil {
			entries = []AnswerEntry{}
		}
		return nil, AnswerList{Answers: entries}, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "resolve_screening_answers",
		Title: "Answer screening questions",
		Description: "Answer a job's screening questions from the profile and the answer bank, falling back to the AI adapter and then to templates. " +
			"Every resolution says where it came from (source: profile, bank, bank_similar, ai, template, or unanswered) and whether it still needs review. Anything marked needs_review is a draft: show the user its exact wording before it is used anywhere, and approve it with approve_answer only once they confirm. " +
			"Questions about protected characteristics are never answered, and declarations the user must make personally are left blank on purpose.",
		// Drafts are persisted unapproved, which is a write.
		Annotations: writeTool("Answer screening questions", false),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in resolveInput) (*mcp.CallToolResult, ResolvedAnswers, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, ResolvedAnswers{}, errors.New("job_id is required")
		}
		if len(in.Questions) == 0 {
			return nil, ResolvedAnswers{}, errors.New("give at least one question to answer")
		}
		if len(in.Questions) > maxQuestions {
			return nil, ResolvedAnswers{}, fmt.Errorf("at most %d questions per call, got %d", maxQuestions, len(in.Questions))
		}
		for i, q := range in.Questions {
			if strings.TrimSpace(q.Text) == "" {
				return nil, ResolvedAnswers{}, fmt.Errorf("question %d has no text", i+1)
			}
		}
		out, err := c.ResolveAnswers(ctx, in.JobID, in.Questions)
		if err != nil {
			return nil, ResolvedAnswers{}, toolError(err)
		}
		if out.Resolutions == nil {
			out.Resolutions = []Resolution{}
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "save_answer",
		Title: "Save an answer",
		Description: "Add an answer to the bank for reuse on similar questions. It is saved unapproved unless the user has read this exact wording and confirmed it, " +
			"because an unapproved answer is never reused and that is the safe default. Use only facts from the profile or an uploaded CV.",
		Annotations: writeTool("Save an answer", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in saveAnswerInput) (*mcp.CallToolResult, AnswerEntry, error) {
		if strings.TrimSpace(in.QuestionText) == "" {
			return nil, AnswerEntry{}, errors.New("question_text is required")
		}
		if strings.TrimSpace(in.Answer) == "" {
			return nil, AnswerEntry{}, errors.New("answer is required")
		}
		// The API treats a hand-written answer as self-approving when the
		// field is absent. An agent's is not, so it is always explicit.
		approved := in.Approved
		out, err := c.SaveAnswer(ctx, answerWrite{
			QuestionText: in.QuestionText, Answer: in.Answer,
			RoleFamily: in.RoleFamily, Technologies: in.Technologies, Approved: &approved,
		})
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:        "approve_answer",
		Title:       "Approve an answer",
		Description: "Approve a saved answer so it can be reused, optionally replacing its wording first. Only call this after the user has read the exact text and said yes.",
		Annotations: writeTool("Approve an answer", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in approveAnswerInput) (*mcp.CallToolResult, AnswerEntry, error) {
		if strings.TrimSpace(in.AnswerID) == "" {
			return nil, AnswerEntry{}, errors.New("answer_id is required")
		}
		approved := true
		if in.Approved != nil {
			approved = *in.Approved
		}
		out, err := c.UpdateAnswer(ctx, in.AnswerID, answerPatch{Answer: in.Answer, Approved: &approved})
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:        "list_cvs",
		Title:       "List CVs",
		Description: "Return the user's uploaded and tailored CVs: kind is base for an upload or tailored for one written for a job, and only an approved CV is ever reused. The CV text is omitted here; call get_cv for one. Uploading a CV is done in the web app.",
		Annotations: readOnly("List CVs"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, CVList, error) {
		cvs, err := c.CVs(ctx)
		if err != nil {
			return nil, CVList{}, toolError(err)
		}
		if cvs == nil {
			cvs = []CVDocument{}
		}
		return nil, CVList{CVs: cvs}, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "get_cv",
		Title: "Get a CV",
		Description: "Return one CV with its extracted text and the fields read out of it. The parsed warnings list says what could not be read, which is worth telling the user about. " +
			"These facts, with the profile, are the only source for anything written on the user's behalf.",
		Annotations: readOnly("Get a CV"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in cvInput) (*mcp.CallToolResult, CVDocument, error) {
		if strings.TrimSpace(in.CVID) == "" {
			return nil, CVDocument{}, errors.New("cv_id is required")
		}
		out, err := c.CV(ctx, in.CVID)
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:        "approve_cv",
		Title:       "Approve a CV",
		Description: "Approve a CV so it can be reused for similar roles. Only call this after the user has reviewed it. Re-uploading a CV clears its approval.",
		Annotations: writeTool("Approve a CV", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in approveCVInput) (*mcp.CallToolResult, CVDocument, error) {
		if strings.TrimSpace(in.CVID) == "" {
			return nil, CVDocument{}, errors.New("cv_id is required")
		}
		approved := in.Approved == nil || *in.Approved
		out, err := c.ApproveCV(ctx, in.CVID, approved)
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:  "tailor_cv_for_job",
		Title: "Choose or write a CV for a job",
		Description: "Decide which CV suits a job: keep the base one, reuse an earlier tailored one, or write a new one (decision: base, reused, or generated, with alignment from 0 to 1). Every role, employer, date and bullet is carried over unchanged; only the summary and emphasis differ. " +
			"A newly written CV is unapproved until the user reads it. Refused for rejected jobs.",
		Annotations: writeTool("Choose or write a CV for a job", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, TailoredCV, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, TailoredCV{}, errors.New("job_id is required")
		}
		out, err := c.TailorCV(ctx, in.JobID)
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:  "compose_application",
		Title: "Compose an application into the outbox",
		Description: "Assemble a complete application for a job into the outbox: a CV decision, a cover letter, and an answer for every field on the employer's form. " +
			"This does not send anything. The entry waits for the user, who approves and sends it in the web app. " +
			"Blockers say what stands in the way; show them verbatim. Recomposing replaces the entry and withdraws any approval it had. Refused for rejected jobs.",
		Annotations: writeTool("Compose an application into the outbox", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobInput) (*mcp.CallToolResult, OutboxDetail, error) {
		if strings.TrimSpace(in.JobID) == "" {
			return nil, OutboxDetail{}, errors.New("job_id is required")
		}
		entry, err := c.Compose(ctx, in.JobID)
		if err != nil {
			return nil, OutboxDetail{}, toolError(err)
		}
		return nil, detailOutbox(entry), nil
	})

	addTool(s, &mcp.Tool{
		Name:        "list_outbox",
		Title:       "List composed applications",
		Description: "Return composed applications with their status and any blockers. Approving and sending happen in the web app, not here.",
		Annotations: readOnly("List composed applications"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in outboxStatusInput) (*mcp.CallToolResult, OutboxList, error) {
		entries, err := c.Outbox(ctx, strings.ToLower(strings.TrimSpace(in.Status)))
		if err != nil {
			return nil, OutboxList{}, toolError(err)
		}
		out := OutboxList{Entries: make([]OutboxSummary, 0, len(entries))}
		for _, x := range entries {
			out.Entries = append(out.Entries, summarizeOutbox(x))
		}
		return nil, out, nil
	})

	addTool(s, &mcp.Tool{
		Name:  "get_outbox_entry",
		Title: "Get a composed application",
		Description: "Return one composed application in full: the cover letter (cover_letter_source: ai or template), every form answer with where it came from (source: profile, bank, bank_similar, ai, template, cv, cover_letter, declaration, or unanswered), and any blockers or notices. " +
			"Anything marked needs_review is a draft the user must read. Never describe an entry as approved or sent.",
		Annotations: readOnly("Get a composed application"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in outboxInput) (*mcp.CallToolResult, OutboxDetail, error) {
		if strings.TrimSpace(in.OutboxID) == "" {
			return nil, OutboxDetail{}, errors.New("outbox_id is required")
		}
		entry, err := c.OutboxEntry(ctx, in.OutboxID)
		if err != nil {
			return nil, OutboxDetail{}, toolError(err)
		}
		return nil, detailOutbox(entry), nil
	})

	addTool(s, &mcp.Tool{
		Name:        "cancel_outbox_entry",
		Title:       "Cancel a composed application",
		Description: "Withdraw a composed application from the outbox. Reversible: composing again rebuilds it.",
		Annotations: writeTool("Cancel a composed application", true),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in outboxInput) (*mcp.CallToolResult, OutboxDetail, error) {
		if strings.TrimSpace(in.OutboxID) == "" {
			return nil, OutboxDetail{}, errors.New("outbox_id is required")
		}
		entry, err := c.CancelOutboxEntry(ctx, in.OutboxID)
		if err != nil {
			return nil, OutboxDetail{}, toolError(err)
		}
		return nil, detailOutbox(entry), nil
	})

	addTool(s, &mcp.Tool{
		Name:        "detect_board",
		Title:       "Detect a job board",
		Description: "Read a careers page or posting URL and work out which board provider and identifier it uses, ready for add_discovery_source. Parses the URL only; it fetches nothing.",
		Annotations: readOnly("Detect a job board"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in detectBoardInput) (*mcp.CallToolResult, DetectedBoard, error) {
		if strings.TrimSpace(in.URL) == "" {
			return nil, DetectedBoard{}, errors.New("url is required: paste a link to a company's job board or one of its postings")
		}
		out, err := c.DetectBoard(ctx, in.URL)
		return nil, out, toolError(err)
	})

	addTool(s, &mcp.Tool{
		Name:        "list_board_suggestions",
		Title:       "List suggested boards",
		Description: "Return a curated list of employer job boards known to work as discovery sources, for when the user does not have one in mind.",
		Annotations: readOnly("List suggested boards"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, BoardSuggestionList, error) {
		suggestions, err := c.BoardSuggestions(ctx)
		if err != nil {
			return nil, BoardSuggestionList{}, toolError(err)
		}
		if suggestions == nil {
			suggestions = []BoardSuggestion{}
		}
		return nil, BoardSuggestionList{Suggestions: suggestions}, nil
	})
}
