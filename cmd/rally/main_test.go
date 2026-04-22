package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/rally/progress"
	"github.com/mitchell-wallace/rally/internal/rally/state"
)

func TestProgressRecordMergesSessionMeta(t *testing.T) {
	dir := t.TempDir()
	sessionID := 3
	if err := progress.WriteSessionMeta(progress.SessionMetaPath(dir, sessionID), progress.SessionMeta{
		Version: app.SchemaVersion,
		Session: progress.SessionProgress{
			SessionID: sessionID,
			BatchID:   1,
			Agent:     "codex",
			Status:    "running",
		},
	}); err != nil {
		t.Fatal(err)
	}

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("summary: done\nfiles_touched:\n  - internal/foo.go\nstatus: completed\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	os.Stdin = reader
	defer func() { os.Stdin = oldStdin }()

	t.Setenv(app.EnvDataDir, dir)
	t.Setenv(app.EnvRepoProgressPath, filepath.Join(dir, "repo.yaml"))
	t.Setenv(app.EnvSessionID, "3")

	if err := run([]string{"progress", "record"}); err != nil {
		t.Fatalf("run progress record returned error: %v", err)
	}

	meta, err := progress.ReadSessionMeta(progress.SessionMetaPath(dir, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Session.Summary != "done" || meta.Session.Status != "completed" {
		t.Fatalf("unexpected session meta: %#v", meta.Session)
	}
}

func TestPrepareBatchStartRejectsAmbiguousNonInteractiveResume(t *testing.T) {
	dir := t.TempDir()
	if err := state.NewStore(dir).Save(state.State{
		SchemaVersion: app.SchemaVersion,
		ActiveBatch: &state.BatchState{
			BatchID:             2,
			TargetIterations:    3,
			CompletedIterations: 1,
		},
		NextBatchID:   3,
		NextSessionID: 5,
		NextMessageID: 1,
		NextEventID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	err := prepareBatchStart(dir, batchStartPrompt, bytes.NewBuffer(nil), bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPrepareBatchStartNewClearsActiveBatch(t *testing.T) {
	dir := t.TempDir()
	if err := state.NewStore(dir).Save(state.State{
		SchemaVersion: app.SchemaVersion,
		ActiveBatch: &state.BatchState{
			BatchID:             2,
			TargetIterations:    3,
			CompletedIterations: 1,
		},
		StopAfterCurrent: true,
		NextBatchID:      3,
		NextSessionID:    5,
		NextMessageID:    1,
		NextEventID:      1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := prepareBatchStart(dir, batchStartNew, bytes.NewBuffer(nil), bytes.NewBuffer(nil)); err != nil {
		t.Fatalf("prepareBatchStart returned error: %v", err)
	}

	st, err := state.NewStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveBatch != nil {
		t.Fatalf("expected active batch cleared, got %#v", st.ActiveBatch)
	}
	if st.StopAfterCurrent {
		t.Fatal("expected stop-after-current cleared")
	}
}

func TestDefaultConfigStandaloneUsesWorkingDirectoryAndHomeDataDir(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := t.TempDir()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	t.Setenv("HOME", homeDir)
	t.Setenv(app.EnvWorkspaceDir, "")
	t.Setenv(app.EnvDataDir, "")
	t.Setenv(app.EnvRepoProgressPath, "")
	t.Setenv(app.EnvContainerName, "")

	cfg := defaultConfig()
	if cfg.WorkspaceDir != workspaceDir {
		t.Fatalf("workspace dir = %q, want %q", cfg.WorkspaceDir, workspaceDir)
	}

	wantDataDir := filepath.Join(homeDir, ".local", "share", app.BinaryName)
	if cfg.DataDir != wantDataDir {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, wantDataDir)
	}

	wantRepoPath := filepath.Join(workspaceDir, app.DefaultRepoProgress)
	if cfg.RepoProgressPath != wantRepoPath {
		t.Fatalf("repo progress path = %q, want %q", cfg.RepoProgressPath, wantRepoPath)
	}
}

func TestDefaultConfigUsesWorkspaceRallyConfigDataDir(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := t.TempDir()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	t.Setenv("HOME", homeDir)
	t.Setenv(app.EnvWorkspaceDir, "")
	t.Setenv(app.EnvDataDir, "")
	t.Setenv(app.EnvRepoProgressPath, "")
	t.Setenv(app.EnvContainerName, "")

	configDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte("RALLY_DATA_DIR=$HOME/custom-rally\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	want := filepath.Join(homeDir, "custom-rally")
	if cfg.DataDir != want {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, want)
	}
}

func TestDefaultConfigLoadsRunHooksOnAutoCommit(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := t.TempDir()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	t.Setenv("HOME", homeDir)
	t.Setenv(app.EnvWorkspaceDir, "")
	t.Setenv(app.EnvDataDir, "")
	t.Setenv(app.EnvRepoProgressPath, "")
	t.Setenv(app.EnvContainerName, "")

	if err := os.WriteFile(filepath.Join(workspaceDir, "rally.toml"), []byte("run_hooks_on_autocommit = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	if !cfg.RunHooksOnAutoCommit {
		t.Fatal("expected run_hooks_on_autocommit loaded from rally.toml")
	}
}

func TestDefaultConfigEnvDataDirOverridesWorkspaceRallyConfig(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := t.TempDir()
	envDataDir := filepath.Join(homeDir, "env-rally")

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	t.Setenv("HOME", homeDir)
	t.Setenv(app.EnvWorkspaceDir, "")
	t.Setenv(app.EnvDataDir, envDataDir)
	t.Setenv(app.EnvRepoProgressPath, "")
	t.Setenv(app.EnvContainerName, "")

	configDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte("RALLY_DATA_DIR=repo-rally\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := defaultConfig()
	if cfg.DataDir != envDataDir {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, envDataDir)
	}
}

func TestRunInitWritesRallyTomlStandalone(t *testing.T) {
	workspaceDir := t.TempDir()
	homeDir := t.TempDir()

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspaceDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	t.Setenv("HOME", homeDir)
	t.Setenv(app.EnvWorkspaceDir, "")
	t.Setenv(app.EnvDataDir, "")
	t.Setenv(app.EnvRepoProgressPath, "")
	t.Setenv(app.EnvContainerName, "")

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("y\n\nn\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	os.Stdin = reader
	defer func() { os.Stdin = oldStdin }()

	if err := run([]string{"init"}); err != nil {
		t.Fatalf("run init returned error: %v", err)
	}

	rallyTomlPath := filepath.Join(workspaceDir, "rally.toml")
	data, err := os.ReadFile(rallyTomlPath)
	if err != nil {
		t.Fatalf("read rally.toml: %v", err)
	}
	if !strings.Contains(string(data), "beads = 'true'") {
		t.Fatalf("rally.toml missing beads setting: %s", string(data))
	}

	rallyConfigData, err := os.ReadFile(filepath.Join(workspaceDir, ".rally", "config"))
	if err != nil {
		t.Fatalf("read .rally/config: %v", err)
	}
	if !strings.Contains(string(rallyConfigData), "RALLY_DATA_DIR=$HOME/.local/share/rally") {
		t.Fatalf(".rally/config missing data dir setting: %s", string(rallyConfigData))
	}

	if _, err := os.Stat(filepath.Join(workspaceDir, "dune.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected no dune.toml, got err=%v", err)
	}
}

func TestAppendAgentSpecsSplitsSingleFlagValue(t *testing.T) {
	got, err := appendAgentSpecs([]string{"ge:1"}, "cc:2 cx:3 op:1")
	if err != nil {
		t.Fatalf("appendAgentSpecs returned error: %v", err)
	}

	want := []string{"ge:1", "cc:2", "cx:3", "op:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent specs mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestAppendAgentSpecsRejectsEmptyFlagValue(t *testing.T) {
	if _, err := appendAgentSpecs(nil, " \t "); err == nil {
		t.Fatal("expected error for empty agent flag value")
	}
}
