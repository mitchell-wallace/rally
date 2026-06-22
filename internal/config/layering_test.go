package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestMergeTOMLDocuments_RepoOverridesUserPerKey(t *testing.T) {
	user := []byte(`schema_version = 2
[defaults]
claude_model = "user-claude"
codex_model = "user-codex"
[routes]
junior = ["op:zai"]
senior = ["cc:opus"]
[harness.cc.models]
opus = "user-opus"
sonnet = "user-sonnet"
`)
	repo := []byte(`[defaults]
claude_model = "repo-claude"
[routes]
junior = ["cc:sonnet", "op:zai"]
[harness.cc.models]
opus = "repo-opus"
`)

	merged, err := mergeTOMLDocuments(user, repo)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	cfg, err := decodeV2(merged)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cfg.ClaudeModel != "repo-claude" {
		t.Errorf("ClaudeModel = %q, want repo-claude (repo overrides)", cfg.ClaudeModel)
	}
	if cfg.CodexModel != "user-codex" {
		t.Errorf("CodexModel = %q, want user-codex (inherited)", cfg.CodexModel)
	}
	if got := cfg.Routes["junior"]; len(got) != 2 || got[0] != "cc:sonnet" {
		t.Errorf("junior route = %v, want repo override [cc:sonnet op:zai]", got)
	}
	if got := cfg.Routes["senior"]; len(got) != 1 || got[0] != "cc:opus" {
		t.Errorf("senior route = %v, want inherited [cc:opus]", got)
	}
	// Sub-tables merge per key: repo overrides opus, user's sonnet is inherited.
	cc := cfg.Harnesses["cc"]
	if cc == nil {
		t.Fatal("missing cc harness after merge")
	}
	if cc.Models["opus"] != "repo-opus" {
		t.Errorf("cc.opus = %q, want repo-opus", cc.Models["opus"])
	}
	if cc.Models["sonnet"] != "user-sonnet" {
		t.Errorf("cc.sonnet = %q, want inherited user-sonnet", cc.Models["sonnet"])
	}
}

func TestLoadV2_LayersUserBaseUnderRepoOverrides(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	userPath := store.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, userPath, `schema_version = 2
[defaults]
claude_model = "user-claude"
opencode_model = "user-op"
[routes]
junior = ["op:zai"]
`)

	repoDir := t.TempDir()
	if err := os.MkdirAll(store.RallyDir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store.ConfigPath(repoDir), `[defaults]
claude_model = "repo-claude"
`)

	cfg, err := LoadV2(repoDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if cfg.ClaudeModel != "repo-claude" {
		t.Errorf("ClaudeModel = %q, want repo-claude", cfg.ClaudeModel)
	}
	if cfg.OpenCodeModel != "user-op" {
		t.Errorf("OpenCodeModel = %q, want inherited user-op", cfg.OpenCodeModel)
	}
	if got := cfg.Routes["junior"]; len(got) != 1 || got[0] != "op:zai" {
		t.Errorf("junior route = %v, want inherited [op:zai]", got)
	}
}

func TestLoadV2_UserOnlyAndRepoOnly(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	userPath := store.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, userPath, "[defaults]\nclaude_model = \"only-user\"\n")

	// Repo dir with no .rally/config.toml: user config still applies.
	repoDir := t.TempDir()
	cfg, err := LoadV2(repoDir)
	if err != nil {
		t.Fatalf("LoadV2 user-only: %v", err)
	}
	if cfg.ClaudeModel != "only-user" {
		t.Errorf("user-only ClaudeModel = %q, want only-user", cfg.ClaudeModel)
	}

	// Remove user config; a repo-only config still loads.
	if err := os.Remove(userPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.RallyDir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, store.ConfigPath(repoDir), "[defaults]\nclaude_model = \"only-repo\"\n")
	cfg, err = LoadV2(repoDir)
	if err != nil {
		t.Fatalf("LoadV2 repo-only: %v", err)
	}
	if cfg.ClaudeModel != "only-repo" {
		t.Errorf("repo-only ClaudeModel = %q, want only-repo", cfg.ClaudeModel)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
