package runner

import (
	"testing"
)

func TestBuildLivenessProbeDisabledByConfig(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: false}}
	exec := &funcExecutor{probeSupported: true}
	if probe := r.buildLivenessProbe(exec); probe != nil {
		t.Fatal("expected liveness probe to be disabled by config")
	}
}

func TestBuildLivenessProbeSkipsUnsupportedAdapter(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: true}}
	exec := &funcExecutor{probeSupported: false}
	if probe := r.buildLivenessProbe(exec); probe != nil {
		t.Fatal("expected unsupported adapter to skip liveness probe")
	}
}

func TestBuildLivenessProbeEnabledForSupportedAdapter(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: true}}
	exec := &funcExecutor{probeSupported: true}
	if probe := r.buildLivenessProbe(exec); probe == nil {
		t.Fatal("expected supported adapter to build liveness probe")
	}
}
