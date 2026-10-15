package jobpilot

import (
	"context"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addPrompts registers the workflows worth encoding: preparing one
// application, the weekly look at the pipeline, and going through what waits
// for the user. They exist so the rules
// that matter (stop at a rejection, never claim to have applied, invent
// nothing) travel with the workflow instead of relying on the model to
// remember them from the server instructions.
func addPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "prepare_application",
		Title:       "Prepare an application",
		Description: "Walk one job from its match explanation to a tracked application, leaving the actual submission to the user.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "job_id",
			Description: "The job to prepare, as returned by list_jobs or get_dashboard.",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		jobID := ""
		if req.Params != nil {
			jobID = strings.TrimSpace(req.Params.Arguments["job_id"])
		}
		if jobID == "" {
			return nil, errors.New("job_id is required")
		}
		return &mcp.GetPromptResult{
			Description: "Prepare an application for " + jobID,
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: `Help me prepare my application for job ` + jobID + `.

1. Call get_job for ` + jobID + `. If the match is rejected, stop: tell me which rule rejected it, using the rejection reasons and get_filters to explain which of my filters or excluded employers applied. Nothing further is possible for a rejected job.
2. Call get_profile, and read the match breakdown, matched skills and missing skills.
3. Tell me how this role fits before writing anything: the score, what matched, what is missing, and any warnings or red flags worth knowing.
4. Call generate_documents to produce the recruiter message, cover letter, candidate summary and screening answers. Show me the cover letter in full.
5. Call create_application to track it.
6. Give me the job's apply link and tell me to open it and submit the application myself. ` + applyLinkOrder + ` You cannot submit it, and you must never say or imply that it has been submitted.
7. Once I confirm that I applied, call update_application_status with applied.

Use only facts from get_profile, get_job and generate_documents. Never invent experience, skills, employers, dates or claims. If something needed for a strong application is missing from my profile, say so instead of filling the gap.`},
			}},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "weekly_review",
		Title:       "Weekly pipeline review",
		Description: "Summarise the job search: pipeline health, what needs attention, and what to do next.",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "Review the job search pipeline",
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: `Give me a review of my job search.

1. Call get_dashboard for the headline numbers and the suggested next actions.
2. Call list_jobs with min_score 70 and limit 10 for my strongest current matches.
3. Call list_applications, and look at what sits in draft and ready as well as what is already applied or interviewing.
4. Call list_discovery_runs to check discovery is actually finding things, and flag any source errors.
5. Call get_filters, and list_jobs with state rejected and limit 10, to see which rules reject the best-scoring jobs.

Then tell me, briefly:
- How the pipeline looks and what changed worth noticing.
- Anything stalled: drafts I never sent, applications with no movement, matches I have not acted on.
- Whether discovery is healthy, and whether my filters look too narrow: name the rule behind the strongest rejections, from the filter itself.
- The two or three things worth doing next.

Do not change any application status without asking me first, and do not describe anything as submitted.`},
			}},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "review_pending_decisions",
		Title:       "Go through what waits for me",
		Description: "Walk the user through every draft and application waiting for their decision, one at a time, and act only on their word.",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "Go through what waits for the user",
			Messages: []*mcp.PromptMessage{{
				Role: "user",
				Content: &mcp.TextContent{Text: `Go through what is waiting for my decision in JobPilot.

1. Call list_pending_decisions. If nothing is open, say so and stop. Otherwise tell me how many items wait (open), then take them one at a time, oldest first. When a page is done and next is set, call list_pending_decisions again with after set to next for the following page, until next is empty.
2. For each item, follow its how_to_resolve: fetch what it names, show me the item in the exact words returned (every unapproved draft, every needs_review field, every blocker), and ask me how to proceed. Wait for my answer before moving on.
3. Act only on my explicit word: call approve_answer or approve_cv only when I approve that exact text, and pass my edited wording if I change it. If I say skip, leave the item as it is and go to the next one.
4. Approving or sending an application happens only in the web app: for those, read it with get_outbox_entry, tell me what still blocks it and that I approve it on the Outbox page. Never say or imply that an application was approved, submitted or sent.
5. When every page has been seen, call list_pending_decisions again without after and tell me what is still open.

Use only facts from my profile, CV and the jobs; never invent experience, skills or claims, and never write an answer for me without showing it first.`},
			}},
		}, nil
	})
}
