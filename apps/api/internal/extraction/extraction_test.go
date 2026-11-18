package extraction

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestEmbeddedExtractionFiles(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"prompt", []byte(Prompt()), "fb4553d2ed5c2b3236fbbca156f5b29799fd0a206d027eb0120de159add50fd8"},
		{"schema", Schema(), "03404ed98910ecccddd3cfbf43f6bd89aceb77bed89364d67cdd89512e979161"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fmt.Sprintf("%x", sha256.Sum256(tc.data))
			if got != tc.want {
				t.Fatalf("SHA-256 = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSchemaMatchesVerifier(t *testing.T) {
	type node struct {
		Properties map[string]node `json:"properties"`
		Items      *node           `json:"items"`
		Enum       []string        `json:"enum"`
		Required   []string        `json:"required"`
	}
	var root node
	if err := json.Unmarshal(Schema(), &root); err != nil {
		t.Fatal(err)
	}
	constraints := root.Properties["constraints"].Items
	if constraints == nil {
		t.Fatal("schema is missing constraints items")
	}
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"levels", root.Properties["seniority"].Properties["level"].Enum, levels},
		{"kinds", constraints.Properties["kind"].Enum, kinds},
		{"required", root.Required, []string{"required", "nice_to_have", "stack", "seniority", "min_years", "constraints"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(tc.got, tc.want) {
				t.Fatalf("schema %s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestSchemaReturnsCopy(t *testing.T) {
	first := Schema()
	original := string(first)
	first[0] ^= 1
	if got := string(Schema()); got != original {
		t.Fatal("mutating Schema() changed a later result")
	}
}

func TestText(t *testing.T) {
	if got := Text("a", "b"); got != "a\nb" {
		t.Fatalf("Text() = %q, want %q", got, "a\nb")
	}
}
