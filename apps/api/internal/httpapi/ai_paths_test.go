package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Members draft without the AI provider (#128) only as long as every path
// that reaches it takes its client from llmFor. This reads the package's
// source, like the routes test, because a handler that went back to s.LLM
// would still pass every test that runs without a provider.
func TestEveryAIPathTakesTheRequestsClient(t *testing.T) {
	allowed := map[string]int{
		"roles.go":  1, // llmForUser hands it to admins
		"server.go": 1, // aiStatus reads Status once for the admin-only route
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(source), "s.LLM"); got != allowed[name] {
			t.Errorf("%s uses s.LLM %d times, expected %d: take the client from s.llmFor(ctx) so members get the deterministic drafts", name, got, allowed[name])
		}
	}
}
