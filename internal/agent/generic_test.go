package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenericExecutor_PromptSubstitution(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `printf 'substituted\n'`)
	modelFlag := "--model"
	g := &GenericExecutor{
		Command:   []string{script, "$PROMPT"},
		ModelFlag: &modelFlag,
		Model:     "test-model",
	}
	res, err := g.Execute(context.Background(), RunOptions{TaskName: "hello"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}
	if !strings.Contains(res.Summary, "substituted") {
		t.Errorf("expected prompt substitution in output, got %q", res.Summary)
	}
}

func TestGenericExecutor_StdinFallback(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")
	script := writeScript(t, dir, "run.sh", fmt.Sprintf(`cat > %q; echo "stdin-ok"`, outFile))
	g := &GenericExecutor{
		Command: []string{script},
	}
	res, err := g.Execute(context.Background(), RunOptions{TaskName: "stdin-test"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Completed {
		t.Error("expected completed")
	}
	if !strings.Contains(res.Summary, "stdin-ok") {
		t.Errorf("expected stdin-ok in output, got %q", res.Summary)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(data), "stdin-test") {
		t.Errorf("expected prompt piped via stdin into file, got %q", string(data))
	}
}

func TestGenericExecutor_ModelFlagNonEmpty(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `for arg in "$@"; do printf '<%s>\n' "$arg"; done`)
	modelFlag := "--model"
	g := &GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
		Model:     "droid-v1",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Summary, "<--model>") {
		t.Errorf("expected '<--model>' in output, got %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "<droid-v1>") {
		t.Errorf("expected '<droid-v1>' in output, got %q", res.Summary)
	}
}

func TestGenericExecutor_ModelFlagEmpty(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `for arg in "$@"; do printf '<%s>\n' "$arg"; done`)
	modelFlag := ""
	g := &GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
		Model:     "droid-v1",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Summary, "<droid-v1>") {
		t.Errorf("expected positional model in output, got %q", res.Summary)
	}
	if strings.Contains(res.Summary, "<--model>") {
		t.Errorf("expected no --model flag, got %q", res.Summary)
	}
}

func TestGenericExecutor_ModelFlagUnset_WithModel(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `for arg in "$@"; do printf '<%s>\n' "$arg"; done`)
	g := &GenericExecutor{
		Command:   []string{script},
		ModelFlag: nil,
		Model:     "droid-v1",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if strings.Contains(res.Summary, "droid-v1") {
		t.Errorf("expected no model in output when model_flag unset, got %q", res.Summary)
	}
}

func TestGenericExecutor_NoModel_NoModelFlag(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `for arg in "$@"; do printf '<%s>\n' "$arg"; done`)
	modelFlag := "--model"
	g := &GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
		Model:     "",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if strings.Contains(res.Summary, "<--model>") {
		t.Errorf("expected no model appended when no model resolved, got %q", res.Summary)
	}
}

func TestGenericExecutor_TailParser_LongOutput(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `for i in $(seq 1 100); do echo "line $i"; done`)
	g := &GenericExecutor{
		Command:     []string{script},
		OutputLines: 5,
		TailStream:  "stdout",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(res.Summary), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d: %q", len(lines), res.Summary)
	}
	if !strings.Contains(lines[0], "line 96") {
		t.Errorf("expected first tail line to be 'line 96', got %q", lines[0])
	}
	if !strings.Contains(lines[4], "line 100") {
		t.Errorf("expected last tail line to be 'line 100', got %q", lines[4])
	}
}

func TestGenericExecutor_TailParser_ShortOutput(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `echo "only line"`)
	g := &GenericExecutor{
		Command:     []string{script},
		OutputLines: 40,
		TailStream:  "stdout",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Summary, "only line") {
		t.Errorf("expected 'only line' in output, got %q", res.Summary)
	}
}

func TestGenericExecutor_TailStreamStderr(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `echo "stdout-msg"; echo "stderr-msg" >&2`)
	g := &GenericExecutor{
		Command:     []string{script},
		OutputLines: 40,
		TailStream:  "stderr",
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if strings.Contains(res.Summary, "stdout-msg") {
		t.Errorf("expected no stdout in output when tail_stream=stderr, got %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "stderr-msg") {
		t.Errorf("expected 'stderr-msg' in output, got %q", res.Summary)
	}
}

func TestGenericExecutor_BuiltInStillUsesBuiltIn(t *testing.T) {
	execs := map[string]Executor{
		"claude": &ClaudeExecutor{Model: "claude-opus-4-7"},
	}
	if _, ok := execs["claude"]; !ok {
		t.Error("expected claude executor to be registered")
	}
	if _, ok := execs["droid"]; ok {
		t.Error("expected droid to not be registered as built-in")
	}
}

func TestGenericExecutor_PromptSubstitutionPartial(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `echo "got: $1"`)
	g := &GenericExecutor{
		Command: []string{script, "--prompt=$PROMPT"},
	}
	res, err := g.Execute(context.Background(), RunOptions{TaskName: "partial"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Summary, "partial") {
		t.Errorf("expected partial substitution in output, got %q", res.Summary)
	}
}

func TestGenericExecutor_InvalidOutputStrategy(t *testing.T) {
	g := &GenericExecutor{
		Command:        []string{"echo"},
		OutputStrategy: "json",
	}
	_, err := g.Execute(context.Background(), RunOptions{})
	if err == nil {
		t.Fatal("expected error for unsupported output_strategy")
	}
	if !strings.Contains(err.Error(), "unsupported output_strategy") {
		t.Errorf("expected 'unsupported output_strategy' in error, got %q", err.Error())
	}
}

func TestGenericExecutor_ValidOutputStrategyTail(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "run.sh", `echo "hello"`)
	g := &GenericExecutor{
		Command:        []string{script},
		OutputStrategy: "tail",
		OutputLines:    40,
	}
	res, err := g.Execute(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Summary, "hello") {
		t.Errorf("expected 'hello' in output, got %q", res.Summary)
	}
}

func TestTailLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		n        int
		expected int
	}{
		{"fewer than n", "a\nb\nc", 5, 3},
		{"exactly n", "a\nb\nc", 3, 3},
		{"more than n", "a\nb\nc\nd\ne", 3, 3},
		{"empty", "", 5, 1},
		{"trailing newline", "a\nb\nc\n", 5, 3},
		{"trailing whitespace", "a\nb\nc  \n", 5, 3},
		{"only whitespace", "  \n\t\n", 5, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tailLines(tt.input, tt.n)
			lines := strings.Split(result, "\n")
			if len(lines) != tt.expected {
				t.Errorf("expected %d lines, got %d: %q", tt.expected, len(lines), result)
			}
		})
	}
}
