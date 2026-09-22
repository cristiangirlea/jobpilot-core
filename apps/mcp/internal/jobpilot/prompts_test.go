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
	"job_id": true, "min_score": true, "apply_url": true, "needs_review": true,
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
				t.Errorf("%s mentions %q, which is neither a tool nor a known argument name", p.Name, word)
			}
			mentioned++
		}
		if mentioned < 3 {
			t.Errorf("%s names only %d tools; the workflow should walk the agent through several", p.Name, mentioned)
		}
	}
}
