package store

import "github.com/mitchell-wallace/rally/internal/textutil"

// FinalSnippetRuneLimit is the durable per-field cap for persisted final text.
const FinalSnippetRuneLimit = 3000

// TruncateFinalSnippet applies the durable persisted final-text cap.
func TruncateFinalSnippet(text string) string {
	return textutil.TruncateHeadTailRunes(text, FinalSnippetRuneLimit)
}
