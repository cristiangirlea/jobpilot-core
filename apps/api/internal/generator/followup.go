package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

type FollowUpMaterials struct {
	CV            string            `json:"cv"`
	CoverLetter   string            `json:"cover_letter"`
	Answers       map[string]string `json:"answers"`
	AnswerSources map[string]string `json:"answer_sources"`
	JobTitle      string            `json:"job_title"`
	Company       string            `json:"company"`
}

func FollowUp(ctx context.Context, client *llm.Client, profile models.Profile, sent FollowUpMaterials) (string, string) {
	opening := "Hello,"
	subject := "I am following up on my application."
	if title, company := strings.Join(strings.Fields(sent.JobTitle), " "), strings.Join(strings.Fields(sent.Company), " "); title != "" && company != "" {
		subject = "I am following up on my application for " + title + " at " + company + "."
	}
	request := "Could you let me know if there is an update on the application?"
	closing := "Thank you,"
	if name := strings.Join(strings.Fields(profile.Name), " "); name != "" {
		closing += "\n" + name
	}
	parts := []string{opening, subject}
	excerpt := ""
	keys := make([]string, 0, len(sent.Answers))
	for key := range sent.Answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		text := strings.TrimSpace(sent.Answers[key])
		if quotableFollowUpAnswer(key, text, sent.AnswerSources[key]) {
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
		var facts bytes.Buffer
		encoder := json.NewEncoder(&facts)
		// Keep angle brackets literal so Fence can neutralise embedded tags.
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(sent)
		result, err := client.Complete(ctx, llm.Request{
			Task:        "follow_up",
			System:      "Write a short follow-up draft, never a sent message. Use only the supplied profile and sent materials. Return the supplied paragraphs verbatim, separated by blank lines; you may omit the quotation paragraph. Do not add facts, attachments, dates, or interview claims. " + llm.FenceRule,
			Prompt:      "PROFILE\n" + letterFacts(profile) + "\nSENT MATERIALS\n" + llm.Fence(strings.TrimSuffix(facts.String(), "\n")) + "\nALLOWED PARAGRAPHS\n" + fallback,
			MaxTokens:   400,
			Temperature: 0.2,
		})
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

func quotableFollowUpAnswer(label, text, source string) bool {
	switch source {
	case "bank", "bank_similar", "ai", "template":
	default:
		return false
	}
	// A source alone is not enough: templates include attachment placeholders,
	// and the answer bank can contain contact details and salary expectations.
	for _, word := range []string{"email", "e-mail", "name", "phone", "address", "linkedin", "github", "website", "salary", "compensation", "pay", "rate", "attachment", "cover letter", "resume", "cv"} {
		if strings.Contains(strings.ToLower(label), word) {
			return false
		}
	}
	if len(text) < 30 || len(text) > 300 || len(strings.Fields(text)) < 6 || strings.ContainsAny(text, "\r\n@()") || !strings.ContainsAny(text[len(text)-1:], ".!?") {
		return false
	}
	for _, word := range []string{"attached", "salary", "compensation", "eur", "usd", "€", "$"} {
		if strings.Contains(strings.ToLower(text), word) {
			return false
		}
	}
	return true
}
