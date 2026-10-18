package generator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

type followUpProvider struct {
	text    string
	err     error
	request llm.Request
}

func (p *followUpProvider) Name() string  { return "fake" }
func (p *followUpProvider) Model() string { return "test" }
func (p *followUpProvider) Complete(_ context.Context, r llm.Request) (string, error) {
	p.request = r
	return p.text, p.err
}

func TestFollowUpWithoutProvider(t *testing.T) {
	p := models.Profile{Name: "Ada", Summary: "I build Go services."}
	sent := FollowUpMaterials{CV: "I build Go services.", CoverLetter: "I build Go services.", Answers: map[string]string{"Experience": "I build Go services."}}
	body, source := FollowUp(context.Background(), nil, p, sent)
	if source != "template" || !strings.Contains(body, "Ada") || !strings.Contains(body, "I build Go services.") {
		t.Fatal(body, source)
	}
	for _, absent := range []string{"Kubernetes", "interview", "ten days", "attached"} {
		if strings.Contains(body, absent) {
			t.Fatalf("invented %q: %s", absent, body)
		}
	}
	empty, _ := FollowUp(context.Background(), llm.New(nil, nil, 0, 0), models.Profile{}, FollowUpMaterials{})
	if !strings.Contains(empty, "application") || strings.Contains(empty, "Ada") {
		t.Fatal(empty)
	}
}

func TestFollowUpProviderCannotAddFacts(t *testing.T) {
	profile := models.Profile{Name: "Ada"}
	sent := FollowUpMaterials{CoverLetter: "I build Go services."}
	fallback, _ := FollowUp(context.Background(), nil, profile, sent)
	for _, tc := range []struct {
		text string
		err  error
	}{{"I have ten years of Kubernetes experience.", nil}, {"", nil}, {"", errors.New("offline")}} {
		provider := &followUpProvider{text: tc.text, err: tc.err}
		body, source := FollowUp(context.Background(), llm.New(provider, nil, 0, 0), profile, sent)
		if body != fallback || source != "template" {
			t.Fatal(body, source)
		}
		if !strings.Contains(provider.request.Prompt, sent.CoverLetter) {
			t.Fatal("sent facts missing")
		}
	}
	provider := &followUpProvider{text: fallback}
	body, source := FollowUp(context.Background(), llm.New(provider, nil, 0, 0), profile, sent)
	if body != fallback || source != "ai" {
		t.Fatal(body, source)
	}
}
