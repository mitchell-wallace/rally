package tuitabs

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

type configLoader func(context.Context) (tuicore.ConfigSnapshot, error)
type configUpdater func(context.Context, tuicore.ConfigMutation) (tuicore.ConfigSnapshot, error)

type configMode int

const (
	configBrowse configMode = iota
	configRoute
	configAddRoute
	configReasoning
	configNewRole
	configConfirm
)

type configRow struct {
	role     string
	provider string
}

type configConfirmation struct {
	mutation   tuicore.ConfigMutation
	label      string
	key        string
	returnMode configMode
}

type configResultMsg struct {
	snapshot tuicore.ConfigSnapshot
	err      error
	reloaded bool
}

type configModel struct {
	snapshot tuicore.ConfigSnapshot
	load     configLoader
	update   configUpdater

	mode         configMode
	cursor       int
	routeCursor  int
	pickerCursor int
	activeRole   string
	pending      *configConfirmation
	input        textinput.Model

	loading bool
	saving  bool
	notice  string
	err     error
}

var configRoleNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func newConfigModel(load configLoader, update configUpdater) configModel {
	input := textinput.New()
	input.Placeholder = "custom-role"
	input.CharLimit = 48
	input.Prompt = "role name: "
	return configModel{load: load, update: update, input: input}
}

func (m configModel) WithSnapshot(snapshot tuicore.ConfigSnapshot) configModel {
	m.snapshot = tuicore.CloneConfigSnapshot(snapshot)
	m.clamp()
	return m
}

func (m *configModel) FetchCmd() tea.Cmd {
	if m.load == nil || m.loading {
		return nil
	}
	m.loading = true
	load := m.load
	return func() tea.Msg {
		snapshot, err := load(context.Background())
		return configResultMsg{snapshot: snapshot, err: err, reloaded: true}
	}
}

func (m *configModel) ApplyResult(msg configResultMsg) {
	m.loading = false
	m.saving = false
	if msg.err != nil {
		m.err = msg.err
		m.notice = ""
		return
	}
	m.err = nil
	m.snapshot = tuicore.CloneConfigSnapshot(msg.snapshot)
	if msg.reloaded {
		m.notice = "machine config reloaded"
	} else {
		m.notice = "saved for relays started afterwards"
	}
	m.clamp()
}

func (m *configModel) HandleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	if m.saving || m.loading {
		return nil, true
	}
	if m.mode == configConfirm {
		return m.handleConfirmation(key)
	}
	if m.mode == configNewRole {
		return m.handleNewRole(msg)
	}
	if m.mode == configAddRoute {
		return m.handleAddRoute(key)
	}
	if m.mode == configReasoning {
		return m.handleReasoning(key)
	}
	if m.mode == configRoute {
		return m.handleRoute(key)
	}
	return m.handleBrowse(key)
}

func (m *configModel) handleBrowse(key string) (tea.Cmd, bool) {
	rows := m.rows()
	switch key {
	case "up", "k":
		m.cursor = maxInt(0, m.cursor-1)
	case "down", "j":
		m.cursor = minInt(maxInt(0, len(rows)-1), m.cursor+1)
	case "enter", " ":
		if len(rows) == 0 {
			return nil, true
		}
		row := rows[m.cursor]
		if row.role != "" {
			m.openRoute(row.role)
		} else {
			provider, ok := m.provider(row.provider)
			if ok {
				m.arm(tuicore.ConfigMutation{Kind: tuicore.ConfigSetProviderDisabled, Provider: provider.Name, Disabled: !provider.Disabled}, fmt.Sprintf("toggle provider %s to %s", provider.Name, enabledLabel(provider.Disabled)), key, configBrowse)
			}
		}
	case "e":
		if len(rows) > 0 && rows[m.cursor].role != "" {
			m.openReasoning(rows[m.cursor].role)
		}
	case "n":
		m.mode = configNewRole
		m.err = nil
		m.notice = ""
		m.input.SetValue("")
		m.input.Focus()
		return textinput.Blink, true
	case "r":
		return m.FetchCmd(), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *configModel) handleRoute(key string) (tea.Cmd, bool) {
	role, ok := m.role(m.activeRole)
	if !ok {
		m.mode = configBrowse
		return nil, true
	}
	switch key {
	case "esc", "q":
		m.mode = configBrowse
	case "up", "k":
		m.routeCursor = maxInt(0, m.routeCursor-1)
	case "down", "j":
		m.routeCursor = minInt(maxInt(0, len(role.Route)-1), m.routeCursor+1)
	case "a", "enter":
		if len(m.snapshot.Shorthands) > 0 {
			m.mode = configAddRoute
			m.pickerCursor = 0
		}
	case "e":
		m.openReasoning(role.Name)
	case "[":
		if m.routeCursor > 0 && m.routeCursor < len(role.Route) {
			route := append([]string(nil), role.Route...)
			route[m.routeCursor-1], route[m.routeCursor] = route[m.routeCursor], route[m.routeCursor-1]
			m.routeCursor--
			return m.save(tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: role.Name, Route: route}), true
		}
	case "]":
		if m.routeCursor >= 0 && m.routeCursor+1 < len(role.Route) {
			route := append([]string(nil), role.Route...)
			route[m.routeCursor], route[m.routeCursor+1] = route[m.routeCursor+1], route[m.routeCursor]
			m.routeCursor++
			return m.save(tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: role.Name, Route: route}), true
		}
	case "x":
		if m.routeCursor >= 0 && m.routeCursor < len(role.Route) {
			target := role.Route[m.routeCursor]
			route := append([]string(nil), role.Route[:m.routeCursor]...)
			route = append(route, role.Route[m.routeCursor+1:]...)
			m.arm(tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: role.Name, Route: route}, fmt.Sprintf("remove %s from %s", target, role.Name), "x", configRoute)
		}
	default:
		return nil, true
	}
	return nil, true
}

func (m *configModel) handleAddRoute(key string) (tea.Cmd, bool) {
	switch key {
	case "esc", "q":
		m.mode = configRoute
	case "up", "k":
		m.pickerCursor = maxInt(0, m.pickerCursor-1)
	case "down", "j":
		m.pickerCursor = minInt(maxInt(0, len(m.snapshot.Shorthands)-1), m.pickerCursor+1)
	case "enter":
		role, ok := m.role(m.activeRole)
		if ok && len(m.snapshot.Shorthands) > 0 {
			route := append([]string(nil), role.Route...)
			route = append(route, m.snapshot.Shorthands[m.pickerCursor])
			m.routeCursor = len(route) - 1
			m.mode = configRoute
			return m.save(tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: role.Name, Route: route}), true
		}
	}
	return nil, true
}

func (m *configModel) handleReasoning(key string) (tea.Cmd, bool) {
	choices := m.reasoningChoices()
	switch key {
	case "esc", "q":
		m.mode = configRoute
	case "up", "k":
		m.pickerCursor = maxInt(0, m.pickerCursor-1)
	case "down", "j":
		m.pickerCursor = minInt(len(choices)-1, m.pickerCursor+1)
	case "enter":
		m.mode = configRoute
		return m.save(tuicore.ConfigMutation{Kind: tuicore.ConfigSetReasoning, Role: m.activeRole, Value: choices[m.pickerCursor]}), true
	}
	return nil, true
}

func (m *configModel) handleNewRole(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "esc":
		m.input.Blur()
		m.mode = configBrowse
		return nil, true
	case "enter":
		name := strings.ToLower(strings.TrimSpace(m.input.Value()))
		if !configRoleNamePattern.MatchString(name) {
			m.err = fmt.Errorf("role must match %s", configRoleNamePattern.String())
			return nil, true
		}
		if _, exists := m.role(name); exists {
			m.err = fmt.Errorf("role %q already exists", name)
			return nil, true
		}
		m.input.Blur()
		m.activeRole = name
		m.mode = configRoute
		return m.save(tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: name, Route: []string{}}), true
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd, true
}

func (m *configModel) handleConfirmation(key string) (tea.Cmd, bool) {
	if key == "esc" {
		m.mode = m.pending.returnMode
		m.pending = nil
		m.notice = "confirmation cancelled"
		return nil, true
	}
	if m.pending != nil && key == m.pending.key {
		mutation := m.pending.mutation
		returnMode := m.pending.returnMode
		m.pending = nil
		m.mode = returnMode
		return m.save(mutation), true
	}
	return nil, true // modal: consume navigation and unrelated command keys
}

func (m *configModel) openRoute(role string) {
	m.activeRole = role
	m.routeCursor = 0
	m.mode = configRoute
	m.err = nil
	m.notice = ""
}

func (m *configModel) openReasoning(role string) {
	m.activeRole = role
	m.mode = configReasoning
	m.pickerCursor = 0
	current := ""
	if value, ok := m.role(role); ok {
		current = value.Reasoning
	}
	for i, choice := range m.reasoningChoices() {
		if choice == current {
			m.pickerCursor = i
			break
		}
	}
}

func (m *configModel) arm(mutation tuicore.ConfigMutation, label, key string, returnMode configMode) {
	m.pending = &configConfirmation{mutation: mutation, label: label, key: key, returnMode: returnMode}
	m.mode = configConfirm
	m.notice = ""
	m.err = nil
}

func (m *configModel) save(mutation tuicore.ConfigMutation) tea.Cmd {
	m.saving = true
	m.notice = "saving machine config..."
	m.err = nil
	update := m.update
	snapshot := tuicore.CloneConfigSnapshot(m.snapshot)
	return func() tea.Msg {
		if update != nil {
			result, err := update(context.Background(), mutation)
			return configResultMsg{snapshot: result, err: err}
		}
		return configResultMsg{snapshot: applyLocalConfigMutation(snapshot, mutation)}
	}
}

func (m *configModel) rows() []configRow {
	rows := make([]configRow, 0, len(m.snapshot.Roles)+len(m.snapshot.Providers))
	for _, role := range m.snapshot.Roles {
		rows = append(rows, configRow{role: role.Name})
	}
	for _, provider := range m.snapshot.Providers {
		rows = append(rows, configRow{provider: provider.Name})
	}
	return rows
}

func (m *configModel) role(name string) (tuicore.RoleConfig, bool) {
	for _, role := range m.snapshot.Roles {
		if role.Name == name {
			return role, true
		}
	}
	return tuicore.RoleConfig{}, false
}

func (m *configModel) provider(name string) (tuicore.ProviderConfig, bool) {
	for _, provider := range m.snapshot.Providers {
		if provider.Name == name {
			return provider, true
		}
	}
	return tuicore.ProviderConfig{}, false
}

func (m *configModel) reasoningChoices() []string {
	choices := []string{"", "low", "medium", "high", "xhigh"}
	role, ok := m.role(m.activeRole)
	if !ok || role.Reasoning == "" {
		return choices
	}
	for _, choice := range choices {
		if choice == role.Reasoning {
			return choices
		}
	}
	return append(choices, role.Reasoning)
}

func (m *configModel) clamp() {
	rows := m.rows()
	m.cursor = minInt(m.cursor, maxInt(0, len(rows)-1))
	if role, ok := m.role(m.activeRole); ok {
		m.routeCursor = minInt(m.routeCursor, maxInt(0, len(role.Route)-1))
	}
}

func applyLocalConfigMutation(snapshot tuicore.ConfigSnapshot, mutation tuicore.ConfigMutation) tuicore.ConfigSnapshot {
	snapshot = tuicore.CloneConfigSnapshot(snapshot)
	switch mutation.Kind {
	case tuicore.ConfigSetRoute:
		found := false
		for i := range snapshot.Roles {
			if snapshot.Roles[i].Name == mutation.Role {
				snapshot.Roles[i].Route = append([]string(nil), mutation.Route...)
				found = true
			}
		}
		if !found {
			snapshot.Roles = append(snapshot.Roles, tuicore.RoleConfig{Name: mutation.Role, Route: append([]string(nil), mutation.Route...)})
			sort.SliceStable(snapshot.Roles, func(i, j int) bool {
				if snapshot.Roles[i].BuiltIn != snapshot.Roles[j].BuiltIn {
					return snapshot.Roles[i].BuiltIn
				}
				return snapshot.Roles[i].Name < snapshot.Roles[j].Name
			})
		}
	case tuicore.ConfigSetReasoning:
		for i := range snapshot.Roles {
			if snapshot.Roles[i].Name == mutation.Role {
				snapshot.Roles[i].Reasoning = mutation.Value
			}
		}
	case tuicore.ConfigSetProviderDisabled:
		for i := range snapshot.Providers {
			if snapshot.Providers[i].Name == mutation.Provider {
				snapshot.Providers[i].Disabled = mutation.Disabled
			}
		}
	}
	return snapshot
}

func enabledLabel(currentlyDisabled bool) string {
	if currentlyDisabled {
		return "enabled"
	}
	return "disabled"
}
