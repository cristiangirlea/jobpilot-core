package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSecret(t *testing.T) {
	const name = "TEST_SECRET"
	const content = "secret-content-never-log"
	tests := []struct {
		name    string
		value   string
		kind    string
		content string
		want    string
		wantErr string
	}{
		{name: "plain value", value: content, want: content},
		{name: "LF", kind: "file", content: content + "\n", want: content},
		{name: "CRLF", kind: "file", content: content + "\r\n", want: content},
		{name: "no newline", kind: "file", content: content, want: content},
		{name: "two newlines", kind: "file", content: content + "\n\n", want: content + "\n"},
		{name: "two CRLFs", kind: "file", content: content + "\r\n\r\n", want: content + "\r\n"},
		{name: "preserve spaces", kind: "file", content: " " + content + " ", want: " " + content + " "},
		{name: "preserve CR", kind: "file", content: content + "\r", want: content + "\r"},
		{name: "both set", value: content, kind: "file", content: content, wantErr: "set TEST_SECRET or TEST_SECRET_FILE, not both"},
		{name: "missing file", kind: "missing", wantErr: "TEST_SECRET_FILE"},
		{name: "directory", kind: "directory", wantErr: "regular file"},
		{name: "empty file", kind: "file", wantErr: "empty secret"},
		{name: "only LF", kind: "file", content: "\n", wantErr: "empty secret"},
		{name: "only CRLF", kind: "file", content: "\r\n", wantErr: "empty secret"},
		{name: "over limit", kind: "file", content: content + strings.Repeat("x", 64*1024), wantErr: "64 KiB"},
		{name: "at limit", kind: "file", content: strings.Repeat("x", 64*1024), want: strings.Repeat("x", 64*1024)},
		{name: "neither set", want: "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(name, tt.value)
			t.Setenv(name+"_FILE", "")
			path := ""
			if tt.kind != "" {
				dir := t.TempDir()
				path = filepath.Join(dir, "secret")
				switch tt.kind {
				case "file":
					if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
						t.Fatal(err)
					}
				case "directory":
					path = dir
				}
				t.Setenv(name+"_FILE", path)
			}
			got, err := secret(name, "default")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected a secret-file error")
				}
				message := err.Error()
				if !strings.Contains(message, tt.wantErr) || !strings.Contains(message, name+"_FILE") || !strings.Contains(message, strconv.Quote(path)) {
					t.Fatalf("error must name the variable, path and reason: %v", err)
				}
				if strings.Contains(message, content) {
					t.Fatal("error exposed secret content")
				}
				if got != "" {
					t.Fatal("failed secret read returned a value")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatal("secret value differs from the expected value")
			}
		})
	}
}

// A server launched with no credentials used to print usage to stderr and
// exit before the handshake, so the host showed only "connection closed" and
// the reason sat in a stream nobody sees. It must complete the handshake,
// list its tools, and explain the problem in the one place the model and
// the user look: the tool result.
func TestStartsAndExplainsWhenCredentialsAreMissing(t *testing.T) {
	text := profileToolError(t, buildMCP(t), "", "")
	for _, want := range []string{"not configured", "token", "agent-token.ps1", "restart"} {
		if !strings.Contains(text, want) {
			t.Errorf("the error should mention %q so the user knows what to do: %s", want, text)
		}
	}
}

func TestStartsAndExplainsSecretFileErrors(t *testing.T) {
	bin := buildMCP(t)
	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "missing file"},
		{name: "both set", token: "test-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			want := []string{"JOBPILOT_TOKEN_FILE", strconv.Quote(path)}
			if tc.token != "" {
				if err := os.WriteFile(path, []byte("file-token"), 0644); err != nil {
					t.Fatal(err)
				}
				want = []string{"not both"}
			}
			text := profileToolError(t, bin, tc.token, path)
			for _, fragment := range want {
				if !strings.Contains(text, fragment) {
					t.Errorf("the tool error should mention %q: %s", fragment, text)
				}
			}
		})
	}
}

func buildMCP(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jobpilot-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func profileToolError(t *testing.T, bin, token, tokenFile string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cmd := exec.Command(bin)
	// Isolate credentials and use an unreachable API so errors need no network.
	cmd.Env = append(os.Environ(), "JOBPILOT_TOKEN="+token, "JOBPILOT_TOKEN_FILE="+tokenFile, "JOBPILOT_EMAIL=", "JOBPILOT_PASSWORD=", "JOBPILOT_PASSWORD_FILE=", "JOBPILOT_CV_DIR=", "JOBPILOT_API_URL=http://127.0.0.1:1")
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bad-creds", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("the server must complete the handshake with invalid credentials: %v", err)
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
		t.Fatalf("a tool call with invalid credentials must be an error: %s", text.String())
	}
	return text.String()
}

// Found in early use: a project's .mcp.json overrides a server of
// the same name registered for the user, so the checked-in one, which runs
// from source with whatever token the session's environment holds, replaced
// the registered binary and its token in every session opened in this
// repository, and every tool answered "not configured". The from-source
// config is now an example to copy, and a copy is never committed.
func TestProjectConfigNeverShadowsTheRegisteredServer(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json.example"))
	if err != nil {
		t.Fatalf("the from-source config should ship as an example: %v", err)
	}
	for _, ref := range regexp.MustCompile(`\$\{[^}]*\}`).FindAllString(string(raw), -1) {
		if !strings.Contains(ref, ":-") {
			t.Errorf("%s has no default, so a session without it warns", ref)
		}
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	ignored := false
	for _, line := range strings.Split(string(ignore), "\n") {
		if strings.TrimSpace(line) == ".mcp.json" || strings.TrimSpace(line) == "/.mcp.json" {
			ignored = true
		}
	}
	if !ignored {
		t.Error(".gitignore should list .mcp.json, so a local copy of the example is never committed")
	}
}
