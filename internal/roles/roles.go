// Package roles defines Rally's built-in role catalog.
package roles

import "strings"

// Mode describes a role's operating mode.
type Mode string

const (
	// ModeImplement is for roles that implement code or related artifacts.
	ModeImplement Mode = "implement"
	// ModePlan is for plan-only diagnosis, decomposition, and replanning.
	ModePlan Mode = "plan"
	// ModeReview is for code review of scoped implementation work.
	ModeReview Mode = "review"
	// ModeVerify is for acceptance evidence and validation gates.
	ModeVerify Mode = "verify"
	// ModeQA is for black-box user-style testing.
	ModeQA Mode = "qa"
	// ModeRecover is for reconciling dirty or failed relay state.
	ModeRecover Mode = "recover"
)

// WritePolicy describes what kind of writes a role may perform.
type WritePolicy string

const (
	// PolicyImplementation allows normal implementation writes inside lap scope.
	PolicyImplementation WritePolicy = "implementation"
	// PolicyPlanOnly allows planning artifact writes but not implementation.
	PolicyPlanOnly WritePolicy = "plan_only"
	// PolicyReadOnlyGate is for roles that report evidence or findings by default.
	PolicyReadOnlyGate WritePolicy = "read_only_gate"
	// PolicyReconcile allows state reconciliation to restore coherence.
	PolicyReconcile WritePolicy = "reconcile"
)

// Spec describes a Rally role.
type Spec struct {
	Name               string
	Mode               Mode
	WritePolicy        WritePolicy
	Summary            string
	DefaultRoute       []string
	RequiredSkills     []string
	EscalationTargets  []string
	GeneratedByDefault bool
}

var builtinSpecs = []Spec{
	{
		Name:               "intern",
		Mode:               ModeImplement,
		WritePolicy:        PolicyImplementation,
		Summary:            "Prescribed mechanical implementation. Executes exact scoped changes; escalates on design ambiguity.",
		DefaultRoute:       []string{"opencode"},
		EscalationTargets:  []string{"junior", "senior"},
		GeneratedByDefault: true,
	},
	{
		Name:               "junior",
		Mode:               ModeImplement,
		WritePolicy:        PolicyImplementation,
		Summary:            "Bounded autonomous implementation. Works inside established architecture with local decision-making.",
		DefaultRoute:       []string{"opencode"},
		EscalationTargets:  []string{"senior", "architect"},
		GeneratedByDefault: true,
	},
	{
		Name:               "senior",
		Mode:               ModeImplement,
		WritePolicy:        PolicyImplementation,
		Summary:            "Design-sensitive implementation. Handles cross-cutting or architecture-aware code changes and bounded plan corrections.",
		DefaultRoute:       []string{"claude"},
		EscalationTargets:  []string{"architect", "recovery"},
		GeneratedByDefault: true,
	},
	{
		Name:               "architect",
		Mode:               ModePlan,
		WritePolicy:        PolicyPlanOnly,
		Summary:            "Plan-only replanning. Diagnoses invalid assumptions, chooses architecture, and rewrites future laps without code edits.",
		DefaultRoute:       []string{"claude"},
		EscalationTargets:  []string{"senior", "junior", "intern"},
		GeneratedByDefault: true,
	},
	{
		Name:               "review",
		Mode:               ModeReview,
		WritePolicy:        PolicyReadOnlyGate,
		Summary:            "Findings-first code review. Loads and follows the auto-code-review skill for scoped diffs or completed relay work.",
		DefaultRoute:       []string{"codex"},
		RequiredSkills:     []string{"auto-code-review"},
		EscalationTargets:  []string{"senior", "junior", "architect", "verify"},
		GeneratedByDefault: true,
	},
	{
		Name:               "verify",
		Mode:               ModeVerify,
		WritePolicy:        PolicyReadOnlyGate,
		Summary:            "Acceptance evidence. Runs and inspects validation against stated criteria; reports pass/fail and follow-ups.",
		DefaultRoute:       []string{"codex"},
		EscalationTargets:  []string{"junior", "senior", "architect", "review"},
		GeneratedByDefault: true,
	},
	{
		Name:               "qa",
		Mode:               ModeQA,
		WritePolicy:        PolicyReadOnlyGate,
		Summary:            "Black-box user-style testing. Exercises observable workflows and reports defects without editing code.",
		DefaultRoute:       []string{"opencode"},
		EscalationTargets:  []string{"junior", "senior", "architect", "verify"},
		GeneratedByDefault: true,
	},
	{
		Name:         "recovery",
		Mode:         ModeRecover,
		WritePolicy:  PolicyReconcile,
		Summary:      "State reconciliation. Handles dirty, failed, timed-out, or incoherent work so the relay can safely continue.",
		DefaultRoute: []string{"claude"},
		// Recovery's continue/discard/course_correct/needs_user outcomes are
		// classifications, not roles; the role targets below are where work
		// routes after reconciliation.
		EscalationTargets:  []string{"architect", "senior", "junior"},
		GeneratedByDefault: true,
	},
}

var tombstoneSpecs = []Spec{
	{
		Name:               "ui",
		Mode:               ModeImplement,
		WritePolicy:        PolicyImplementation,
		Summary:            "Retired built-in role. UI, branding, accessibility, and design-system guidance now belongs in skills.",
		GeneratedByDefault: false,
	},
}

// Builtins returns Rally's generated built-in role specs in stable order.
func Builtins() []Spec {
	return cloneSpecs(builtinSpecs)
}

// Lookup resolves a role name to a built-in or tombstone spec.
//
// Unknown names return a usable implementation-mode fallback with ok=false.
func Lookup(name string) (Spec, bool) {
	normalized := normalize(name)
	if normalized == "" {
		return zeroSpecFor(normalized), false
	}
	if normalized == "reviewer" {
		normalized = "review"
	}
	for _, spec := range builtinSpecs {
		if spec.Name == normalized {
			return cloneSpec(spec), true
		}
	}
	for _, spec := range tombstoneSpecs {
		if spec.Name == normalized {
			return cloneSpec(spec), true
		}
	}
	return zeroSpecFor(normalized), false
}

// Suggest returns the nearest generated built-in role name for likely typos.
func Suggest(name string) string {
	normalized := normalize(name)
	if normalized == "" {
		return ""
	}
	bestName := ""
	bestDistance := 3
	for _, spec := range builtinSpecs {
		distance := levenshtein(normalized, spec.Name)
		if distance < bestDistance {
			bestDistance = distance
			bestName = spec.Name
		}
	}
	if bestDistance <= 2 {
		return bestName
	}
	return ""
}

func zeroSpecFor(name string) Spec {
	return Spec{
		Name:        name,
		Mode:        ModeImplement,
		WritePolicy: PolicyImplementation,
	}
}

func normalize(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cloneSpecs(specs []Spec) []Spec {
	out := make([]Spec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, cloneSpec(spec))
	}
	return out
}

func cloneSpec(spec Spec) Spec {
	spec.DefaultRoute = append([]string(nil), spec.DefaultRoute...)
	spec.RequiredSkills = append([]string(nil), spec.RequiredSkills...)
	spec.EscalationTargets = append([]string(nil), spec.EscalationTargets...)
	return spec
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min3(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
