package telemetry

import (
	"os"
	"strings"
)

// homeDir is resolved once at init so that collapseHomePaths is fast and
// deterministic. Tests may override this value temporarily via SetHomeDir.
var homeDir string

func init() {
	homeDir, _ = os.UserHomeDir()
}

// SetHomeDir sets the home directory used by collapseHomePaths. It returns
// the previous value so callers can restore it. Intended for use in tests
// only.
func SetHomeDir(dir string) string {
	prev := homeDir
	homeDir = dir
	return prev
}

// HomeDir returns the current home directory used by the scrubber. Intended
// for use in tests only.
func HomeDir() string {
	return homeDir
}

// collapseHomePaths replaces every occurrence of the user's home directory
// path with "~" in the given string. This strips the username from any
// telemetry value that might reference local paths — cwd-style values, file
// URIs, error messages, and raw provider signals alike. Non-home absolute
// paths are left untouched.
func collapseHomePaths(s string) string {
	if homeDir == "" {
		return s
	}
	return strings.ReplaceAll(s, homeDir, "~")
}

// maxValueBytes caps the size of any single string value transmitted in an
// event payload. Telemetry only ships summaries and metadata, so this is a
// defensive ceiling that guards against an unexpectedly large value (a stray
// transcript, a giant error string) leaking through.
const maxValueBytes = 4096

// sensitiveKeys names payload fields that must NEVER be transmitted: the
// assembled task prompt (current_task.md) and full agent transcripts. We never
// place these into an event payload in the first place — only summaries, byte
// sizes, and metadata — so this denylist is defense-in-depth. Keys are matched
// case-insensitively against an exact lowercased key, so size fields like
// "task_prompt_bytes" are preserved while "task_prompt" is dropped.
var sensitiveKeys = map[string]struct{}{
	"current_task":     {},
	"current_task.md":  {},
	"host":             {},
	"hostname":         {},
	"ip":               {},
	"ip_address":       {},
	"prompt":           {},
	"server_name":      {},
	"task_prompt":      {},
	"assembled_prompt": {},
	"transcript":       {},
	"full_transcript":  {},
	"full_log":         {},
	"log":              {},
	"logs":             {},
	"output":           {},
	"username":         {},
	"user":             {},
}

func isSensitiveKey(key string) bool {
	_, ok := sensitiveKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// scrubStringMap drops sensitive keys and home-collapses/truncates string
// values in place. It is used for Rally-supplied scalar tag maps.
func scrubStringMap(m map[string]string) {
	for k, v := range m {
		if isSensitiveKey(k) {
			delete(m, k)
			continue
		}
		m[k] = truncateValue(collapseHomePaths(v))
	}
}

// scrubContextMaps drops sensitive context blocks and scrubs each remaining
// string-keyed context map in place.
func scrubContextMaps(contexts map[string]map[string]interface{}) {
	for name, data := range contexts {
		if isSensitiveKey(name) {
			delete(contexts, name)
			continue
		}
		scrubAttributeMap(data)
	}
}

// scrubAttributeMap drops sensitive keys and recursively truncates /
// home-collapses string values in place. It is backend-neutral and suitable
// for Rally-supplied custom-event attributes, error attributes, breadcrumbs,
// and span data.
func scrubAttributeMap(m map[string]interface{}) {
	for k, v := range m {
		if isSensitiveKey(k) {
			delete(m, k)
			continue
		}
		m[k] = scrubValue(v)
	}
}

func scrubValue(v interface{}) interface{} {
	switch x := v.(type) {
	case string:
		return truncateValue(collapseHomePaths(x))
	case map[string]interface{}:
		scrubAttributeMap(x)
		return x
	case map[string]string:
		scrubStringMap(x)
		return x
	case map[string]map[string]interface{}:
		scrubContextMaps(x)
		return x
	case []interface{}:
		for i, item := range x {
			x[i] = scrubValue(item)
		}
		return x
	default:
		return v
	}
}

func truncateValue(s string) string {
	if len(s) <= maxValueBytes {
		return s
	}
	return s[:maxValueBytes] + "…[truncated]"
}
