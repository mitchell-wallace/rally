// Package buildinfo embeds build-time metadata that ships with the binary.
package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawEmbeddedVersion string

// EmbeddedVersion returns the VERSION file contents committed at build time,
// trimmed of surrounding whitespace. Release builds replace main.Version via
// ldflags; dev builds (plain `go build`) fall back to this string with a
// "-dev" suffix so `rally version` always reports a meaningful number.
func EmbeddedVersion() string {
	return strings.TrimSpace(rawEmbeddedVersion)
}
