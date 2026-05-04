package agent

import (
	"strings"
	"testing"
)

func TestBuildPrompt_LapsEnabled(t *testing.T) {
	opts := RunOptions{
		LapsEnabled: true,
	}
	p := BuildPrompt(opts)

	if !strings.Contains(p, "laps done") {
		t.Error("expected prompt to contain 'laps done'")
	}
	if !strings.Contains(p, "laps handoff") {
		t.Error("expected prompt to contain 'laps handoff'")
	}
	if strings.Contains(p, "rally progress") {
		t.Error("expected prompt NOT to contain 'rally progress'")
	}
	if strings.Contains(p, "Header Context") {
		t.Error("expected prompt NOT to contain 'Header Context'")
	}
}

func TestBuildPrompt_NoBackend(t *testing.T) {
	opts := RunOptions{
		LapsEnabled: false,
	}
	p := BuildPrompt(opts)

	if !strings.Contains(p, "rally progress --complete") {
		t.Error("expected prompt to contain 'rally progress --complete'")
	}
	if strings.Contains(p, "laps done") {
		t.Error("expected prompt NOT to contain 'laps done'")
	}
	if strings.Contains(p, "Header Context") {
		t.Error("expected prompt NOT to contain 'Header Context'")
	}
}
