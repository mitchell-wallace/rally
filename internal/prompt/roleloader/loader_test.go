package roleloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExactMatch(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "SENIOR.md"), []byte("Senior instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Loader{WorkspaceDir: workspaceDir}.Load("SENIOR")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != "Senior instructions" {
		t.Fatalf("Load = %q, want %q", got, "Senior instructions")
	}
}

func TestLoadCaseInsensitiveMatch(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "senior.md"), []byte("lowercase instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Loader{WorkspaceDir: workspaceDir}.Load("Senior")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != "lowercase instructions" {
		t.Fatalf("Load = %q, want %q", got, "lowercase instructions")
	}
}

func TestLoadDeterministicFirstCaseVariant(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "Senior.md"), []byte("title-case instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "senior.md"), []byte("lowercase instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Skip("filesystem does not support distinct case-variant filenames")
	}

	got, err := Loader{WorkspaceDir: workspaceDir}.Load("SENIOR")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != "title-case instructions" {
		t.Fatalf("Load = %q, want %q", got, "title-case instructions")
	}
}

func TestLoadMissingFileIsSilent(t *testing.T) {
	workspaceDir := t.TempDir()

	got, err := Loader{WorkspaceDir: workspaceDir}.Load("MISSING")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != "" {
		t.Fatalf("Load = %q, want empty string", got)
	}
}

func TestLoadIgnoresExtensionWhenMatchingBaseName(t *testing.T) {
	workspaceDir := t.TempDir()
	agentsDir := filepath.Join(workspaceDir, ".rally", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "qa.txt"), []byte("QA instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Loader{WorkspaceDir: workspaceDir}.Load("QA")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(got, "QA instructions") {
		t.Fatalf("Load = %q, want QA instructions", got)
	}
}
