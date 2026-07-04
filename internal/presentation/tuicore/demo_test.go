package tuicore

import (
	"testing"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestDemoScriptShape(t *testing.T) {
	steps := DemoScript()
	if len(steps) == 0 {
		t.Fatal("DemoScript is empty")
	}
	if _, ok := steps[0].Event.(runtimeevent.RelayStarted); !ok {
		t.Fatalf("first event = %T, want RelayStarted", steps[0].Event)
	}
	if _, ok := steps[len(steps)-1].Event.(runtimeevent.RelayCompleted); !ok {
		t.Fatalf("last event = %T, want RelayCompleted", steps[len(steps)-1].Event)
	}
}

func TestDemoScriptAppliesToTranscript(t *testing.T) {
	var tr Transcript
	for i, step := range DemoScript() {
		if step.Event == nil {
			t.Fatalf("step %d has nil event", i)
		}
		tr.Apply(step.Event)
	}
	if len(tr.Lines()) == 0 {
		t.Fatal("DemoScript produced no transcript lines")
	}
	if !tr.RelayCompleted() {
		t.Fatal("DemoScript did not mark relay completed")
	}
}

func TestDemoStatusFrames(t *testing.T) {
	frames := DemoStatusFrames()
	if len(frames) == 0 {
		t.Fatal("DemoStatusFrames is empty")
	}
	for i, frame := range frames {
		if frame == "" {
			t.Fatalf("frame %d is empty", i)
		}
	}
}
