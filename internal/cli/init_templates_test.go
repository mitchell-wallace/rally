package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

// TestRunInit_WritesByteIdenticalTemplates is the golden test for the init
// seed templates. It runs `rally init` in a clean workspace and asserts the
// generated repo config, user config, and .rally/README.md are byte-for-byte
// identical to repoConfigTemplate, userConfigSeed, and rallyReadmeBody. This
// guards the verbatim move of those templates into init_templates.go: any edit
// to the template content surfaces here as a golden mismatch.
func TestRunInit_WritesByteIdenticalTemplates(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	isolateUserConfig(t)

	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	cmd.SetArgs([]string{})
	if err := runInit(cmd, []string{}); err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	repoCfg, err := os.ReadFile(store.ConfigPath(tmp))
	if err != nil {
		t.Fatalf("failed to read repo config: %v", err)
	}
	if string(repoCfg) != repoConfigTemplate {
		t.Errorf("repo config is not byte-for-byte identical to repoConfigTemplate\nwant:\n%s\n got:\n%s", repoConfigTemplate, string(repoCfg))
	}

	userCfg, err := os.ReadFile(store.UserConfigPath())
	if err != nil {
		t.Fatalf("failed to read user config: %v", err)
	}
	if string(userCfg) != userConfigSeed {
		t.Errorf("user config is not byte-for-byte identical to userConfigSeed\nwant:\n%s\n got:\n%s", userConfigSeed, string(userCfg))
	}

	readme, err := os.ReadFile(filepath.Join(store.RallyDir(tmp), "README.md"))
	if err != nil {
		t.Fatalf("failed to read .rally/README.md: %v", err)
	}
	if string(readme) != rallyReadmeBody {
		t.Errorf(".rally/README.md is not byte-for-byte identical to rallyReadmeBody\nwant:\n%s\n got:\n%s", rallyReadmeBody, string(readme))
	}
}
