package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestNewRootCommandRegistersCommandSurface(t *testing.T) {
	root := NewRootCommand(RootOptions{Version: "dev"})

	var got []string
	for _, cmd := range root.Commands() {
		got = append(got, cmd.Name())
	}
	sort.Strings(got)
	want := []string{"config", "hooks", "init", "init-roles", "instructions", "progress", "routes", "start", "tail", "update", "version"}
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands = %v, want %v", got, want)
		}
	}

	versionFlag := root.Flags().Lookup("version")
	if versionFlag == nil {
		t.Fatal("missing --version flag")
	}
	if versionFlag.Shorthand != "v" {
		t.Fatalf("--version shorthand = %q, want v", versionFlag.Shorthand)
	}

	startCmd, _, err := root.Find([]string{"start"})
	if err != nil {
		t.Fatal(err)
	}
	if len(startCmd.Aliases) != 1 || startCmd.Aliases[0] != "relay" {
		t.Fatalf("start aliases = %v, want [relay]", startCmd.Aliases)
	}
	for _, name := range []string{"iterations", "agent", "mix", "resume", "new"} {
		if startCmd.Flags().Lookup(name) == nil {
			t.Fatalf("start missing --%s flag", name)
		}
	}

	initRolesCmd, _, err := root.Find([]string{"init-roles"})
	if err != nil {
		t.Fatal(err)
	}
	if !initRolesCmd.Hidden {
		t.Fatal("init-roles must remain hidden")
	}
}

func TestRootHelpHidesProgressWhenLapsDetected(t *testing.T) {
	if _, err := exec.LookPath("laps"); err != nil {
		t.Skip("laps binary is not on PATH")
	}
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".laps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".laps", "laps.json"), []byte(`{"tasks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	prev := resolveWorkspaceDir
	resolveWorkspaceDir = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { resolveWorkspaceDir = prev })

	root := NewRootCommand(RootOptions{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	progressCmd, _, err := root.Find([]string{"progress"})
	if err != nil {
		t.Fatal(err)
	}
	if !progressCmd.Hidden {
		t.Fatal("progress command should be hidden in help when laps is detected")
	}
}
