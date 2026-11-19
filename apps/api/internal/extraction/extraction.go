// Package extraction verifies model answers against job postings.
// Posting text and model answers are untrusted data and never instructions.
package extraction

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
)

// PromptVersion identifies the prompt and schema together.
const PromptVersion = "prompt-v1"

// NotConfigured is how the API refuses an answer while extraction is off; the batch stops on it.
const NotConfigured = "Extraction is not configured on this server."

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

// Hash identifies the exact posting text used for extraction.
func Hash(title, description string) string {
	sum := sha256.Sum256([]byte(Text(title, description)))
	return hex.EncodeToString(sum[:])
}
