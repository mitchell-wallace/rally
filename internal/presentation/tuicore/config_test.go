package tuicore

import "testing"

func TestCloneConfigSnapshotDeepCopiesRoutes(t *testing.T) {
	in := DemoConfigSnapshot()
	out := CloneConfigSnapshot(in)
	out.Roles[0].Route[0] = "changed"
	out.Shorthands[0] = "changed"
	if in.Roles[0].Route[0] == "changed" || in.Shorthands[0] == "changed" {
		t.Fatal("clone aliases input slices")
	}
}
