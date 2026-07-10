package tuitabs

import (
	"fmt"
	"strings"
)

func (m configModel) View(width, height int) string {
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	lines := []string{
		fitLine("Machine config · "+m.snapshot.Path, innerWidth),
		mutedStyle.Render(fitLine("Writes [routes], [reasoning], and provider switches for relays started afterwards.", innerWidth)),
	}
	lines = append(lines, m.modeLines(innerWidth, innerHeight-2)...)
	return borderStyle.Width(innerWidth).Height(innerHeight).Render(fitLines(lines, innerWidth, innerHeight))
}

func (m configModel) modeLines(width, height int) []string {
	var lines []string
	switch m.mode {
	case configRoute:
		lines = m.routeLines(width)
	case configAddRoute:
		lines = m.pickerLines("Add runner to "+m.activeRole, m.snapshot.Shorthands, width)
	case configReasoning:
		labels := make([]string, 0, len(m.reasoningChoices()))
		for _, choice := range m.reasoningChoices() {
			if choice == "" {
				labels = append(labels, "(default / clear)")
			} else {
				labels = append(labels, choice)
			}
		}
		lines = m.pickerLines("Reasoning effort for "+m.activeRole, labels, width)
	case configNewRole:
		lines = []string{"", "New custom role", m.input.View(), mutedStyle.Render("Enter creates an empty route · Esc cancels")}
	case configConfirm:
		lines = m.confirmLines(width)
	default:
		lines = m.browseLines(width)
	}
	if m.saving {
		lines = append(lines, "", "saving machine config...")
	} else if m.loading {
		lines = append(lines, "", "reloading machine config...")
	} else if m.err != nil {
		lines = append(lines, "", "error: "+m.err.Error())
	} else if m.notice != "" {
		lines = append(lines, "", mutedStyle.Render(m.notice))
	}
	return visibleConfigLines(lines, m.mode, m.cursor, height)
}

func (m configModel) browseLines(width int) []string {
	lines := []string{"", "Roles · Enter routes · e effort · n new custom role", ""}
	for i, role := range m.snapshot.Roles {
		marker := "  "
		if m.cursor == i {
			marker = "> "
		}
		kind := "custom"
		if role.BuiltIn {
			kind = "built-in"
		}
		route := "(empty)"
		if len(role.Route) > 0 {
			route = strings.Join(role.Route, " → ")
		}
		reasoning := role.Reasoning
		if reasoning == "" {
			reasoning = "default"
		}
		line := fmt.Sprintf("%s%-12s %-8s route: %s  effort: %s", marker, role.Name, kind, route, reasoning)
		lines = append(lines, fitLine(line, width))
	}
	lines = append(lines, "", "Providers · Enter/Space toggle (confirm required)", "")
	offset := len(m.snapshot.Roles)
	for i, provider := range m.snapshot.Providers {
		marker := "  "
		if m.cursor == offset+i {
			marker = "> "
		}
		state := "ENABLED"
		if provider.Disabled {
			state = "DISABLED"
		}
		line := fmt.Sprintf("%s%-18s %-8s %d runners", marker, provider.Name, state, provider.MemberCount)
		lines = append(lines, fitLine(line, width))
	}
	if len(m.snapshot.Providers) == 0 {
		lines = append(lines, mutedStyle.Render("  no providers configured"))
	}
	return lines
}

func (m configModel) routeLines(width int) []string {
	role, _ := m.role(m.activeRole)
	reasoning := role.Reasoning
	if reasoning == "" {
		reasoning = "default"
	}
	lines := []string{"", fmt.Sprintf("Role %s · reasoning %s", role.Name, reasoning), ""}
	for i, entry := range role.Route {
		marker := "  "
		if i == m.routeCursor {
			marker = "> "
		}
		lines = append(lines, fitLine(fmt.Sprintf("%s%d. %s", marker, i+1, entry), width))
	}
	if len(role.Route) == 0 {
		lines = append(lines, mutedStyle.Render("  route is empty; press a to add a configured runner"))
	}
	lines = append(lines, "", mutedStyle.Render("a/Enter add · x x remove · [ / ] reorder · e effort · Esc back"))
	return lines
}

func (m configModel) pickerLines(title string, choices []string, width int) []string {
	lines := []string{"", title, ""}
	for i, choice := range choices {
		marker := "  "
		if i == m.pickerCursor {
			marker = "> "
		}
		lines = append(lines, fitLine(marker+choice, width))
	}
	if len(choices) == 0 {
		lines = append(lines, mutedStyle.Render("  no configured choices"))
	}
	lines = append(lines, "", mutedStyle.Render("j/k select · Enter save · Esc cancel"))
	return lines
}

func (m configModel) confirmLines(width int) []string {
	if m.pending == nil {
		return []string{"", "confirmation expired"}
	}
	return []string{
		"",
		fitLine("Confirm: "+m.pending.label, width),
		"",
		fitLine(fmt.Sprintf("Press %s again to confirm · Esc cancels", displayConfirmKey(m.pending.key)), width),
		mutedStyle.Render(fitLine("Other keys are ignored while this exact target is armed.", width)),
	}
}

func (m configModel) Legend() string {
	switch m.mode {
	case configRoute:
		return "a add · x remove · [/] reorder · e effort"
	case configAddRoute, configReasoning:
		return "j/k select · Enter save · Esc cancel"
	case configNewRole:
		return "type role · Enter create · Esc cancel"
	case configConfirm:
		return "confirm exact target · Esc cancel"
	default:
		return "j/k select · Enter edit · e effort · n role · r reload"
	}
}

func visibleConfigLines(lines []string, mode configMode, cursor, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if mode != configBrowse {
		return lines[:height]
	}
	// Browse lines contain two fixed headings before role rows. Keep the scope
	// header outside this slice and scroll the long role/provider body around
	// the logical row cursor.
	start := maxInt(0, cursor-height/2)
	if start > len(lines)-height {
		start = len(lines) - height
	}
	return lines[start : start+height]
}

func displayConfirmKey(key string) string {
	if key == " " {
		return "Space"
	}
	if key == "enter" {
		return "Enter"
	}
	return key
}
