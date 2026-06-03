package laps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/mitchell-wallace/rally/internal/release"
)

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// executablePath resolves the running rally executable. It is indirected so
// tests can point the sibling lookup at a controlled directory.
var executablePath = os.Executable

// versionOf runs the laps binary at path, returning the first semver-looking
// token from `laps version` then `laps --version`. The boolean is false when
// the binary cannot be run or its version cannot be parsed.
func versionOf(path string) (string, bool) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		out, err := exec.Command(path, args...).CombinedOutput()
		if err != nil {
			continue
		}
		if v := semverRe.FindString(string(out)); v != "" {
			return v, true
		}
	}
	return "", false
}

// siblingLapsPath returns the path to the laps binary bundled next to the
// running rally executable (where `rally update` installs it). The boolean is
// false when the rally executable path cannot be resolved.
func siblingLapsPath() (string, bool) {
	exe, err := executablePath()
	if err != nil {
		return "", false
	}
	return filepath.Join(filepath.Dir(exe), release.Laps.BinaryName), true
}

// CompanionVersion returns the version of the bundled laps companion located
// next to the running rally executable — the exact binary `rally update`
// overwrites. It deliberately ignores any unrelated laps on PATH so a stale or
// missing companion is never masked by an up-to-date PATH copy. The boolean is
// false when the companion is absent or its version cannot be read.
var CompanionVersion = func() (string, bool) {
	path, ok := siblingLapsPath()
	if !ok {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return versionOf(path)
}

// InstalledVersion returns the version of the laps binary rally relies on,
// preferring the bundled companion next to the rally executable and falling
// back to a copy on PATH. The boolean is false when no laps binary can be
// found or its version cannot be determined.
var InstalledVersion = func() (string, bool) {
	if v, ok := CompanionVersion(); ok {
		return v, true
	}
	if path, err := exec.LookPath(release.Laps.BinaryName); err == nil {
		return versionOf(path)
	}
	return "", false
}

// lapsBinaryPresent reports whether any laps binary exists for rally to use,
// checking the bundled companion first then PATH. It is used to distinguish "no
// laps installed at all" from "installed but version unreadable".
func lapsBinaryPresent() bool {
	if path, ok := siblingLapsPath(); ok {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath(release.Laps.BinaryName)
	return err == nil
}

// VersionWarning returns a non-empty advisory message when the workspace uses
// laps (a .laps/laps.json exists) but the laps binary rally relies on is
// missing or older than the minimum required by Rally's companion contract. It
// inspects the bundled companion next to the rally executable first, falling
// back to a PATH copy only when no companion is present, so an unrelated PATH
// copy cannot hide a stale companion. It returns an empty string when laps is
// absent from the workspace, when the binary is compatible, or when the version
// cannot be determined (we do not nag on unknowns).
func VersionWarning(workspaceDir string) string {
	lapsJSON := filepath.Join(workspaceDir, ".laps", "laps.json")
	if _, err := os.Stat(lapsJSON); err != nil {
		return ""
	}
	v, ok := InstalledVersion()
	if !ok {
		if !lapsBinaryPresent() {
			return "warning: this workspace uses .laps/ but laps is not installed; run `rally update` to install it"
		}
		return ""
	}
	if release.CompareVersions(v, release.MinLapsVersion) < 0 {
		return fmt.Sprintf("warning: laps v%s is older than the required v%s; run `rally update` to upgrade", v, release.MinLapsVersion)
	}
	return ""
}
