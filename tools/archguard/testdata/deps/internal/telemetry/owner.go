package telemetry

import "github.com/newrelic/go-agent/v3/newrelic"

// This fixture is the dependency-confinement PASS case: internal/telemetry owns
// New Relic, so this import is allowed and must NOT be flagged. It lives next
// to the leak.go fixture so the integration test proves only the non-owner is
// reported. Under testdata/, so it is never compiled; archguard parses imports
// only and never resolves them. Not real source.
var _ = newrelic.Application{}
