package agent

import "github.com/mitchell-wallace/rally/internal/testutil"

// This fixture is the testutil-confinement FAIL case: a non-test (production)
// file importing internal/testutil is a leak. It is under testdata/, so the go
// tool never compiles it; archguard parses it with parser.ImportsOnly and never
// resolves the import, so the path need not be importable. Deliberately broken
// architecture, not real source.
var _ = testutil.Dummy
