package agent_prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// normalizeRoleContent matches the whitespace trimming used when role bodies are
// embedded (see read) and when they are written to disk (embedded + "\n"), so an
// on-disk role file and its embedded source hash identically.
func normalizeRoleContent(content string) string {
	return strings.TrimSpace(content)
}

func roleContentHash(content string) string {
	sum := sha256.Sum256([]byte(normalizeRoleContent(content)))
	return hex.EncodeToString(sum[:])
}

// IsManagedRoleContent reports whether content is a canonical rally role body
// for the given role — i.e. something rally itself has shipped, as opposed to a
// user customization. It matches against every embedded default rally has
// released for the role (the baked managedRoleContentHashes manifest plus the
// current embedded body) and any extra canonical bodies the caller supplies
// (e.g. the init bootstrap text). Matching is over the whitespace-trimmed body,
// so trailing-newline differences do not matter.
//
// This is the safety gate for the flat -> builtin/user role migration: managed
// content is moved to builtin/ (where rally keeps it current), unrecognized
// content is preserved untouched in user/.
func IsManagedRoleContent(role, content string, extra ...string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	target := roleContentHash(content)

	for _, h := range managedRoleContentHashes[role] {
		if h == target {
			return true
		}
	}
	if embedded, ok := Role(role); ok {
		if roleContentHash(embedded) == target {
			return true
		}
	}
	for _, e := range extra {
		if strings.TrimSpace(e) == "" {
			continue
		}
		if roleContentHash(e) == target {
			return true
		}
	}
	return false
}
