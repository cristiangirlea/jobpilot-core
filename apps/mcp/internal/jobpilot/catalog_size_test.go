package jobpilot

import (
	"context"
	"encoding/json"
	"testing"
)

// catalogBudget bounds what an agent host downloads at the start of every
// session. It was 60 KB, about fifteen thousand tokens, before the inferred
// output schemas were dropped; the budget sits above the new size with room
// for a few more tools, and below the old one, so the regression is loud.
// #143 adds seven thin tools across four app areas; their compact input
// schemas and descriptions need 4 KiB beyond the original 30 KiB limit.
const catalogBudget = 34 * 1024

func TestCatalogStaysWithinBudget(t *testing.T) {
	cs, _ := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > catalogBudget {
		t.Fatalf("tools/list is %d bytes for %d tools; the budget is %d. Something is advertising a large schema again", len(raw), len(res.Tools), catalogBudget)
	}
	for _, tool := range res.Tools {
		schema, _ := json.Marshal(tool.OutputSchema)
		if len(schema) > 64 {
			t.Errorf("%s advertises a %d-byte output schema; every tool should go through addTool", tool.Name, len(schema))
		}
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
		}
	}
	t.Logf("tools/list: %d tools, %d bytes", len(res.Tools), len(raw))
}
