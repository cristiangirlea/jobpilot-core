package httpapi

import (
	"context"
	"slices"
	"testing"

	"jobpilot/core/api/internal/llm"
	"jobpilot/core/api/internal/models"
)

type letterReply string

func (letterReply) Name() string                                            { return "fake" }
func (letterReply) Model() string                                           { return "test" }
func (r letterReply) Complete(context.Context, llm.Request) (string, error) { return string(r), nil }

func TestComposedCoverLetterBackupNotice(t *testing.T) {
	const notice = "The backup AI model wrote this cover letter because the main one was unavailable. Read it closely before you approve this application."
	for _, tc := range []struct {
		primary, source, letter string
		notices                 []string
	}{
		{"Primary letter", "ai", "Primary letter", []string{"existing"}},
		{"", "ai_backup", "Backup letter", []string{"existing", notice}},
	} {
		client := llm.New(letterReply(tc.primary), nil, 0, 0).WithBackup(letterReply("Backup letter"))
		entry := models.OutboxEntry{Notices: []string{"existing"}}
		composeCoverLetter(t.Context(), client, &entry, models.Profile{}, models.JobWithMatch{})
		if entry.CoverLetter != tc.letter || entry.CoverLetterSource != tc.source || !slices.Equal(entry.Notices, tc.notices) {
			t.Errorf("got %q from %q with notices %q", entry.CoverLetter, entry.CoverLetterSource, entry.Notices)
		}
	}
}
