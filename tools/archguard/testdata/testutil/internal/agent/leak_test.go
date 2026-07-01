package agent

import "github.com/mitchell-wallace/rally/internal/testutil"

// This fixture is the testutil-confinement PASS case: a _test.go file may
// freely import internal/testutil, so this import must NOT be flagged. It lives
// next to leak.go so the integration test proves the rule is production-only.
// Under testdata/, so it is never compiled; archguard parses imports only and
// never resolves them. Not real source.
var _ = testutil.Dummy
