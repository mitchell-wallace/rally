package progress

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/app"
)

func TestRebuildRepoProgressCompactsToHistoryWindow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for i := 1; i <= app.RepoHistoryWindow+5; i++ {
		if err := WriteSessionMeta(SessionMetaPath(dir, i), SessionMeta{
			Version: app.SchemaVersion,
			Session: SessionProgress{
				SessionID: i,
				BatchID:   1,
				Agent:     "codex",
				Status:    "completed",
				Summary:   fmt.Sprintf("session %d", i),
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	repoPath := filepath.Join(dir, "repo.yaml")
	repo, err := RebuildRepoProgress(dir, repoPath, nil)
	if err != nil {
		t.Fatalf("RebuildRepoProgress returned error: %v", err)
	}
	if len(repo.RecentSessions) != app.RepoHistoryWindow {
		t.Fatalf("unexpected retained sessions: %d", len(repo.RecentSessions))
	}
	if repo.RecentSessions[0].SessionID != 6 {
		t.Fatalf("unexpected first retained session: %d", repo.RecentSessions[0].SessionID)
	}
}
