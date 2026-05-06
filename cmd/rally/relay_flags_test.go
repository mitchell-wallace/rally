package main

import "testing"

func TestExpandRelayFlag(t *testing.T) {
	got, err := expandRelayFlag([]string{"cc:opus, cx:1", "op:z"}, "--agent")
	if err != nil {
		t.Fatalf("expandRelayFlag() error = %v", err)
	}

	want := []string{"cc:opus", "cx:1", "op:z"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExpandRelayFlag_EmptyValueRejected(t *testing.T) {
	_, err := expandRelayFlag([]string{"   "}, "--agent")
	if err == nil {
		t.Fatal("expected error for empty flag value")
	}
}

func TestChooseRelayAgentSpecs_AgentWinsOverMix(t *testing.T) {
	got, usedOverride, warning, err := chooseRelayAgentSpecs([]string{"cc:opus"}, []string{"cx:1"}, "")
	if err != nil {
		t.Fatalf("chooseRelayAgentSpecs() error = %v", err)
	}
	if !usedOverride {
		t.Fatal("usedOverride = false, want true")
	}
	if warning == "" {
		t.Fatal("warning = empty, want precedence warning")
	}
	if len(got) != 1 || got[0] != "cc:opus" {
		t.Fatalf("got = %v, want [cc:opus]", got)
	}
}

func TestChooseRelayAgentSpecs_UsesMixWhenAgentMissing(t *testing.T) {
	got, usedOverride, warning, err := chooseRelayAgentSpecs(nil, []string{"cx:1 op:z"}, "")
	if err != nil {
		t.Fatalf("chooseRelayAgentSpecs() error = %v", err)
	}
	if !usedOverride {
		t.Fatal("usedOverride = false, want true")
	}
	if warning != "" {
		t.Fatalf("warning = %q, want empty", warning)
	}
	want := []string{"cx:1", "op:z"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestChooseRelayAgentSpecs_FallsBackToDefaultsMix(t *testing.T) {
	got, usedOverride, warning, err := chooseRelayAgentSpecs(nil, nil, "cc cx")
	if err != nil {
		t.Fatalf("chooseRelayAgentSpecs() error = %v", err)
	}
	if usedOverride {
		t.Fatal("usedOverride = true, want false for [defaults].mix")
	}
	if warning != "" {
		t.Fatalf("warning = %q, want empty", warning)
	}
	want := []string{"cc", "cx"}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
