package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestAntigravityGlogPath_LatestByModTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	older := writeAntigravityGlog(t, home, "cli-old.log", "E0628 11:00:00.000000 1 log.go:1] old\n", time.Now().Add(-time.Hour))
	newer := writeAntigravityGlog(t, home, "cli-new.log", "E0628 12:00:00.000000 1 log.go:1] new\n", time.Now())
	writeAntigravityGlog(t, home, "not-cli.log", "E0628 13:00:00.000000 1 log.go:1] ignored\n", time.Now().Add(time.Hour))

	got, err := latestAntigravityGlogPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Fatalf("latest path = %q, want %q; older was %q", got, newer, older)
	}
}

func TestAntigravityGlogEvidence_AuthErrorFiltersUnsafeLines(t *testing.T) {
	data := strings.Join([]string{
		"I0628 11:57:20.000000 19765 server.go:1] startup info must not leak",
		"safe context before error",
		"E0628 11:57:21.000000 19765 log.go:398] Failed to poll ListExperiments: error getting token source: You are not logged into Antigravity",
		"I0628 11:57:21.100000 19765 auth.go:1] info after error must not leak",
		"stack line mentioning oauth_creds.json must not leak",
		"W0628 11:57:21.200000 19765 settings.go:1] warning about settings.json must not leak",
		"E0628 11:57:22.000000 19765 settings.go:2] skipped because settings.json is unsafe",
	}, "\n")

	ev := antigravityGlogEvidenceFromData([]byte(data))
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Source != antigravityGlogSource {
		t.Errorf("Source = %q, want %q", ev.Source, antigravityGlogSource)
	}
	if ev.Category != reliability.CategoryAuthOrProxy {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryAuthOrProxy)
	}
	if !strings.Contains(ev.RawSignal, "safe context before error") {
		t.Errorf("RawSignal = %q, want safe context line", ev.RawSignal)
	}
	for _, forbidden := range []string{"I0628", "W0628", "oauth_creds.json", "antigravity-oauth-token", "settings.json"} {
		if strings.Contains(ev.RawSignal, forbidden) {
			t.Fatalf("RawSignal leaked forbidden %q: %q", forbidden, ev.RawSignal)
		}
	}
}

func TestAntigravityGlogEvidence_UnrecognizedErrorIsUnidentifiedAndBounded(t *testing.T) {
	longDetail := strings.Repeat("unclassified detail ", 40)
	data := "E0628 11:57:21.000000 19765 log.go:398] " + longDetail + "\n"

	ev := antigravityGlogEvidenceFromData([]byte(data))
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Source != antigravityGlogSource {
		t.Errorf("Source = %q, want %q", ev.Source, antigravityGlogSource)
	}
	if ev.Category != reliability.CategoryUnidentifiedIssue {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryUnidentifiedIssue)
	}
	if utf8.RuneCountInString(ev.RawSignal) > 256 {
		t.Fatalf("RawSignal has %d runes, want <= 256: %q", utf8.RuneCountInString(ev.RawSignal), ev.RawSignal)
	}
}

func TestAntigravityGlogEvidence_ResourceExhaustedUsesUsageLimit(t *testing.T) {
	data := "E0628 11:57:23.507556 19765 log.go:398] model unreachable: RESOURCE_EXHAUSTED (code 429): Individual quota reached. Resets in 4h\n"

	ev := antigravityGlogEvidenceFromData([]byte(data))
	if ev == nil {
		t.Fatal("expected fallback evidence")
	}
	if ev.Source != antigravityGlogSource {
		t.Errorf("Source = %q, want %q", ev.Source, antigravityGlogSource)
	}
	if ev.Category != reliability.CategoryUsageLimit {
		t.Errorf("Category = %q, want %q", ev.Category, reliability.CategoryUsageLimit)
	}
	if ev.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", ev.StatusCode)
	}
	if ev.ResetAfter <= 0 {
		t.Errorf("ResetAfter = %v, want parsed duration", ev.ResetAfter)
	}
}

func TestAntigravityGlogFailureEvidence_MissingDirIsNotEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if ev := antigravityGlogFailureEvidence(); ev != nil {
		t.Fatalf("expected no evidence for missing glog directory, got %+v", ev)
	}
}

func TestAntigravityExecutor_GlogFallbackWiredOnUnknownFailure(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	writeAntigravityGlog(t, home, "cli-fallback.log", "E0628 11:57:21.000000 19765 log.go:398] Failed to poll ListExperiments: error getting token source: You are not logged into Antigravity\n", time.Now())

	binDir, _ := testMockBinDir(t, "antigravity")
	scriptPath := filepath.Join(binDir, "agy")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'unstructured failure\\n'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{PrintTimeout: time.Second}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from antigravity mock")
	}
	if tr == nil || tr.Evidence == nil {
		t.Fatalf("expected harnessapi.TryResult with antigravity_glog Evidence, got %+v", tr)
	}
	if tr.Evidence.Source != antigravityGlogSource {
		t.Errorf("Evidence.Source = %q, want %q", tr.Evidence.Source, antigravityGlogSource)
	}
	if tr.Evidence.Category != reliability.CategoryAuthOrProxy {
		t.Errorf("Evidence.Category = %q, want %q", tr.Evidence.Category, reliability.CategoryAuthOrProxy)
	}
}

func TestAntigravityExecutor_TempLogParseStillAuthoritative(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	writeAntigravityGlog(t, home, "cli-fallback.log", "E0628 11:57:21.000000 19765 log.go:398] unknown persistent glog failure\n", time.Now())

	binDir, _ := testMockBinDir(t, "antigravity")
	scriptPath := filepath.Join(binDir, "agy")
	script := `#!/bin/sh
log_file=""
for arg in "$@"; do
	case "$arg" in
		--log-file=*) log_file="${arg#--log-file=}" ;;
	esac
done
printf 'RESOURCE_EXHAUSTED\nIndividual quota reached\nResets in 1h\n' > "$log_file"
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	exec := &Executor{PrintTimeout: time.Second}
	tr, err := exec.Execute(context.Background(), harnessapi.RunOptions{Prompt: "do work"})
	if err == nil {
		t.Fatal("expected error from antigravity mock")
	}
	if tr == nil || tr.Evidence == nil {
		t.Fatalf("expected harnessapi.TryResult with temp-log Evidence, got %+v", tr)
	}
	if tr.Evidence.Category != reliability.CategoryUsageLimit {
		t.Errorf("Evidence.Category = %q, want %q", tr.Evidence.Category, reliability.CategoryUsageLimit)
	}
	if tr.Evidence.Source == antigravityGlogSource {
		t.Errorf("Evidence.Source = %q, want temp --log-file parse to remain authoritative", tr.Evidence.Source)
	}
}

func writeAntigravityGlog(t *testing.T, home, name, content string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(home, ".gemini", "antigravity-cli", "log", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}
