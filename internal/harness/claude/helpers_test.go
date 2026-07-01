package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// testMockBinDir creates a temp bin dir for a mock CLI and an args.txt path the
// mock script can write its argv to. Mirrors the helper that lived in
// internal/agent/agent_test.go before the claude adapter moved into its own
// package.
func testMockBinDir(t *testing.T, binName string) (binDir string, argsPath string) {
	t.Helper()
	tmp := t.TempDir()
	binDir = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath = filepath.Join(tmp, "args.txt")
	return binDir, argsPath
}
