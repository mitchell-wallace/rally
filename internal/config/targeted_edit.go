package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

var bareTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SetRouteFile updates one machine-config route without re-marshalling
// unrelated TOML. The exact result is decoded and validated before it replaces
// the original file.
func SetRouteFile(path, role string, entries []string) error {
	role = strings.TrimSpace(role)
	if role == "" {
		return errors.New("config: route role cannot be empty")
	}
	return editTargetedFile(path, func(doc *tomlDocument, cfg V2Config) error {
		if cfg.Routes == nil {
			cfg.Routes = make(map[string][]string)
		}
		cfg.Routes[role] = append([]string(nil), entries...)
		if err := validateRoutes(cfg.Routes); err != nil {
			return err
		}
		doc.setTableKey([]string{"routes"}, role, append([]string(nil), entries...))
		return nil
	})
}

// SetReasoningFile updates one machine-config reasoning preference. An empty
// value removes the key so normal defaults apply.
func SetReasoningFile(path, role, value string) error {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return errors.New("config: reasoning role cannot be empty")
	}
	value = strings.TrimSpace(value)
	return editTargetedFile(path, func(doc *tomlDocument, cfg V2Config) error {
		if cfg.Reasoning == nil {
			cfg.Reasoning = make(map[string]string)
		}
		if value == "" {
			delete(cfg.Reasoning, role)
			doc.deleteTableKey([]string{"reasoning"}, role)
		} else {
			cfg.Reasoning[role] = value
			doc.setTableKey([]string{"reasoning"}, role, value)
		}
		_, err := normalizeReasoning(cfg.Reasoning)
		return err
	})
}

// SetProviderDisabledFile updates one provider switch. Concise providers under
// [providers] are converted to table form only when disabling requires the
// additional key.
func SetProviderDisabledFile(path, provider string, disabled bool) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return errors.New("config: provider name cannot be empty")
	}
	return editTargetedFile(path, func(doc *tomlDocument, cfg V2Config) error {
		pc, ok := cfg.Providers[provider]
		if !ok {
			return fmt.Errorf("config: provider %q is not configured", provider)
		}
		if doc.hasTable([]string{"providers", provider}) {
			doc.setTableKey([]string{"providers", provider}, "disabled", disabled)
			return nil
		}
		if !disabled {
			return nil // concise form is enabled by definition
		}
		found, comment := doc.removeTableKey([]string{"providers"}, provider)
		if !found {
			return fmt.Errorf("config: cannot locate provider %q in TOML document", provider)
		}
		doc.appendProviderTable(provider, pc, true, comment)
		return nil
	})
}

func editTargetedFile(path string, mutate func(*tomlDocument, V2Config) error) error {
	if path == "" {
		return errors.New("config: target path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cfg, err := LoadV2File(path)
	if err != nil {
		return err
	}
	doc := newTOMLDocument(data)
	if err := mutate(&doc, cfg); err != nil {
		return err
	}
	result := doc.bytes()
	if _, err := decodeV2(result); err != nil {
		return fmt.Errorf("validate edited config: %w", err)
	}
	if string(result) == string(data) {
		return nil
	}
	return atomicWriteConfig(path, result)
}

type tomlDocument struct {
	lines        []string
	separator    string
	finalNewline bool
}

func newTOMLDocument(data []byte) tomlDocument {
	text := string(data)
	separator := "\n"
	if strings.Contains(text, "\r\n") {
		separator = "\r\n"
	}
	finalNewline := strings.HasSuffix(text, separator)
	lines := strings.Split(text, separator)
	if finalNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	return tomlDocument{lines: lines, separator: separator, finalNewline: finalNewline}
}

func (d tomlDocument) bytes() []byte {
	text := strings.Join(d.lines, d.separator)
	if d.finalNewline || len(d.lines) > 0 {
		text += d.separator
	}
	return []byte(text)
}

func (d *tomlDocument) setTableKey(table []string, key string, value interface{}) {
	d.patchTableKey(table, key, value, false)
}

func (d *tomlDocument) deleteTableKey(table []string, key string) bool {
	found, _ := d.removeTableKey(table, key)
	return found
}

func (d *tomlDocument) patchTableKey(table []string, key string, value interface{}, deleting bool) bool {
	if deleting {
		found, _ := d.removeTableKey(table, key)
		return found
	}
	start, end, ok := d.tableRange(table)
	if !ok {
		d.appendTable(table, []string{renderAssignment(key, value)})
		return true
	}
	for i := start + 1; i < end; i++ {
		foundKey, assignmentEnd, comment, found := assignmentAt(d.lines, i, end)
		if !found {
			continue
		}
		if foundKey != key {
			i = assignmentEnd - 1
			continue
		}
		line := renderAssignment(key, value)
		if comment != "" {
			line += " " + comment
		}
		d.lines = append(d.lines[:i], append([]string{line}, d.lines[assignmentEnd:]...)...)
		return true
	}
	insertAt := end
	for insertAt > start+1 && strings.TrimSpace(d.lines[insertAt-1]) == "" {
		insertAt--
	}
	d.lines = append(d.lines[:insertAt], append([]string{renderAssignment(key, value)}, d.lines[insertAt:]...)...)
	return true
}

func (d *tomlDocument) removeTableKey(table []string, key string) (bool, string) {
	start, end, ok := d.tableRange(table)
	if !ok {
		return false, ""
	}
	for i := start + 1; i < end; i++ {
		foundKey, assignmentEnd, comment, found := assignmentAt(d.lines, i, end)
		if !found {
			continue
		}
		if foundKey != key {
			i = assignmentEnd - 1
			continue
		}
		d.lines = append(d.lines[:i], d.lines[assignmentEnd:]...)
		return true, comment
	}
	return false, ""
}

func (d tomlDocument) hasTable(path []string) bool {
	_, _, ok := d.tableRange(path)
	return ok
}

func (d tomlDocument) tableRange(path []string) (int, int, bool) {
	for i, line := range d.lines {
		header, ok := parseTableHeader(line)
		if !ok || !equalStrings(header, path) {
			continue
		}
		end := len(d.lines)
		for j := i + 1; j < len(d.lines); j++ {
			if _, ok := parseTableHeader(d.lines[j]); ok {
				end = j
				break
			}
		}
		return i, end, true
	}
	return 0, 0, false
}

func (d *tomlDocument) appendTable(path, assignments []string) {
	if len(d.lines) > 0 && strings.TrimSpace(d.lines[len(d.lines)-1]) != "" {
		d.lines = append(d.lines, "")
	}
	d.lines = append(d.lines, renderTableHeader(path))
	d.lines = append(d.lines, assignments...)
}

func (d *tomlDocument) appendProviderTable(name string, pc ProviderConfig, disabled bool, comment string) {
	models := renderAssignment("models", append([]string(nil), pc.Models...))
	if comment != "" {
		models += " " + comment
	}
	assignments := []string{models}
	if len(pc.Exclude) > 0 {
		assignments = append(assignments, renderAssignment("exclude", append([]string(nil), pc.Exclude...)))
	}
	assignments = append(assignments, renderAssignment("disabled", disabled))
	d.appendTable([]string{"providers", name}, assignments)
}

func assignmentAt(lines []string, start, limit int) (string, int, string, bool) {
	trimmed := strings.TrimSpace(lines[start])
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", start + 1, "", false
	}
	for end := start + 1; end <= limit; end++ {
		snippet := "[__rally_probe]\n" + strings.Join(lines[start:end], "\n") + "\n"
		var parsed map[string]interface{}
		if err := toml.Unmarshal([]byte(snippet), &parsed); err != nil {
			continue
		}
		table, ok := parsed["__rally_probe"].(map[string]interface{})
		if !ok || len(table) != 1 {
			return "", end, "", false
		}
		for key := range table {
			comment := ""
			if end == start+1 {
				comment = inlineComment(lines[start])
			}
			return key, end, comment, true
		}
	}
	return "", start + 1, "", false
}

func parseTableHeader(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return nil, false
	}
	close := strings.Index(trimmed, "]")
	if close < 2 {
		return nil, false
	}
	header := trimmed[:close+1]
	var parsed map[string]interface{}
	if err := toml.Unmarshal([]byte(header+"\n__rally_header_probe = true\n"), &parsed); err != nil {
		return nil, false
	}
	path, ok := findProbePath(parsed, "__rally_header_probe")
	return path, ok
}

func findProbePath(values map[string]interface{}, probe string) ([]string, bool) {
	for key, value := range values {
		if key == probe {
			return nil, true
		}
		child, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if path, found := findProbePath(child, probe); found {
			return append([]string{key}, path...), true
		}
	}
	return nil, false
}

func renderAssignment(key string, value interface{}) string {
	data, err := toml.Marshal(map[string]interface{}{key: value})
	if err != nil {
		panic(err) // values are limited to strings, bools, and []string
	}
	return strings.TrimSpace(string(data))
}

func renderTableHeader(path []string) string {
	parts := make([]string, len(path))
	for i, part := range path {
		if bareTOMLKey.MatchString(part) {
			parts[i] = part
		} else {
			parts[i] = strconv.Quote(part)
		}
	}
	return "[" + strings.Join(parts, ".") + "]"
}

func inlineComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if quote == '"' && r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '#' {
			return strings.TrimSpace(line[i:])
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func atomicWriteConfig(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rally-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
