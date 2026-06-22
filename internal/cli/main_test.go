package cli

import (
	"os"
	"testing"
)

// TestMain isolates the user-level rally config from the developer's real home
// so config-loading tests in this package never read or write ~/.config/rally.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rally-cli-xdg-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
