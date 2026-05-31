package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/buildinfo"
)

const latestReleaseURL = "https://api.github.com/repos/" + app.ReleaseOwner + "/" + app.ReleaseRepo + "/releases/latest"

// MinLapsVersion is the minimum laps release the installed hooks contract
// (the rally-keyed entries written by laps.InstallHooks) depends on. Bump this
// when a new hooks feature requires a newer laps. It is advisory: rally warns
// but never hard-fails when the installed laps is older.
const MinLapsVersion = "0.1.0"

// Tool describes a GitHub-released binary that rally knows how to install and
// upgrade. Rally ships laps as a first-class companion, so the install/upgrade
// machinery is parameterised over the tool rather than hard-coded to rally.
type Tool struct {
	Owner      string
	Repo       string
	BinaryName string
}

var (
	// Rally is this binary.
	Rally = Tool{Owner: app.ReleaseOwner, Repo: app.ReleaseRepo, BinaryName: app.BinaryName}
	// Laps is the companion work-queue binary bundled alongside rally.
	Laps = Tool{Owner: "mitchell-wallace", Repo: "laps", BinaryName: "laps"}
)

func (t Tool) latestReleaseURL() string {
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", t.Owner, t.Repo)
}

// FetchLatest fetches the latest release metadata for this tool.
func (t Tool) FetchLatest(ctx context.Context) (Release, error) {
	return FetchLatestFrom(ctx, t.latestReleaseURL())
}

// ArchiveName returns the release archive name for this tool and platform.
func (t Tool) ArchiveName(goos, goarch string) (string, error) {
	if goos != "linux" && goos != "darwin" {
		return "", fmt.Errorf("unsupported OS %q", goos)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %q", goarch)
	}
	return fmt.Sprintf("%s_%s_%s.tar.gz", t.BinaryName, goos, goarch), nil
}

// FindAsset locates the archive asset for this tool and platform in a release.
func (t Tool) FindAsset(rel Release, goos, goarch string) (Asset, error) {
	archiveName, err := t.ArchiveName(goos, goarch)
	if err != nil {
		return Asset{}, err
	}
	for _, asset := range rel.Assets {
		if asset.Name == archiveName {
			return asset, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s does not include %s", rel.TagName, archiveName)
}

// InstallAsset downloads and extracts this tool's binary from the asset archive
// into destination.
func (t Tool) InstallAsset(ctx context.Context, asset Asset, destination string) error {
	return installAsset(ctx, asset, destination, t.BinaryName)
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func FetchLatest(ctx context.Context) (Release, error) {
	return FetchLatestFrom(ctx, latestReleaseURL)
}

func FetchLatestFrom(ctx context.Context, url string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub releases API returned %s", resp.Status)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, err
	}
	if rel.TagName == "" {
		return Release{}, errors.New("latest release is missing tag_name")
	}
	return rel, nil
}

func installAsset(ctx context.Context, asset Asset, destination, binaryName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(destination), binaryName+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = tmpFile.Close()
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		if _, err := io.Copy(tmpFile, tr); err != nil {
			_ = tmpFile.Close()
			return err
		}
		found = true
		break
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("download archive did not contain %s binary", binaryName)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func NormalizeVersion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "v")
}

func DisplayVersion(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "", value == "dev":
		if embedded := buildinfo.EmbeddedVersion(); embedded != "" {
			return "v" + embedded + "-dev"
		}
		return "dev"
	case strings.HasPrefix(value, "v"):
		return value
	default:
		return "v" + value
	}
}

func CheckForUpdate(currentVersion string) (string, error) {
	return CheckForUpdateFrom(currentVersion, latestReleaseURL)
}

func CheckForUpdateFrom(currentVersion, checkURL string) (string, error) {
	if NormalizeVersion(currentVersion) == "" || NormalizeVersion(currentVersion) == "dev" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rel, err := FetchLatestFrom(ctx, checkURL)
	if err != nil {
		return "", err
	}
	if NormalizeVersion(rel.TagName) == NormalizeVersion(currentVersion) {
		return "", nil
	}
	return fmt.Sprintf("new update available: %s | to update run `rally update`", DisplayVersion(rel.TagName)), nil
}

// UpdateTool upgrades the given tool's binary at destination to the latest
// release. currentVersion is the version already installed (empty when the
// binary is absent, which forces an install). Returns the display-formatted old
// and new versions and whether an install actually happened.
var UpdateTool = func(t Tool, currentVersion, destination string) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := t.FetchLatest(ctx)
	if err != nil {
		return "", "", false, err
	}
	oldVersion := DisplayVersion(currentVersion)
	newVersion := DisplayVersion(rel.TagName)
	if NormalizeVersion(currentVersion) != "" && NormalizeVersion(rel.TagName) == NormalizeVersion(currentVersion) {
		return oldVersion, newVersion, false, nil
	}
	asset, err := t.FindAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", "", false, err
	}
	if err := t.InstallAsset(ctx, asset, destination); err != nil {
		return "", "", false, err
	}
	return oldVersion, newVersion, true, nil
}

var UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
	return UpdateTool(Rally, currentVersion, destination)
}

// CompareVersions compares two semver-ish version strings, ignoring a leading
// "v" and any pre-release/build suffix. Returns -1 if a < b, 0 if equal, 1 if
// a > b. Unparseable segments are treated as 0.
func CompareVersions(a, b string) int {
	av := versionParts(a)
	bv := versionParts(b)
	for i := 0; i < 3; i++ {
		switch {
		case av[i] < bv[i]:
			return -1
		case av[i] > bv[i]:
			return 1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	v = NormalizeVersion(v)
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var parts [3]int
	segs := strings.Split(v, ".")
	for i := 0; i < 3 && i < len(segs); i++ {
		n, _ := strconv.Atoi(strings.TrimSpace(segs[i]))
		parts[i] = n
	}
	return parts
}
