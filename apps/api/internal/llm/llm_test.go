package llm

import (
	"context"
	"errors"
	"os"
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
// subprocess, which keeps the test cross-platform and dependency-free.
func TestCLIProviderReadsStdin(t *testing.T) {
	provider := &CLI{Command: os.Args[0], Args: []string{"-test.run=TestCLIHelperProcess", "--"}, Timeout: 20 * time.Second}
	t.Setenv("LLM_TEST_HELPER", "echo")
	out, err := provider.Complete(context.Background(), Request{System: "Be brief.", Prompt: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Be brief.") || !strings.Contains(out, "Hello") {
		t.Fatalf("system and prompt should both reach the CLI, got %q", out)
	}
}

func TestCLIProviderSurfacesFailure(t *testing.T) {
	provider := &CLI{Command: os.Args[0], Args: []string{"-test.run=TestCLIHelperProcess", "--"}, Timeout: 20 * time.Second}
	t.Setenv("LLM_TEST_HELPER", "fail")
	if _, err := provider.Complete(context.Background(), Request{Prompt: "Hello"}); err == nil {
		t.Fatal("expected a non-zero exit to surface as an error")
	}
}

func TestCLIProviderSubstitutesInlinePrompt(t *testing.T) {
	provider := &CLI{Command: os.Args[0], Args: []string{"-test.run=TestCLIHelperProcess", "--", PromptPlaceholder}, Timeout: 20 * time.Second}
	t.Setenv("LLM_TEST_HELPER", "args")
	out, err := provider.Complete(context.Background(), Request{Prompt: "inline prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "inline prompt") {
		t.Fatalf("expected the prompt to be passed as an argument, got %q", out)
	}
}

func TestCLIHelperProcess(t *testing.T) {
	mode := os.Getenv("LLM_TEST_HELPER")
	if mode == "" {
		t.Skip("helper process; not a real test")
	}
	switch mode {
	case "fail":
		os.Stderr.WriteString("simulated CLI failure\n")
		os.Exit(3)
	case "args":
		os.Stdout.WriteString(strings.Join(os.Args[len(os.Args)-1:], " "))
	default:
		buf := make([]byte, 4096)
		n, _ := os.Stdin.Read(buf)
		os.Stdout.Write(buf[:n])
	}
	os.Exit(0)
}
