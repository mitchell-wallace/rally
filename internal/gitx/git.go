// Package gitx provides shared git helper functions used by the relay runner.
package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// rallyStatePathspecs returns the subset of {.rally, .laps} that exist in dir.
// Naming a nonexistent pathspec to `git add` errors, so callers stage only the
// paths that are present (a workspace without laps has no .laps directory).
func rallyStatePathspecs(dir string) []string {
	var paths []string
	for _, p := range []string{".rally", ".laps"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func GitRepoRoot(dir string) (string, bool, error) {
	output, err := GitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil
		}
		return "", false, err
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", false, nil
	}
	return root, true, nil
}

func GitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, GitCommandError(args, out, err)
	}
	return out, nil
}

func GitCommandError(args []string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, detail)
}

func GitUserFallbackConfig(dir string) []string {
	var cfg []string
	if value, err := GitOutput(dir, "config", "--get", "user.name"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.name=Rally")
	}
	if value, err := GitOutput(dir, "config", "--get", "user.email"); err != nil || strings.TrimSpace(string(value)) == "" {
		cfg = append(cfg, "-c", "user.email=rally@localhost")
	}
	return cfg
}

func IsRallyOwnedOrTransientPath(path string) bool {
	if strings.HasPrefix(path, ".rally/") || strings.HasPrefix(path, ".laps/") {
		return true
	}
	if path == ".claude/settings.local.json" {
		return true
	}
	return false
}

func IsGitDirty(dir string) (bool, error) {
	out, err := GitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// IsWorkspaceDirty checks if there are user-agent workspace changes, excluding
// Rally's own workspace metadata: gitignored local state under .rally/ and the
// .laps/ queue/hook machinery that Rally rewrites on its own. This mirrors the
// rally-state exclusion in filesChangedList so a run that only churns Rally's
// bookkeeping is not mistaken for real user work.
func IsWorkspaceDirty(dir string) (bool, error) {
	out, err := GitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			path := parts[1]
			if IsRallyOwnedOrTransientPath(path) {
				continue
			}
		}
		return true, nil
	}
	return false, nil
}

// WorkspaceDirtyPaths returns a map of path -> porcelain XY status code for
// every workspace entry that is dirty (excluding Rally's own .rally/ and
// .laps/ paths). The returned map is suitable for snapshotting before a try
// and diffing against the post-try state to attribute changes to a specific
// try.
func WorkspaceDirtyPaths(dir string) (map[string]string, error) {
	out, err := GitOutput(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[2:])
		if IsRallyOwnedOrTransientPath(path) {
			continue
		}
		result[path] = xy
	}
	return result, nil
}

// FoldRallyState folds Rally's own bookkeeping (state under .rally/ and the
// .laps/ queue) into the run's history without ever emitting a standalone state
// commit in the common path. The run's work commit already stages the working
// tree via `git add -A`, which folds the summary.jsonl append in; this is the
// insurance path for no-code runs where only state churn remains.
//
// It stages only Rally's own paths — never user code — then:
//   - if nothing is staged, does nothing;
//   - else if HEAD is a rally-authored commit (its message has the `rally:`
//     prefix), amends HEAD in place and appends ` [+state]` to the message,
//     preserving authorship;
//   - else creates a single `rally: update state` commit.
//
// Deciding amend-vs-new by the `rally:` message prefix (not author identity) is
// deliberate: GitUserFallbackConfig is only a fallback, so a rally commit made
// in a repo with a configured user.name carries the user's identity and would
// not match an identity check. Amends never touch a non-rally HEAD and never
// stack consecutive state commits.
func FoldRallyState(dir string) error {
	_, inGit, err := GitRepoRoot(dir)
	if err != nil || !inGit {
		return nil
	}

	// Stage only Rally's own bookkeeping. A directory pathspec silently skips
	// gitignored entries (unlike naming an ignored file), so operator-ignored
	// .rally operational paths do not error here, and user code is never staged
	// — preserving any intentionally-uncommitted work tree from an incomplete
	// run.
	paths := rallyStatePathspecs(dir)
	if len(paths) == 0 {
		return nil
	}
	if _, err := GitOutput(dir, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}

	// `git diff --cached --quiet` exits 0 when nothing is staged and 1 when
	// something is. Anything else is a real error and surfaces.
	if _, err := GitOutput(dir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return err
		}
	}

	headMsg := ""
	if out, err := GitOutput(dir, "log", "-1", "--format=%s"); err == nil {
		headMsg = strings.TrimSpace(string(out))
	}

	if strings.HasPrefix(headMsg, "rally:") {
		// Fold into the existing rally-authored HEAD. `--amend` preserves the
		// original author; --no-verify skips hooks. Mark the message once.
		newMsg := headMsg
		if !strings.HasSuffix(newMsg, " [+state]") {
			newMsg += " [+state]"
		}
		args := append(GitUserFallbackConfig(dir), "commit", "--amend", "--no-verify", "-m", newMsg)
		if _, err := GitOutput(dir, args...); err != nil {
			return err
		}
		return nil
	}

	// HEAD is not rally-authored (or the repo has no commits yet): create a
	// single state commit. A later state-only run will amend this one rather
	// than stack another.
	args := append(GitUserFallbackConfig(dir), "commit", "--no-verify", "-m", "rally: update state")
	if _, err := GitOutput(dir, args...); err != nil {
		return err
	}
	return nil
}

// FoldRallyStateIntoHead folds Rally's own bookkeeping (state under .rally/ and
// the .laps/ queue, including the summary.jsonl append) into the existing HEAD
// commit via --amend, preserving HEAD's message and author. It is used when the
// current run already produced its own commit (the agent's `<lap>: done` work
// commit or rally's autocommit), so the summary update lands inside that commit
// instead of trailing it as a separate `rally: update state` commit or a
// leftover working-tree change.
//
// Only Rally's own paths are staged — never user code — so any intentionally
// uncommitted work (an incomplete/dirty handoff) is preserved. It returns the
// resulting HEAD hash (the amended hash when a fold happened, otherwise the
// unchanged HEAD), so callers can refresh the commit hash they report.
func FoldRallyStateIntoHead(dir string) (string, error) {
	_, inGit, err := GitRepoRoot(dir)
	if err != nil || !inGit {
		return "", nil
	}

	if paths := rallyStatePathspecs(dir); len(paths) > 0 {
		if _, err := GitOutput(dir, append([]string{"add", "--"}, paths...)...); err != nil {
			return "", err
		}
	}

	staged := false
	if _, err := GitOutput(dir, "diff", "--cached", "--quiet"); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", err
		}
		staged = true
	}

	if staged {
		args := append(GitUserFallbackConfig(dir), "commit", "--amend", "--no-edit", "--no-verify")
		if _, err := GitOutput(dir, args...); err != nil {
			return "", err
		}
	}

	out, err := GitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
