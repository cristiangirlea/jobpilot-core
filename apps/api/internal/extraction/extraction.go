// Package extraction verifies model answers against job postings.
// Posting text and model answers are untrusted data and never instructions.
package extraction

import (
	_ "embed"
	"encoding/json"
)

// PromptVersion identifies the prompt and schema together.
const PromptVersion = "prompt-v1"

//go:embed prompt-v1.txt
var prompt string

//go:embed schema-v1.json
var schema string

var levels = []string{"intern", "junior", "mid", "senior", "lead", "staff", "principal", "manager", "director", "unspecified"}

var kinds = []string{"citizenship", "work_authorization", "clearance", "language", "location", "timezone", "travel", "degree", "certification", "contract", "other"}

// Prompt returns the embedded model instructions.
func Prompt() string {
	return prompt
}

// Schema returns a copy of the embedded answer schema.
func Schema() json.RawMessage {
	return json.RawMessage(schema)
}

// Text is the exact posting text sent to the model and used to verify quotes.
func Text(title, description string) string {
	return title + "\n" + description
}
