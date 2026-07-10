package cli

import (
	"reflect"
	"testing"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

func TestAppConfigToTUIDeepCopiesValues(t *testing.T) {
	in := app.TUIConfigSnapshot{
		Path:       "/tmp/config.toml",
		Roles:      []app.TUIRoleConfig{{Name: "junior", Route: []string{"op:zai"}, BuiltIn: true}},
		Providers:  []app.TUIProviderConfig{{Name: "primary", MemberCount: 2}},
		Shorthands: []string{"op:zai"},
	}
	out := appConfigToTUI(in)
	out.Roles[0].Route[0] = "changed"
	out.Shorthands[0] = "changed"
	if in.Roles[0].Route[0] == "changed" || in.Shorthands[0] == "changed" {
		t.Fatal("mapping aliases app snapshot slices")
	}
}

func TestTUIMutationToApp(t *testing.T) {
	in := tuicore.ConfigMutation{Kind: tuicore.ConfigSetRoute, Role: "planner", Route: []string{"cl:opus", "cx:g55"}}
	got := tuiMutationToApp(in)
	want := app.TUIConfigMutation{Kind: app.TUIConfigSetRoute, Role: "planner", Route: []string{"cl:opus", "cx:g55"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutation = %#v, want %#v", got, want)
	}
}
