package generator

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

type FollowUpMaterials struct {
	CV          string            `json:"cv"`
	CoverLetter string            `json:"cover_letter"`
	Answers     map[string]string `json:"answers"`
}

func FollowUp(ctx context.Context, client *llm.Client, profile models.Profile, sent FollowUpMaterials) (string, string) {
	opening := "Hello,"
	subject := "I am following up on my application."
	request := "Could you let me know if there is an update on the application?"
	closing := "Thank you,"
	if name := strings.Join(strings.Fields(profile.Name), " "); name != "" {
		closing += "\n" + name
	}
	parts := []string{opening, subject}
	excerpt := ""
	candidates := []string{sent.CoverLetter, sent.CV}
	keys := make([]string, 0, len(sent.Answers))
	for key := range sent.Answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		candidates = append(candidates, sent.Answers[key])
	}
	for _, text := range candidates {
		text = strings.TrimSpace(text)
		if text != "" && len(text) <= 300 && !strings.ContainsAny(text, "\r\n") {
			excerpt = "In my application I wrote: \"" + text + "\""
			break
		}
	}
	if excerpt != "" {
		parts = append(parts, excerpt)
	}
	parts = append(parts, request, closing)
	fallback := strings.Join(parts, "\n\n")
	if client.Enabled() {
		facts, _ := json.Marshal(sent)
		result, err := client.Complete(ctx, llm.Request{Task: "follow_up", System: "Write a short follow-up draft, never a sent message. Use only the supplied profile and sent materials. Return the supplied paragraphs verbatim, separated by blank lines; you may omit the quotation paragraph. Do not add facts, attachments, dates, or interview claims.", Prompt: "PROFILE\n" + letterFacts(profile) + "\nSENT MATERIALS\n" + string(facts) + "\nALLOWED PARAGRAPHS\n" + fallback, MaxTokens: 400, Temperature: 0.2})
		if err == nil {
			text := strings.TrimSpace(result.Text)
			// Restrict provider output to verified wording rather than trusting factual claims.
			withoutQuote := strings.Join([]string{opening, subject, request, closing}, "\n\n")
			if text == fallback || text == withoutQuote {
				return text, LetterSourceAI
			}
		}
	}
	return fallback, LetterSourceTemplate
}
