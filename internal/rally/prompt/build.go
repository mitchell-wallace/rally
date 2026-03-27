package prompt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Build renders the full agent prompt from the given data.
func Build(data PromptData) (string, error) {
	tmpl, err := template.New("base").Parse(baseTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	// Collapse runs of 3+ newlines to 2.
	return collapseNewlines(buf.String()), nil
}

// LoadProjectInstructions reads the project instructions file from the
// rally data directory. Returns empty string if the file does not exist.
func LoadProjectInstructions(dataDir string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, "instructions.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func collapseNewlines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s) + "\n"
}
