package tuitabs

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	chassistabs "github.com/mitchell-wallace/chassis/tabs"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

func TestLapsModelComposesAsChassisTab(t *testing.T) {
	laps := newLapsModel(nil).WithSnapshot(testLapsSnapshot())
	shell, err := chassistabs.New([]chassistabs.Tab{
		{Title: "Laps", Model: laps},
	}, chassistabs.Options{Title: "rally tui"})
	if err != nil {
		t.Fatalf("tabs.New: %v", err)
	}

	next, _ := shell.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	shell = next.(chassistabs.Model)
	view := shell.View()
	for _, want := range []string{"Laps", "state held", "claim rall-3 age 1m", "alpha/ (stint 1/2)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("chassis laps view missing %q:\n%s", want, view)
		}
	}
}

func TestLapsModelHandlesRefreshAsChassisChild(t *testing.T) {
	calls := 0
	laps := newLapsModel(func(_ context.Context) (tuicore.LapsSnapshot, error) {
		calls++
		return testLapsSnapshot(), nil
	})

	cmd := laps.Init()
	if cmd == nil {
		t.Fatal("Init cmd = nil, want fetch")
	}
	next, _ := laps.Update(cmd())
	laps = next.(lapsModel)
	if calls != 1 || !strings.Contains(laps.View(), "state held") {
		t.Fatalf("initial chassis fetch calls/view = %d/%q", calls, laps.View())
	}

	next, cmd = laps.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	laps = next.(lapsModel)
	if cmd == nil {
		t.Fatal("r did not issue a refresh command")
	}
	_, _ = laps.Update(cmd())
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
}
