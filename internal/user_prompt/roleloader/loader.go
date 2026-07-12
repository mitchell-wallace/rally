// Package roleloader preserves the original user-prompt loader API. The
// implementation is audience-neutral in internal/roleinstructions so runtime
// workflows can reuse role resolution without importing user_prompt.
package roleloader

import "github.com/mitchell-wallace/rally/internal/roleinstructions"

type Loader = roleinstructions.Loader
