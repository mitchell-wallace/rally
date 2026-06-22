package config

import (
	"os"
	"testing"
)

// TestMain isolates the user-level rally config from the developer's real home
// so LoadV2 layering tests are hermetic and never merge in ~/.config/rally.
// Tests that exercise layering set their own user config inside this dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rally-config-xdg-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
