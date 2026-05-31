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

// InstalledVersion returns the semantic version reported by the laps binary on
// PATH. It tries `laps version` then `laps --version`, extracting the first
// semver-looking token from the output. The boolean is false when laps is not
// installed or its version cannot be determined.
func InstalledVersion() (string, bool) {
	path, err := exec.LookPath("laps")
	if err != nil {
		return "", false
	}
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

// VersionWarning returns a non-empty advisory message when the workspace uses
// laps (a .laps/laps.json exists) but the installed laps binary is missing or
// older than the minimum required by the hooks contract. It returns an empty
// string when laps is absent from the workspace, when the binary is compatible,
// or when the version cannot be determined (we do not nag on unknowns).
func VersionWarning(workspaceDir string) string {
	lapsJSON := filepath.Join(workspaceDir, ".laps", "laps.json")
	if _, err := os.Stat(lapsJSON); err != nil {
		return ""
	}
	if _, err := exec.LookPath("laps"); err != nil {
		return "warning: this workspace uses .laps/ but laps is not installed; run `rally update` to install it"
	}
	v, ok := InstalledVersion()
	if !ok {
		return ""
	}
	if release.CompareVersions(v, release.MinLapsVersion) < 0 {
		return fmt.Sprintf("warning: laps v%s is older than the required v%s; run `rally update` to upgrade", v, release.MinLapsVersion)
	}
	return ""
}
