package jobpilot

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// argumentNames are snake_case words the prompts use that are tool arguments
// or result fields rather than tool names. Anything else in snake_case must be
// a real tool, so a renamed tool cannot leave a prompt quietly pointing at a
// name that no longer exists.
var argumentNames = map[string]bool{
	"job_id": true, "min_score": true, "apply_url": true, "employer_url": true, "needs_review": true,
	"how_to_resolve": true,
}

var snakeCase = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)

func TestPromptsOnlyNameToolsThatExist(t *testing.T) {
	cs, _ := session(t)
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exists := map[string]bool{}
	for _, tool := range tools.Tools {
		exists[tool.Name] = true
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prompts.Prompts {
		args := map[string]string{}
		for _, a := range p.Arguments {
			args[a.Name] = "job-a"
		}
		got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: p.Name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		var text strings.Builder
		for _, m := range got.Messages {
			if tc, ok := m.Content.(*mcp.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		mentioned := 0
		for _, word := range snakeCase.FindAllString(text.String(), -1) {
			if argumentNames[word] {
				continue
			}
			if !exists[word] {
				t.Errorf("%s mentions %q, which is neither a tool nor a known argument name; if it is a field or argument, add it to argumentNames in prompts_test.go", p.Name, word)
			}
			mentioned++
		}
		if mentioned < 3 {
			t.Errorf("%s names only %d tools; the workflow should walk the agent through several", p.Name, mentioned)
		}
	}
}

// The host is told which link to give the user (the employer's page, else the
// apply URL, as models.Job.FormTarget in apps/api reads the form), in one
// sentence, wherever it hands out a link. The partner link is the outbound
// endpoint's alone and never reaches the MCP server.
func TestApplyLinkOrderIsStatedOnce(t *testing.T) {
	employer, apply := strings.Index(applyLinkOrder, "employer_url"), strings.Index(applyLinkOrder, "apply_url")
	if employer < 0 || apply < 0 || employer > apply {
		t.Errorf("applyLinkOrder should name employer_url before apply_url: %q", applyLinkOrder)
	}
	if strings.Contains(strings.ToLower(applyLinkOrder), "partner") {
		t.Errorf("applyLinkOrder should never mention a partner link: %q", applyLinkOrder)
	}
	if !strings.Contains(instructions, applyLinkOrder) {
		t.Error("server instructions should state the apply link order")
	}
	cs, _ := session(t)
	got, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "prepare_application", Arguments: map[string]string{"job_id": "job-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if text := got.Messages[0].Content.(*mcp.TextContent).Text; !strings.Contains(text, applyLinkOrder) {
		t.Error("prepare_application should state the apply link order")
	}
}

// Found in early use: the review asks whether the filters are too
// narrow, but never had the model read them, so it could only guess.
func TestWeeklyReviewReadsTheFilters(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "weekly_review"})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, m := range res.Messages {
		if tc, ok := m.Content.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !strings.Contains(text.String(), "get_filters") {
		t.Fatalf("the review should read the filters before judging them: %s", text.String())
	}
}
