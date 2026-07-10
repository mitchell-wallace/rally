package tuitabs

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

func TestConfigTabRendersMachineScopeRoutesReasoningAndProviders(t *testing.T) {
	m, _ := newConfigTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	m = updateModel(t, m, runeKey("5"))
	view := m.View()
	for _, want := range []string{
		"[5] Config",
		"Machine config · /home/demo/.config/rally/config.toml",
		"junior",
		"op:zai → cx:g55",
		"effort: high",
		"anthropic",
		"openai",
		"DISABLED",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("config view missing %q:\n%s", want, view)
		}
	}
}

func TestConfigRouteReorderPersistsOrderedRoute(t *testing.T) {
	m, mutations := newConfigTestModel()
	m = updateModel(t, m, runeKey("5"))
	m = updateModel(t, m, enterKey()) // default route
	if m.config.mode != configRoute {
		t.Fatalf("mode = %v", m.config.mode)
	}
	m, cmd := updateModelCmd(t, m, runeKey("]"))
	if cmd == nil {
		t.Fatal("reorder did not issue mutation")
	}
	m = updateModel(t, m, cmd())
	want := []string{"cx:g55", "op:zai"}
	if len(*mutations) != 1 || !reflect.DeepEqual((*mutations)[0].Route, want) {
		t.Fatalf("mutations = %#v", *mutations)
	}
	role, _ := m.config.role("default")
	if !reflect.DeepEqual(role.Route, want) {
		t.Fatalf("saved route = %v", role.Route)
	}
}

func TestConfigRemoveConfirmationConsumesKeysAndKeepsExactTarget(t *testing.T) {
	m, mutations := newConfigTestModel()
	m = updateModel(t, m, runeKey("5"))
	m = updateModel(t, m, enterKey())
	m = updateModel(t, m, runeKey("x"))
	if m.config.mode != configConfirm || m.config.pending == nil {
		t.Fatal("remove was not armed")
	}
	armedCursor := m.config.routeCursor
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateModel(t, m, runeKey("a"))
	m = updateModel(t, m, runeKey("2"))
	if m.active != tabConfig || m.config.routeCursor != armedCursor || m.config.mode != configConfirm {
		t.Fatalf("modal key leaked: active=%v cursor=%d mode=%v", m.active, m.config.routeCursor, m.config.mode)
	}
	m, cmd := updateModelCmd(t, m, runeKey("x"))
	if cmd == nil {
		t.Fatal("confirmed remove did not issue mutation")
	}
	m = updateModel(t, m, cmd())
	if len(*mutations) != 1 || !reflect.DeepEqual((*mutations)[0].Route, []string{"cx:g55"}) {
		t.Fatalf("remove retargeted: %#v", *mutations)
	}
}

func TestConfigProviderConfirmationCannotRetarget(t *testing.T) {
	m, mutations := newConfigTestModel()
	m = updateModel(t, m, runeKey("5"))
	m.config.cursor = len(m.config.snapshot.Roles) // anthropic
	m = updateModel(t, m, enterKey())
	armedCursor := m.config.cursor
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateModel(t, m, runeKey("n"))
	if m.config.cursor != armedCursor || m.config.mode != configConfirm {
		t.Fatalf("provider confirmation leaked keys: cursor=%d mode=%v", m.config.cursor, m.config.mode)
	}
	m, cmd := updateModelCmd(t, m, enterKey())
	if cmd == nil {
		t.Fatal("provider confirm did not issue mutation")
	}
	m = updateModel(t, m, cmd())
	if len(*mutations) != 1 || (*mutations)[0].Provider != "anthropic" || !(*mutations)[0].Disabled {
		t.Fatalf("provider mutation retargeted: %#v", *mutations)
	}
}

func TestConfigReasoningPickerAndCustomRole(t *testing.T) {
	m, mutations := newConfigTestModel()
	m = updateModel(t, m, runeKey("5"))
	m.config.cursor = 2 // junior, current high
	m = updateModel(t, m, runeKey("e"))
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m, cmd := updateModelCmd(t, m, enterKey())
	m = updateModel(t, m, cmd())
	if got := (*mutations)[0]; got.Kind != tuicore.ConfigSetReasoning || got.Role != "junior" || got.Value != "medium" {
		t.Fatalf("reasoning mutation = %#v", got)
	}

	m.config.mode = configBrowse
	m = updateModel(t, m, runeKey("n"))
	for _, r := range "observer" {
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, cmd = updateModelCmd(t, m, enterKey())
	if cmd == nil {
		t.Fatal("custom role create did not issue mutation")
	}
	m = updateModel(t, m, cmd())
	if got := (*mutations)[1]; got.Kind != tuicore.ConfigSetRoute || got.Role != "observer" || len(got.Route) != 0 {
		t.Fatalf("custom role mutation = %#v", got)
	}
	if _, ok := m.config.role("observer"); !ok {
		t.Fatal("custom role missing after save")
	}
}

func TestConfigAddRouteUsesConfiguredPicker(t *testing.T) {
	m, mutations := newConfigTestModel()
	m = updateModel(t, m, runeKey("5"))
	m = updateModel(t, m, enterKey())
	m = updateModel(t, m, runeKey("a"))
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := updateModelCmd(t, m, enterKey())
	m = updateModel(t, m, cmd())
	got := (*mutations)[0]
	if got.Route[len(got.Route)-1] != m.config.snapshot.Shorthands[1] {
		t.Fatalf("picker appended %q, want %q", got.Route[len(got.Route)-1], m.config.snapshot.Shorthands[1])
	}
}

func newConfigTestModel() (model, *[]tuicore.ConfigMutation) {
	mutations := &[]tuicore.ConfigMutation{}
	snapshot := tuicore.DemoConfigSnapshot()
	update := func(_ context.Context, mutation tuicore.ConfigMutation) (tuicore.ConfigSnapshot, error) {
		*mutations = append(*mutations, mutation)
		snapshot = applyLocalConfigMutation(snapshot, mutation)
		return tuicore.CloneConfigSnapshot(snapshot), nil
	}
	m := newTestModel()
	m.config = newConfigModel(nil, update).WithSnapshot(snapshot)
	return m, mutations
}

func updateModelCmd(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("model type = %T", next)
	}
	return got, cmd
}

func runeKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func enterKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}
