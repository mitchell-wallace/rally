package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetRouteFilePreservesUnrelatedTOMLAndInlineComment(t *testing.T) {
	path := writeTargetedConfig(t, `# operator heading
schema_version = 2

[defaults] # keep this table
iterations = 7

[routes]
junior = ["op:zai"] # junior fallback
senior = ["cl:sonnet"]

[harness.op.models]
zai = "provider/zai"

[harness.cl.models]
sonnet = "claude-sonnet"
`)

	if err := SetRouteFile(path, "junior", []string{"cl:sonnet", "op:zai"}); err != nil {
		t.Fatal(err)
	}
	got := readTargetedConfig(t, path)
	for _, unchanged := range []string{
		"# operator heading\n",
		"[defaults] # keep this table\niterations = 7\n",
		`senior = ["cl:sonnet"]`,
		`junior = ['cl:sonnet', 'op:zai'] # junior fallback`,
	} {
		if !strings.Contains(got, unchanged) {
			t.Fatalf("edited config missing preserved text %q:\n%s", unchanged, got)
		}
	}
	cfg, err := LoadV2File(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Routes["junior"], ",") != "cl:sonnet,op:zai" {
		t.Fatalf("route = %v", cfg.Routes["junior"])
	}
}

func TestSetRouteFileHandlesQuotedKeyAndMultilineArray(t *testing.T) {
	path := writeTargetedConfig(t, `schema_version = 2
[routes]
"custom.role" = [
  "op:zai",
  "cx:g55",
]
[harness.op.models]
zai = "provider/zai"
[harness.cx.models]
g55 = "gpt-5.5"
`)
	if err := SetRouteFile(path, "custom.role", []string{"cx:g55"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadV2File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Routes["custom.role"]; len(got) != 1 || got[0] != "cx:g55" {
		t.Fatalf("quoted route = %v", got)
	}
}

func TestSetReasoningFileSetAndClear(t *testing.T) {
	path := writeTargetedConfig(t, "# keep\nschema_version = 2\n")
	if err := SetReasoningFile(path, "Verify", "xhigh"); err != nil {
		t.Fatal(err)
	}
	if err := SetReasoningFile(path, "verify", ""); err != nil {
		t.Fatal(err)
	}
	got := readTargetedConfig(t, path)
	if !strings.HasPrefix(got, "# keep\nschema_version = 2\n") {
		t.Fatalf("unrelated prefix changed:\n%s", got)
	}
	cfg, err := LoadV2File(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Reasoning["verify"]; ok {
		t.Fatalf("reasoning key survived clear: %v", cfg.Reasoning)
	}
}

func TestSetReasoningFileInsertsBeforeTrailingTableWhitespace(t *testing.T) {
	path := writeTargetedConfig(t, "schema_version = 2\n[reasoning]\njunior = \"medium\"\n\n[providers]\n")
	if err := SetReasoningFile(path, "default", "high"); err != nil {
		t.Fatal(err)
	}
	got := readTargetedConfig(t, path)
	want := "[reasoning]\njunior = \"medium\"\ndefault = 'high'\n\n[providers]"
	if !strings.Contains(got, want) {
		t.Fatalf("new key did not preserve table separator:\n%s", got)
	}
}

func TestSetProviderDisabledFileConvertsOnlyConciseProvider(t *testing.T) {
	path := writeTargetedConfig(t, `schema_version = 2
[harness.op.models]
zai = "provider/zai"
deep = "provider/deep"

[providers] # provider notes
fast = ["op:zai"] # keep sibling
primary = ["op:deep"] # convert me

[telemetry]
enabled = false # untouched
`)
	if err := SetProviderDisabledFile(path, "primary", true); err != nil {
		t.Fatal(err)
	}
	got := readTargetedConfig(t, path)
	for _, want := range []string{
		`fast = ["op:zai"] # keep sibling`,
		"[telemetry]\nenabled = false # untouched",
		"[providers.primary]",
		`models = ['op:deep'] # convert me`,
		"disabled = true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q after conversion:\n%s", want, got)
		}
	}
	if strings.Contains(got, `primary = ["op:deep"]`) {
		t.Fatalf("concise provider survived conversion:\n%s", got)
	}
	cfg, err := LoadV2File(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Providers["primary"].Disabled || cfg.Providers["fast"].Disabled {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
}

func TestSetProviderDisabledFileUpdatesTableAndPreservesComment(t *testing.T) {
	path := writeTargetedConfig(t, `schema_version = 2
[harness.op.models]
zai = "provider/zai"
[providers.primary]
models = ["op:zai"]
disabled = true # operator switch
`)
	if err := SetProviderDisabledFile(path, "primary", false); err != nil {
		t.Fatal(err)
	}
	got := readTargetedConfig(t, path)
	if !strings.Contains(got, "disabled = false # operator switch") {
		t.Fatalf("provider comment not preserved:\n%s", got)
	}
}

func TestSetRouteFileValidationFailureLeavesOriginal(t *testing.T) {
	original := "schema_version = 2\n[routes]\njunior = [\"op\"]\nsenior = [\"cl\"]\n"
	path := writeTargetedConfig(t, original)
	if err := SetRouteFile(path, "junior", []string{"senior"}); err == nil {
		t.Fatal("expected role-reference validation error")
	}
	if got := readTargetedConfig(t, path); got != original {
		t.Fatalf("file changed on validation error:\n%s", got)
	}
}

func TestTargetedEditPreservesFileMode(t *testing.T) {
	path := writeTargetedConfig(t, "schema_version = 2\n[routes]\njunior = [\"op\"]\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetRouteFile(path, "junior", []string{"cx"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func writeTargetedConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTargetedConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
