package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildZeroConfig(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 3,
		Agent:            "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Exploration Mode") {
		t.Error("expected zero-config fallback")
	}
	if strings.Contains(result, "Beads") {
		t.Error("should not contain beads instructions")
	}
	if strings.Contains(result, "Scout Mode") {
		t.Error("should not contain scout instructions")
	}
}

func TestBuildWithBeads(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 1,
		Agent:            "claude",
		BeadsEnabled:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "bd ready") {
		t.Error("expected beads instructions")
	}
	if !strings.Contains(result, "bd update") {
		t.Error("expected bd update instruction")
	}
	if !strings.Contains(result, "--status closed") {
		t.Error("expected beads closed status instruction")
	}
	if strings.Contains(result, "Exploration Mode") {
		t.Error("should not contain zero-config fallback when beads enabled")
	}
}

func TestBuildWithScout(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 5,
		Agent:            "claude",
		ScoutMode:        true,
		ScoutFocus:       "error handling",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Scout Mode") {
		t.Error("expected scout instructions")
	}
	if !strings.Contains(result, "error handling") {
		t.Error("expected scout focus")
	}
	if !strings.Contains(result, ".rally/tasks/") {
		t.Error("expected default task output path")
	}
}

func TestBuildScoutWithBeads(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 5,
		Agent:            "claude",
		ScoutMode:        true,
		BeadsEnabled:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "bd create") {
		t.Error("expected beads creation instructions in scout mode")
	}
	if strings.Contains(result, ".rally/tasks/") {
		t.Error("should not contain file-based task output when beads enabled")
	}
}

func TestBuildWithBatchMessages(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        2,
		BatchID:          1,
		IterationIndex:   2,
		TargetIterations: 3,
		Agent:            "codex",
		BatchMessages:    []string{"fix the auth tests", "don't touch the database layer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "fix the auth tests") {
		t.Error("expected batch message in output")
	}
	if !strings.Contains(result, "don't touch the database layer") {
		t.Error("expected second batch message in output")
	}
	if strings.Contains(result, "Exploration Mode") {
		t.Error("should not contain zero-config fallback with batch messages")
	}
}

func TestBuildWithProjectInstructions(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:           1,
		BatchID:             1,
		IterationIndex:      1,
		TargetIterations:    1,
		Agent:               "claude",
		ProjectInstructions: "Always run tests before committing.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Project Instructions") {
		t.Error("expected project instructions section")
	}
	if !strings.Contains(result, "Always run tests before committing.") {
		t.Error("expected project instructions content")
	}
}

func TestBuildWithSessionDirective(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 1,
		Agent:            "claude",
		SessionDirective: "Focus on the parser module only.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Session Directive") {
		t.Error("expected session directive section")
	}
	if !strings.Contains(result, "Focus on the parser module only.") {
		t.Error("expected session directive content")
	}
}

func TestBuildAlwaysIncludesProgressRecord(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        1,
		BatchID:          1,
		IterationIndex:   1,
		TargetIterations: 1,
		Agent:            "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "rally progress record") {
		t.Error("expected rally progress record instruction")
	}
	if !strings.Contains(result, "Commit your work") {
		t.Error("expected commit instruction")
	}
	if !strings.Contains(result, "repo progress yaml") {
		t.Error("expected fallback progress instruction")
	}
}

func TestBuildAlwaysIncludesSessionIdentity(t *testing.T) {
	t.Parallel()
	result, err := Build(PromptData{
		SessionID:        42,
		BatchID:          7,
		IterationIndex:   3,
		TargetIterations: 10,
		Agent:            "gemini",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Session 42") {
		t.Error("expected session ID in output")
	}
	if !strings.Contains(result, "batch 7") {
		t.Error("expected batch ID in output")
	}
	if !strings.Contains(result, "agent: gemini") {
		t.Error("expected agent name in output")
	}
}

func TestLoadProjectInstructions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Test instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadProjectInstructions(dir)
	if got != "Test instructions" {
		t.Fatalf("expected %q, got %q", "Test instructions", got)
	}
}

func TestLoadProjectInstructionsMissing(t *testing.T) {
	t.Parallel()
	got := LoadProjectInstructions(t.TempDir())
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestCollapseNewlines(t *testing.T) {
	t.Parallel()
	input := "a\n\n\n\nb\n\n\nc"
	got := collapseNewlines(input)
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("expected collapsed newlines, got %q", got)
	}
}
