package llm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type stubProvider struct {
	calls int
	reply string
	err   error
}

func (s *stubProvider) Name() string  { return "stub" }
func (s *stubProvider) Model() string { return "stub-1" }
func (s *stubProvider) Complete(context.Context, Request) (string, error) {
	s.calls++
	return s.reply, s.err
}

type memoryCache map[string]string

func (m memoryCache) LookupCompletion(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}
func (m memoryCache) StoreCompletion(_ context.Context, key, _, _, _, response string) error {
	m[key] = response
	return nil
}

func TestDisabledClientFallsBack(t *testing.T) {
	client, err := Build(Options{Provider: "none"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.Enabled() {
		t.Fatal("expected a disabled client by default")
	}
	if _, err = client.Complete(context.Background(), Request{Prompt: "hi"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCacheHitSkipsProvider(t *testing.T) {
	provider := &stubProvider{reply: "cached answer"}
	client := New(provider, memoryCache{}, 0, time.Hour)
	request := Request{Task: "answer_question", Prompt: "Why us?"}

	first, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || first.Text != "cached answer" {
		t.Fatalf("unexpected first result: %+v", first)
	}
	second, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.Text != "cached answer" {
		t.Fatalf("expected a cache hit, got %+v", second)
	}
	if provider.calls != 1 {
		t.Fatalf("expected the provider to be called once, got %d", provider.calls)
	}
}

func TestCacheKeyChangesWithModel(t *testing.T) {
	request := Request{Task: "t", Prompt: "p"}
	if CacheKey("cli", "opus", request) == CacheKey("cli", "haiku", request) {
		t.Fatal("a model switch must not reuse another model's cached answer")
	}
}

func TestBudgetIsEnforced(t *testing.T) {
	provider := &stubProvider{reply: "ok"}
	client := New(provider, nil, 2, time.Hour)
	for i := 0; i < 2; i++ {
		if _, err := client.Complete(context.Background(), Request{Prompt: strings.Repeat("a", i+1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Complete(context.Background(), Request{Prompt: "third"}); !errors.Is(err, ErrBudget) {
		t.Fatalf("expected ErrBudget, got %v", err)
	}
}

func TestEmptyResponseIsAnError(t *testing.T) {
	client := New(&stubProvider{reply: "   "}, nil, 0, time.Hour)
	if _, err := client.Complete(context.Background(), Request{Prompt: "p"}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("expected ErrEmpty, got %v", err)
	}
}

func TestBuildRejectsCLIWithoutCommand(t *testing.T) {
	if _, err := Build(Options{Provider: "cli"}, nil); err == nil {
		t.Fatal("expected an error when LLM_CLI_COMMAND is missing")
	}
	if _, err := Build(Options{Provider: "wat"}, nil); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

// The CLI provider is exercised by re-running this test binary as the
// subprocess, which keeps the test cross-platform and dependency-free. The
// helper's mode travels as an argument, so the real environment filter applies.
func helperCLI(mode string, extra ...string) *CLI {
	args := append([]string{"-test.run=TestCLIHelperProcess", "--", "helper=" + mode}, extra...)
	return &CLI{Command: os.Args[0], Args: args, Timeout: 20 * time.Second}
}

func TestCLIProviderReadsStdin(t *testing.T) {
	out, err := helperCLI("echo").Complete(context.Background(), Request{System: "Be brief.", Prompt: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Be brief.") || !strings.Contains(out, "Hello") {
		t.Fatalf("system and prompt should both reach the CLI, got %q", out)
	}
}

func TestCLIProviderSurfacesFailure(t *testing.T) {
	if _, err := helperCLI("fail").Complete(context.Background(), Request{Prompt: "Hello"}); err == nil {
		t.Fatal("expected a non-zero exit to surface as an error")
	}
}

func TestCLIProviderSubstitutesInlinePrompt(t *testing.T) {
	out, err := helperCLI("args", PromptPlaceholder, "").Complete(context.Background(), Request{Prompt: "inline prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "[inline prompt][]" {
		t.Fatalf("expected the prompt and an empty argument to arrive as separate arguments, got %q", out)
	}
}

func TestCLIProviderPassesOnlyAllowedEnvironment(t *testing.T) {
	t.Setenv("JWT_SECRET", "not-for-the-cli")
	t.Setenv("DATABASE_URL", "postgres://not-for-the-cli")
	t.Setenv("SMTP_PASSWORD", "not-for-the-cli")
	t.Setenv("GOOGLE_CLIENT_SECRET", "not-for-the-cli")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "for=the=cli")
	t.Setenv("HOME", "/home/jobpilot")
	t.Setenv("USE_BUILTIN_RIPGREP", "0")
	t.Setenv("DISABLE_AUTOUPDATER", "1")
	out, err := helperCLI("env").Complete(context.Background(), Request{Prompt: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			got[strings.ToUpper(name)] = value
		}
	}
	for _, secret := range []string{"JWT_SECRET", "DATABASE_URL", "SMTP_PASSWORD", "GOOGLE_CLIENT_SECRET"} {
		if _, found := got[secret]; found {
			t.Fatalf("%s reached the CLI", secret)
		}
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "for=the=cli", "HOME": "/home/jobpilot", "USE_BUILTIN_RIPGREP": "0", "DISABLE_AUTOUPDATER": "1"}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("%s should reach the CLI as %q, got %q", name, value, got[name])
		}
	}
	if got["PATH"] == "" {
		t.Fatal("PATH should reach the CLI")
	}
}

func TestCLIEnvFilter(t *testing.T) {
	got := cliEnv([]string{"Path=C:\bin", "=C:=C:\\", "noequals", "JWT_SECRET=x", "openai_api_key=k", "X=y"})
	if strings.Join(got, ";") != "Path=C:\bin;openai_api_key=k" {
		t.Fatalf("unexpected environment: %q", got)
	}
}

func TestCLIProviderRunsInAnEmptyFolderItRemoves(t *testing.T) {
	out, err := helperCLI("cwd").Complete(context.Background(), Request{Prompt: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	dir, count, _ := strings.Cut(out, "\n")
	here, _ := os.Getwd()
	if dir == "" || dir == here || count != "0" {
		t.Fatalf("the CLI should run in an empty folder of its own, got %q with %q entries", dir, count)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the CLI's folder %q should be removed after the call", dir)
	}
}

func TestCLIProviderRunsInAFolderOfItsOwnUnderDir(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(parent+"/left-by-someone", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := helperCLI("cwd")
	cli.Dir = parent
	var dirs []string
	for range 2 {
		out, err := cli.Complete(context.Background(), Request{Prompt: "Hello"})
		if err != nil {
			t.Fatal(err)
		}
		dir, count, _ := strings.Cut(out, "\n")
		if count != "0" || !sameDir(t, filepath.Dir(dir), parent) {
			t.Fatalf("each call needs an empty folder of its own under %q, got %q with %q entries", parent, dir, count)
		}
		dirs = append(dirs, dir)
	}
	if dirs[0] == dirs[1] {
		t.Fatal("two calls shared a folder")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the configured folder should keep only its own file, got %v (%v)", entries, err)
	}
}

func TestCLIProviderReportsAMissingFolder(t *testing.T) {
	cli := helperCLI("echo")
	cli.Dir = t.TempDir() + "/missing"
	if _, err := cli.Complete(context.Background(), Request{Prompt: "Hello"}); err == nil || !strings.Contains(err.Error(), "no working folder") {
		t.Fatalf("expected a working folder error, got %v", err)
	}
}

func TestCLIProviderRefusesOversizedOutput(t *testing.T) {
	if _, err := helperCLI("flood").Complete(context.Background(), Request{Prompt: "Hello"}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("expected an oversized output error, got %v", err)
	}
}

func TestCLIProviderTimesOut(t *testing.T) {
	cli := helperCLI("sleep")
	cli.Timeout = 300 * time.Millisecond
	start := time.Now()
	_, err := cli.Complete(context.Background(), Request{Prompt: "Hello"})
	if err == nil || !strings.Contains(err.Error(), "timed out") || time.Since(start) > 10*time.Second {
		t.Fatalf("expected a prompt timeout, got %v after %s", err, time.Since(start))
	}
}

func TestCLIProviderRefusesOutputAChildKeptOpen(t *testing.T) {
	cli := helperCLI("orphan")
	cli.waitDelay = 300 * time.Millisecond
	start := time.Now()
	_, err := cli.Complete(context.Background(), Request{Prompt: "Hello"})
	if err == nil || !strings.Contains(err.Error(), "kept its output open") || time.Since(start) > 10*time.Second {
		t.Fatalf("expected the cut-short output to be refused, got %v after %s", err, time.Since(start))
	}
	time.Sleep(2 * time.Second) // let the child go, so Windows can remove the test binary
}

func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

func TestCLIHelperProcess(t *testing.T) {
	var mode string
	var rest []string
	for i, arg := range os.Args {
		if strings.HasPrefix(arg, "helper=") {
			mode, rest = strings.TrimPrefix(arg, "helper="), os.Args[i+1:]
		}
	}
	if mode == "" {
		t.Skip("helper process; not a real test")
	}
	switch mode {
	case "fail":
		os.Stderr.WriteString("simulated CLI failure\n")
		os.Exit(3)
	case "args":
		for _, arg := range rest {
			os.Stdout.WriteString("[" + arg + "]")
		}
	case "env":
		os.Stdout.WriteString(strings.Join(os.Environ(), "\n"))
	case "cwd":
		dir, err := os.Getwd()
		if err != nil {
			os.Exit(4)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			os.Exit(5)
		}
		os.Stdout.WriteString(dir + "\n" + strconv.Itoa(len(entries)))
	case "flood":
		chunk := []byte(strings.Repeat("x", 64<<10))
		for range 20 {
			os.Stdout.Write(chunk)
		}
	case "sleep":
		time.Sleep(time.Minute)
	case "linger":
		time.Sleep(time.Second)
	case "orphan":
		// A child that keeps stdout open after this process exits.
		child := exec.Command(os.Args[0], "-test.run=TestCLIHelperProcess", "--", "helper=linger")
		child.Stdout = os.Stdout
		if child.Start() != nil {
			os.Exit(6)
		}
		os.Stdout.WriteString("a draft cut sho")
	default:
		buf := make([]byte, 4096)
		n, _ := os.Stdin.Read(buf)
		os.Stdout.Write(buf[:n])
	}
	os.Exit(0)
}
