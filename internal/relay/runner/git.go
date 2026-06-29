package runner

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"strings"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// commitLeftoverSummary is the end-of-relay failover for summary.jsonl: if the
// tracked .rally/summary.jsonl is still dirty when the relay exits, it commits
// just that file and records a RallyDiagnostic so the leftover is visible in
// New Relic. With the per-run amend fold in place this should rarely fire; a
// firing means a run finished without folding its summary, which is the signal
// worth surfacing. Best-effort: git/telemetry failures are logged, not fatal.
func (r *Runner) commitLeftoverSummary(ctx context.Context, relay *store.RelayRecord, rc telemetry.RallyContext) {
	if relay == nil {
		return
	}
	dir := r.cfg.WorkspaceDir
	if _, inGit, err := gitx.GitRepoRoot(dir); err != nil || !inGit {
		return
	}

	const rel = ".rally/summary.jsonl"
	out, err := gitx.GitOutput(dir, "status", "--porcelain", "--", rel)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return // clean — nothing left over
	}

	if _, err := gitx.GitOutput(dir, "add", "--", rel); err != nil {
		r.logf("relay %d leftover summary stage warning: %v\n", relay.ID, err)
		return
	}
	// Confirm something is actually staged (the add can be a no-op if the path
	// is operator-ignored).
	if _, err := gitx.GitOutput(dir, "diff", "--cached", "--quiet", "--", rel); err == nil {
		return
	}

	args := append(gitx.GitUserFallbackConfig(dir), "commit", "--no-verify", "-m", "rally: commit leftover summary", "--", rel)
	if _, err := gitx.GitOutput(dir, args...); err != nil {
		r.logf("relay %d leftover summary commit warning: %v\n", relay.ID, err)
		return
	}
	r.logf("relay %d committed leftover summary.jsonl via end-of-relay failover\n", relay.ID)

	r.tel().CaptureEvent(ctx, "relay left summary.jsonl uncommitted; committed via failover", telemetry.Event{
		Level: telemetry.LevelWarning,
		Tags:  telemetry.Tags(telemetry.EventInfo{RelayID: relay.ID, Repo: rc.Repo, RepoName: rc.RepoName}),
	})
}

func (r *Runner) headHash() (string, error) {
	_, inGit, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil || !inGit {
		return "", nil
	}
	out, err := gitx.GitOutput(r.cfg.WorkspaceDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitRange returns the commit hashes created between headBefore (exclusive)
// and headAfter (inclusive), oldest first. This captures every manual commit an
// agent made in a single try, not just the final HEAD. The last element is
// always headAfter.
func (r *Runner) commitRange(headBefore, headAfter string) []string {
	out, err := gitx.GitOutput(r.cfg.WorkspaceDir, "rev-list", "--reverse", headBefore+".."+headAfter)
	if err != nil {
		return nil
	}
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

func (r *Runner) autoCommit(runIndex int, agentType string, attempt int) (string, error) {
	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	if _, err := gitx.GitOutput(repoRoot, "add", "-A"); err != nil {
		return "", err
	}

	_, err = gitx.GitOutput(repoRoot, "diff", "--cached", "--quiet")
	if err == nil {
		return "", nil
	}
	var exitErr *osexec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return "", err
	}

	commitArgs := append(gitx.GitUserFallbackConfig(repoRoot), "commit")
	if !r.cfg.RunHooksOnAutoCommit {
		commitArgs = append(commitArgs, "--no-verify")
	}
	commitArgs = append(commitArgs, "-m", fmt.Sprintf("rally: run %d attempt %d (%s)", runIndex+1, attempt, agentType))
	if _, err := gitx.GitOutput(repoRoot, commitArgs...); err != nil {
		return "", err
	}

	hashOut, err := gitx.GitOutput(repoRoot, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(hashOut)), nil
}

// filesChangedList returns the list of paths that changed during the try.
// Prefers any explicit list from the agent's TryResult; falls back to a git
// diff against the recorded head before/after hashes (or the new commit
// hash); finally falls back to `git status --porcelain` (excluding rally's
// own state under `.rally/` and `.laps/`).
func (r *Runner) filesChangedList(result *agent.TryResult, headBefore, headAfter, commitHash string) []string {
	if result != nil && len(result.FilesChanged) > 0 {
		out := make([]string, len(result.FilesChanged))
		copy(out, result.FilesChanged)
		return out
	}

	repoRoot, ok, err := gitx.GitRepoRoot(r.cfg.WorkspaceDir)
	if err == nil && ok {
		var out []byte
		if headBefore != "" && headAfter != "" && headBefore != headAfter {
			out, err = gitx.GitOutput(repoRoot, "diff", "--name-only", headBefore, headAfter)
		} else if commitHash != "" {
			out, err = gitx.GitOutput(repoRoot, "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
		}
		if err == nil && len(out) > 0 {
			return nonEmptyLines(string(out))
		}
	}

	// Last resort: list dirty files via `git status --porcelain`, excluding
	// rally's own state files so a no-op try doesn't look like real progress.
	if ok && err == nil {
		statusOut, statusErr := gitx.GitOutput(repoRoot, "status", "--porcelain")
		if statusErr == nil {
			var dirty []string
			for _, line := range strings.Split(string(statusOut), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Porcelain format: "XY path". Skip the two status chars and the space.
				path := line
				if len(line) > 3 {
					path = strings.TrimSpace(line[2:])
				}
				if gitx.IsRallyOwnedOrTransientPath(path) {
					continue
				}
				dirty = append(dirty, path)
			}
			if len(dirty) > 0 {
				return dirty
			}
		}
	}
	return nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
