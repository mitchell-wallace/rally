package agent

import "github.com/newrelic/go-agent/v3/newrelic"

// This fixture exists only to exercise the archguard dependency-confinement
// rule end to end: internal/agent is NOT internal/telemetry, so a New Relic
// import here is a leak. The file is under testdata/, so the go tool never
// compiles it; archguard parses it with parser.ImportsOnly and never resolves
// the import, so the imported package need not exist. It is deliberately
// broken architecture, not real source.
var _ = newrelic.Application{}
