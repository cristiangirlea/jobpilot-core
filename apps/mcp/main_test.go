package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A server launched with no credentials used to print usage to stderr and
// exit before the handshake, so the host showed only "connection closed" and
// the reason sat in a stream nobody sees. It must complete the handshake,
// list its tools, and explain the problem in the one place the model and
// the user look: the tool result.
func TestStartsAndExplainsWhenCredentialsAreMissing(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "jobpilot-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd := exec.Command(bin)
	// No token, no email, no password; an API URL nobody listens on, to
	// prove the explanation does not depend on reaching the API.
	cmd.Env = append(os.Environ(), "JOBPILOT_TOKEN=", "JOBPILOT_EMAIL=", "JOBPILOT_PASSWORD=", "JOBPILOT_API_URL=http://127.0.0.1:1")
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "no-creds", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("the server must complete the handshake without credentials: %v", err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 30 {
		t.Fatalf("the full catalog should still be listed, got %d tools", len(tools.Tools))
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_profile"})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if !res.IsError {
		t.Fatalf("a tool call without credentials must be an error: %s", text.String())
	}
	for _, want := range []string{"not configured", "token", "agent-token.ps1", "restart"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the error should mention %q so the user knows what to do: %s", want, text.String())
		}
	}
}

// Found in early use: once the binary was registered with its own
// token, the checked-in .mcp.json still made every session warn that
// JOBPILOT_TOKEN was missing. Each variable it names has a default, so the
// file works without one, and the server explains a missing token itself.
func TestCheckedInConfigNeedsNoEnvironment(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range regexp.MustCompile(`\$\{[^}]*\}`).FindAllString(string(raw), -1) {
		if !strings.Contains(ref, ":-") {
			t.Errorf("%s has no default, so a session without it warns", ref)
		}
	}
}
