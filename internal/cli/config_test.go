package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/store"
)

func writeCLIConfig(t *testing.T, workspaceDir, content string) {
	t.Helper()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir .rally: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestReliabilityFieldTitlesIncludeTimeouts(t *testing.T) {
	// The interactive reliability form must expose all three timeout keys
	// alongside the existing reliability fields.
	want := map[string]bool{
		"run_timeout_secs":     true,
		"try_timeout_secs":     true,
		"handoff_timeout_secs": true,
		"stall_threshold_secs": true,
		"retry_budget":         true,
		"liveness_probe":       true,
	}
	seen := map[string]bool{}
	for _, title := range reliabilityFieldTitles {
		seen[title] = true
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("reliability form missing field %q; got %v", k, reliabilityFieldTitles)
		}
	}
	if len(reliabilityFieldTitles) != len(want) {
		t.Errorf("reliability form has %d fields, want %d: %v", len(reliabilityFieldTitles), len(want), reliabilityFieldTitles)
	}
}

func TestFixedConfigRoleNamesIncludeRecoveryBeforeCustomSort(t *testing.T) {
	got := fixedConfigRoleNames()
	want := []string{"default", "junior", "senior", "ui", "verify", "recovery"}
	if len(got) != len(want) {
		t.Fatalf("fixedConfigRoleNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fixedConfigRoleNames() = %v, want %v", got, want)
		}
	}
}

func TestNewReliabilityFormPrefillsAllFields(t *testing.T) {
	cfg := config.V2Config{Reliability: config.ReliabilityConfig{
		StallThresholdSecs: 90,
		RetryBudget:        7,
		LivenessProbe:      true,
		RunTimeoutSecs:     1800,
		TryTimeoutSecs:     1500,
		HandoffTimeoutSecs: 120,
	}}

	f := newReliabilityForm(cfg)

	if f.stallThreshold != "90" {
		t.Errorf("stallThreshold = %q, want %q", f.stallThreshold, "90")
	}
	if f.retryBudget != "7" {
		t.Errorf("retryBudget = %q, want %q", f.retryBudget, "7")
	}
	if !f.livenessProbe {
		t.Errorf("livenessProbe = false, want true")
	}
	if f.runTimeoutSecs != "1800" {
		t.Errorf("runTimeoutSecs = %q, want %q", f.runTimeoutSecs, "1800")
	}
	if f.tryTimeoutSecs != "1500" {
		t.Errorf("tryTimeoutSecs = %q, want %q", f.tryTimeoutSecs, "1500")
	}
	if f.handoffTimeoutSecs != "120" {
		t.Errorf("handoffTimeoutSecs = %q, want %q", f.handoffTimeoutSecs, "120")
	}
}

func TestReliabilityFormApplyRoundTrip(t *testing.T) {
	// Load a config with configured timeout values, pre-fill the form, leave
	// the values unchanged, apply back, and confirm they survive a save/load.
	workspaceDir := t.TempDir()
	writeCLIConfig(t, workspaceDir, `schema_version = 2

[reliability]
run_timeout_secs = 1800
try_timeout_secs = 1500
handoff_timeout_secs = 120
`)
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}

	f := newReliabilityForm(cfg)
	f.apply(&cfg)

	if err := config.SaveV2(workspaceDir, cfg); err != nil {
		t.Fatalf("SaveV2: %v", err)
	}
	roundTrip, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2 round-trip: %v", err)
	}

	if got, want := roundTrip.Reliability.RunTimeoutSecs, 1800; got != want {
		t.Errorf("RunTimeoutSecs round-trip = %d, want %d", got, want)
	}
	if got, want := roundTrip.Reliability.TryTimeoutSecs, 1500; got != want {
		t.Errorf("TryTimeoutSecs round-trip = %d, want %d", got, want)
	}
	if got, want := roundTrip.Reliability.HandoffTimeoutSecs, 120; got != want {
		t.Errorf("HandoffTimeoutSecs round-trip = %d, want %d", got, want)
	}
}

func TestReliabilityFormApplyEditsAndClamp(t *testing.T) {
	// Simulate editing the three timeout fields in the form, apply, save, and
	// confirm a handoff value above the try/run bounds is clamped on reload.
	workspaceDir := t.TempDir()
	writeCLIConfig(t, workspaceDir, `schema_version = 2
`)
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}

	f := newReliabilityForm(cfg)
	f.runTimeoutSecs = "600"
	f.tryTimeoutSecs = "500"
	// handoff above the try bound (500) — must be clamped on reload.
	f.handoffTimeoutSecs = "999"
	f.apply(&cfg)

	if err := config.SaveV2(workspaceDir, cfg); err != nil {
		t.Fatalf("SaveV2: %v", err)
	}
	roundTrip, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2 round-trip: %v", err)
	}

	if got, want := roundTrip.Reliability.RunTimeoutSecs, 600; got != want {
		t.Errorf("RunTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := roundTrip.Reliability.TryTimeoutSecs, 500; got != want {
		t.Errorf("TryTimeoutSecs = %d, want %d", got, want)
	}
	if got, want := roundTrip.Reliability.HandoffTimeoutSecs, 499; got != want {
		t.Errorf("HandoffTimeoutSecs = %d, want %d (clamped below try bound)", got, want)
	}
	foundClamp := false
	for _, note := range roundTrip.DeprecationNotes {
		if strings.Contains(note, "handoff_timeout_secs") && strings.Contains(note, "clamped") {
			foundClamp = true
		}
	}
	if !foundClamp {
		t.Errorf("expected a clamped note on reload, got %v", roundTrip.DeprecationNotes)
	}
}

func TestReliabilityFormApplyEmptyKeepsLoadedValue(t *testing.T) {
	// Clearing a numeric field keeps the loaded value (parseIntDefault
	// semantics) rather than resetting it to 0.
	workspaceDir := t.TempDir()
	writeCLIConfig(t, workspaceDir, `schema_version = 2

[reliability]
run_timeout_secs = 1800
`)
	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}

	f := newReliabilityForm(cfg)
	f.runTimeoutSecs = ""
	f.apply(&cfg)

	if got, want := cfg.Reliability.RunTimeoutSecs, 1800; got != want {
		t.Errorf("cleared RunTimeoutSecs = %d, want %d (kept loaded value)", got, want)
	}
}
