package laps

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestInstallHooksFirstInstall(t *testing.T) {
	tmp := t.TempDir()
	lapsDir := filepath.Join(tmp, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	changed, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}

	// Verify hooks.json
	hf, err := loadHooksFile(filepath.Join(lapsDir, "hooks.json"))
	if err != nil {
		t.Fatalf("load hooks.json: %v", err)
	}
	if len(hf.Hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hf.Hooks))
	}

	// Verify all rally hooks present
	commands := map[string]string{}
	for _, h := range hf.Hooks {
		commands[h.Command] = h.When
	}
	if commands["done"] != "after" {
		t.Errorf("expected done after hook, got %v", commands["done"])
	}
	if commands["handoff"] != "before" {
		t.Errorf("expected handoff before hook, got %v", commands["handoff"])
	}
	if commands["wrapup"] != "before" {
		t.Errorf("expected wrapup before hook, got %v", commands["wrapup"])
	}

	// Verify scripts written
	for _, rh := range rallyHooks {
		path := filepath.Join(lapsDir, "hooks", "rally", rh.filename)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rh.filename, err)
		}
		if string(data) != rh.script {
			t.Errorf("script %s content mismatch", rh.filename)
		}
	}
}

func TestInstallHooksReinstallIsNoOp(t *testing.T) {
	tmp := t.TempDir()
	lapsDir := filepath.Join(tmp, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	changed, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("reinstall failed: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on reinstall")
	}
}

func TestInstallHooksPreservesUserEntries(t *testing.T) {
	tmp := t.TempDir()
	lapsDir := filepath.Join(tmp, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Pre-populate with user hook.
	userHook := HooksFile{
		Version: 1,
		Hooks: []Hook{
			{
				Title:       "user:custom",
				Description: "User hook",
				Command:     "custom",
				When:        "before",
				Run:         "echo custom",
				Passback:    false,
			},
		},
	}
	data, _ := json.Marshal(userHook)
	os.WriteFile(filepath.Join(lapsDir, "hooks.json"), data, 0o644)

	changed, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when adding rally hooks alongside user hooks")
	}

	hf, err := loadHooksFile(filepath.Join(lapsDir, "hooks.json"))
	if err != nil {
		t.Fatalf("load hooks.json: %v", err)
	}
	if len(hf.Hooks) != 4 {
		t.Fatalf("expected 4 hooks (3 rally + 1 user), got %d", len(hf.Hooks))
	}

	var foundUser bool
	for _, h := range hf.Hooks {
		if h.Title == "user:custom" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatal("user hook was not preserved")
	}

	// Reinstall should be a no-op.
	changed, err = InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("reinstall failed: %v", err)
	}
	if changed {
		t.Fatal("expected changed=false on reinstall")
	}
}

func TestInstallHooksDoesNotCreateGitignore(t *testing.T) {
	tmp := t.TempDir()
	lapsDir := filepath.Join(tmp, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(lapsDir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("InstallHooks must not create .laps/.gitignore")
	}
}

func TestInstallHooksUpdatesModifiedScript(t *testing.T) {
	tmp := t.TempDir()
	lapsDir := filepath.Join(tmp, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}

	// Corrupt a script.
	scriptPath := filepath.Join(lapsDir, "hooks", "rally", "laps-done-hook.sh")
	if err := os.WriteFile(scriptPath, []byte("# corrupted"), 0o755); err != nil {
		t.Fatalf("corrupt script: %v", err)
	}

	changed, err := InstallHooks(lapsDir)
	if err != nil {
		t.Fatalf("reinstall failed: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true when script was modified")
	}

	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if string(data) != doneHookScript {
		t.Fatal("script was not restored")
	}
}

func TestDoneHookScript(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptAbs, err := filepath.Abs("laps-done-hook.sh")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)
	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create mock rally binary that records arguments.
	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	out, err := exec.Command("/bin/sh", scriptAbs, "lap-123").CombinedOutput()
	if err != nil {
		t.Fatalf("hook script failed: %v\n%s", err, out)
	}

	// Verify rally was called with correct args.
	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--record-lap") || !strings.Contains(string(logData), "lap-123") {
		t.Errorf("expected rally progress --record-lap lap-123 in log, got %q", string(logData))
	}
	if _, err := os.Stat(store.HookAuditPath(tmp)); err != nil {
		t.Fatalf("expected audit log under .rally/state/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.RallyDir(tmp), "hook-audit.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("top-level hook-audit.jsonl should not exist, stat err=%v", err)
	}

	output := string(out)
	if !strings.Contains(output, "Commit your work and wrap up this run before exiting") {
		t.Errorf("expected commit+wrapup instruction in output, got %q", output)
	}
	if !strings.Contains(output, "<lap-description>: done") {
		t.Errorf("expected done commit message form in output, got %q", output)
	}
	if !strings.Contains(output, "replace <lap-description> with this lap's description") {
		t.Errorf("expected lap-description placeholder guidance in output, got %q", output)
	}
	if !strings.Contains(output, "Wrapup: laps wrapup") {
		t.Errorf("expected wrapup instruction in output, got %q", output)
	}
}

func TestHandoffHookScript(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptAbs, err := filepath.Abs("laps-handoff-hook.sh")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)
	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	out, err := exec.Command("/bin/sh", scriptAbs).CombinedOutput()
	if err != nil {
		t.Fatalf("hook script failed: %v\n%s", err, out)
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--set-handoff") {
		t.Errorf("expected rally progress --set-handoff in log, got %q", string(logData))
	}

	output := string(out)
	if !strings.Contains(output, "Handoff signaled") {
		t.Errorf("expected handoff message in output, got %q", output)
	}
	if !strings.Contains(output, "<lap-description>: in progress (handoff)") {
		t.Errorf("expected handoff commit message form in output, got %q", output)
	}
	if !strings.Contains(output, "replace <lap-description> with this lap's description") {
		t.Errorf("expected lap-description placeholder guidance in output, got %q", output)
	}
}

func TestWrapupHookScriptRoutesToComplete(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	// Create state file with handoff_state=0.
	stateDir := store.StateDir(tmp)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := `{"run_id":"run-1","handoff_state":0,"recorded_laps":[]}`
	os.WriteFile(store.RunStatePath(tmp), []byte(state), 0o644)

	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	scriptPath := filepath.Join(origWD, "laps-wrapup-hook.sh")
	out, err := exec.Command("/bin/sh", scriptPath, "--summary", "test summary").CombinedOutput()
	if err != nil {
		t.Fatalf("hook script failed: %v\n%s", err, out)
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--complete") {
		t.Errorf("expected rally progress --complete in log, got %q", string(logData))
	}
	if strings.Contains(string(logData), "--handoff") {
		t.Errorf("did not expect --handoff in log, got %q", string(logData))
	}

	output := string(out)
	if !strings.Contains(output, "Progress recorded") {
		t.Errorf("expected progress recorded message, got %q", output)
	}
}

func TestWrapupHookScriptRoutesToHandoff(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	// Create state file with handoff_state=1.
	stateDir := store.StateDir(tmp)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := `{"run_id":"run-1","handoff_state":1,"recorded_laps":[]}`
	os.WriteFile(store.RunStatePath(tmp), []byte(state), 0o644)

	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	scriptPath := filepath.Join(origWD, "laps-wrapup-hook.sh")
	out, err := exec.Command("/bin/sh", scriptPath, "--summary", "blocked").CombinedOutput()
	if err != nil {
		t.Fatalf("hook script failed: %v\n%s", err, out)
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--handoff") {
		t.Errorf("expected rally progress --handoff in log, got %q", string(logData))
	}
	if strings.Contains(string(logData), "--complete") {
		t.Errorf("did not expect --complete in log, got %q", string(logData))
	}

	output := string(out)
	if !strings.Contains(output, "Progress recorded") {
		t.Errorf("expected progress recorded message, got %q", output)
	}

	// Verify state was reset to 0.
	stateData, err := os.ReadFile(store.RunStatePath(tmp))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.Contains(string(stateData), `"handoff_state": 1`) {
		t.Error("handoff_state was not reset to 0")
	}
	if !strings.Contains(string(stateData), `"handoff_state": 0`) {
		t.Errorf("expected handoff_state to be 0, got %s", string(stateData))
	}
}

func loadHooksFile(path string) (*HooksFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hf HooksFile
	if err := json.Unmarshal(b, &hf); err != nil {
		return nil, err
	}
	return &hf, nil
}

// --- Edge-case hardening: special characters in hook arguments ---

// The done-hook audit trail records the lap ID via printf %s — special
// characters in the ID must not break the JSON audit line or the script.
func TestDoneHookScript_SpecialCharLapId(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	scriptAbs, err := filepath.Abs("laps-done-hook.sh")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	specialID := `lap-"quotes'$dollars`
	out, err := exec.Command("/bin/sh", scriptAbs, specialID).CombinedOutput()
	if err != nil {
		t.Fatalf("hook script with special char lap ID failed: %v\n%s", err, out)
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--record-lap") || !strings.Contains(string(logData), specialID) {
		t.Errorf("expected rally progress --record-lap with special ID in log, got %q", string(logData))
	}

	auditPath := store.HookAuditPath(tmp)
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	audit := string(auditData)
	if !strings.Contains(audit, "laps-done") {
		t.Errorf("audit log missing hook name, got %q", audit)
	}
	if !strings.Contains(audit, specialID) {
		t.Errorf("audit log missing special char lap ID, got %q", audit)
	}
}

// The wrapup hook forwards arguments via $args — special characters in the
// summary should pass through without breaking the script.
func TestWrapupHookScript_SpecialCharSummary(t *testing.T) {
	tmp := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	stateDir := store.StateDir(tmp)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := `{"run_id":"run-1","handoff_state":0,"recorded_laps":[]}`
	os.WriteFile(store.RunStatePath(tmp), []byte(state), 0o644)

	mockDir := filepath.Join(tmp, "mockbin")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mockRally := filepath.Join(mockDir, "rally")
	mockScript := "#!/bin/sh\necho \"$@\" > \"$MOCK_LOG\"\n"
	if err := os.WriteFile(mockRally, []byte(mockScript), 0o755); err != nil {
		t.Fatalf("write mock rally: %v", err)
	}

	logFile := filepath.Join(tmp, "mock.log")
	t.Setenv("PATH", mockDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_LOG", logFile)

	scriptPath := filepath.Join(origWD, "laps-wrapup-hook.sh")
	summary := `Fixed "quotes" and $variables in 'parser'`
	out, err := exec.Command("/bin/sh", scriptPath, "--summary", summary).CombinedOutput()
	if err != nil {
		t.Fatalf("wrapup hook with special char summary failed: %v\n%s", err, out)
	}

	logData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read mock log: %v", err)
	}
	if !strings.Contains(string(logData), "--complete") {
		t.Errorf("expected rally progress --complete in log, got %q", string(logData))
	}
	if !strings.Contains(string(logData), summary) {
		t.Errorf("summary with special chars not in log, got %q", string(logData))
	}
}
