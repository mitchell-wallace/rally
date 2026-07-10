package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTUIConfigServiceSnapshotIncludesBuiltinsCustomRolesAndProviders(t *testing.T) {
	path := writeAppTUIConfig(t, `schema_version = 2
[harness.op.models]
zai = "provider/zai"
deep = "provider/deep"
[routes]
junior = ["op:zai"]
planner = ["op:deep"]
[reasoning]
planner = "high"
[providers.primary]
models = ["op:zai", "op:deep"]
disabled = true
`)
	service := NewTUIConfigServiceAt(path)
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Path != path {
		t.Fatalf("path = %q", snapshot.Path)
	}
	if len(snapshot.Roles) != 10 { // default + 8 built-ins + planner
		t.Fatalf("roles = %#v", snapshot.Roles)
	}
	last := snapshot.Roles[len(snapshot.Roles)-1]
	if last.Name != "planner" || last.BuiltIn || last.Reasoning != "high" || !reflect.DeepEqual(last.Route, []string{"op:deep"}) {
		t.Fatalf("custom role = %#v", last)
	}
	if len(snapshot.Providers) != 1 || snapshot.Providers[0].Name != "primary" || !snapshot.Providers[0].Disabled || snapshot.Providers[0].MemberCount != 2 {
		t.Fatalf("providers = %#v", snapshot.Providers)
	}
	for _, shorthand := range []string{"ag", "cl", "cx", "op", "op:zai", "op:deep"} {
		if !containsString(snapshot.Shorthands, shorthand) {
			t.Errorf("shorthands %v missing %q", snapshot.Shorthands, shorthand)
		}
	}
}

func TestTUIConfigServiceApplyReloadsSnapshot(t *testing.T) {
	path := writeAppTUIConfig(t, `# keep
schema_version = 2
[harness.op.models]
zai = "provider/zai"
[routes]
junior = ["op:zai"]
[providers.primary]
models = ["op:zai"]
`)
	service := NewTUIConfigServiceAt(path)
	ctx := context.Background()
	if _, err := service.Apply(ctx, TUIConfigMutation{Kind: TUIConfigSetRoute, Role: "junior", Route: []string{"cx", "op:zai"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(ctx, TUIConfigMutation{Kind: TUIConfigSetReasoning, Role: "junior", Value: "xhigh"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Apply(ctx, TUIConfigMutation{Kind: TUIConfigSetProviderDisabled, Provider: "primary", Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	junior := findAppRole(t, snapshot, "junior")
	if !reflect.DeepEqual(junior.Route, []string{"cx", "op:zai"}) || junior.Reasoning != "xhigh" {
		t.Fatalf("junior = %#v", junior)
	}
	if !snapshot.Providers[0].Disabled {
		t.Fatal("provider did not reload as disabled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:7]) != "# keep\n" {
		t.Fatalf("comment lost: %s", data)
	}
}

func TestTUIConfigServiceRejectsUnknownMutationAndCancelledContext(t *testing.T) {
	service := NewTUIConfigServiceAt(writeAppTUIConfig(t, "schema_version = 2\n"))
	if _, err := service.Apply(context.Background(), TUIConfigMutation{Kind: "mystery"}); err == nil {
		t.Fatal("expected unknown mutation error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Snapshot(ctx); err == nil {
		t.Fatal("expected cancelled context error")
	}
}

func writeAppTUIConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findAppRole(t *testing.T, snapshot TUIConfigSnapshot, name string) TUIRoleConfig {
	t.Helper()
	for _, role := range snapshot.Roles {
		if role.Name == name {
			return role
		}
	}
	t.Fatalf("role %q not found", name)
	return TUIRoleConfig{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
