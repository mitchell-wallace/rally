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
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"
)

const latestReleaseURL = "https://api.github.com/repos/" + app.ReleaseOwner + "/" + app.ReleaseRepo + "/releases/latest"

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func FetchLatest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
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

func ArchiveName(goos, goarch string) (string, error) {
	if goos != "linux" && goos != "darwin" {
		return "", fmt.Errorf("unsupported OS %q", goos)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %q", goarch)
	}
	return fmt.Sprintf("rally_%s_%s.tar.gz", goos, goarch), nil
}

func FindAsset(rel Release, goos, goarch string) (Asset, error) {
	archiveName, err := ArchiveName(goos, goarch)
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

func InstallAsset(ctx context.Context, asset Asset, destination string) error {
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
	tmpFile, err := os.CreateTemp(filepath.Dir(destination), "rally-*")
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
		if filepath.Base(hdr.Name) != app.BinaryName {
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
		return errors.New("download archive did not contain rally binary")
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
		return "dev"
	case strings.HasPrefix(value, "v"):
		return value
	default:
		return "v" + value
	}
}

func CheckForUpdate(currentVersion string) (string, error) {
	if NormalizeVersion(currentVersion) == "" || NormalizeVersion(currentVersion) == "dev" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rel, err := FetchLatest(ctx)
	if err != nil {
		return "", err
	}
	if NormalizeVersion(rel.TagName) == NormalizeVersion(currentVersion) {
		return "", nil
	}
	return fmt.Sprintf("A new version of Rally is available: %s. Run 'rally update' to upgrade.", DisplayVersion(rel.TagName)), nil
}

func UpdateCurrentBinary(currentVersion, destination string) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := FetchLatest(ctx)
	if err != nil {
		return "", "", false, err
	}
	oldVersion := DisplayVersion(currentVersion)
	newVersion := DisplayVersion(rel.TagName)
	if NormalizeVersion(rel.TagName) == NormalizeVersion(currentVersion) {
		return oldVersion, newVersion, false, nil
	}
	asset, err := FindAsset(rel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", "", false, err
	}
	if err := InstallAsset(ctx, asset, destination); err != nil {
		return "", "", false, err
	}
	return oldVersion, newVersion, true, nil
}
