package relay

import (
	"fmt"

	"github.com/mitchell-wallace/rally/internal/relay/runner"
)

// This fixture exists only to exercise the archguard import-boundary rule
// end to end: internal/relay MUST NOT import internal/relay/runner. The file is
// under testdata/, so the go tool never compiles it; archguard parses it with
// parser.ImportsOnly and never resolves the import, so the imported package
// need not exist. It is deliberately broken architecture, not real source.
var _ = fmt.Sprint
var _ runner.Run
