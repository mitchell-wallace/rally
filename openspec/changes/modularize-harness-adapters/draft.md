## Draft: Modularize Harness Adapters

Status: drafted 2026-06-29 - initial architecture concept only.

This change is an architectural refactor for executor/harness code. It should
preserve the current executor contract and existing harness behaviour unless a
later proposal deliberately scopes behaviour changes.

## Why

Rally is expected to support more harnesses over time. Today, `internal/agent`
contains:

- the executor contract,
- shared run option and result types,
- prompt building,
- built-in harness adapters,
- generic harness support,
- process execution helpers,
- session-log and disk-log readers,
- output parsers,
- reasoning-effort helpers,
- test fixtures.

The package is already showing growth pressure:

- `internal/agent/opencode.go`: 801 lines,
- `internal/agent/claude.go`: 560 lines,
- `internal/agent/antigravity.go`: 517 lines,
- `internal/agent/agent_test.go`: 2,812 lines.

Adding more first-class harnesses in the current shape will make the package
harder to navigate and will encourage cross-harness helper leakage. Harness
modules should be deep: each adapter can have whatever internal parsing and log
recovery complexity it needs, while the rest of Rally sees a small stable
interface.

## Intent

Create a cleaner harness architecture:

- a tiny executor API package,
- one module per built-in harness,
- a registry/factory for runtime construction,
- shared process/log helpers in a deliberate support package,
- per-harness parser tests near the harness that owns them,
- no imports from harness adapters back into relay, config, telemetry, store, or
  UI packages.

This should make adding a new harness feel like adding a new adapter module, not
editing a growing central file.

## Candidate Shapes

### Option A. Keep `internal/agent` as the API package and move adapters to subpackages

Possible layout:

```text
internal/agent/
  executor.go        // Executor, RunOptions, TryResult, ResolvedAgent
  prompt.go          // shared prompt builder
  reasoning.go       // shared reasoning flag mapping if still generic
  process/           // process group and logged command helpers
  claude/            // Claude adapter
  codex/             // Codex adapter
  opencode/          // OpenCode adapter
  antigravity/       // Antigravity adapter
  generic/           // custom command adapter
  registry/          // built-in + configured executor construction
```

Adapters import `internal/agent` for the contract types. The registry imports
the adapter subpackages and returns `map[string]agent.Executor`.

Pros:

- lower conceptual churn,
- keeps existing `agent.Executor` vocabulary,
- avoids a large rename in early stages.

Cons:

- `agent` remains an overloaded name because Rally terminology distinguishes
  runner, harness, role, and agent prompt.

### Option B. Introduce a dedicated harness API package

Possible layout:

```text
internal/harnessapi/
  executor.go
  prompt.go
internal/harness/
  registry.go
  process/
  claude/
  codex/
  opencode/
  antigravity/
  generic/
```

Pros:

- clearer long-term terminology,
- makes harness modules first-class,
- avoids further overloading `agent`.

Cons:

- higher import churn,
- touches relay, routing, config, and tests that currently import `agent`.

Recommended lean: Option A first unless the proposal intentionally includes a
terminology cleanup. The architecture guardrails can prevent new bad imports
even while the API package remains named `agent`.

## Candidate Work

### A. Freeze the executor API before moving adapters

Confirm the minimal contract:

- `ResolvedAgent` or renamed `ResolvedRunner`,
- `RunOptions`,
- `TryResult`,
- `Executor`.

Do not add registry or telemetry concerns to the executor interface. Harnesses
should execute and return structured results/evidence. Relay/runtime should own
retry, routing, and telemetry emission.

### B. Move one adapter as a spike

Start with a smaller adapter or the generic adapter.

Candidate first moves:

- `internal/agent/generic` because it is self-contained and only 216 lines,
- `internal/agent/claude` because it is important and has session-log tests,
- avoid starting with `opencode` because it is the largest and has disk-log
  evidence complexity.

The spike should prove:

- adapter subpackage imports only the API/support packages it needs,
- existing tests move cleanly,
- registry construction can hide adapter concrete types from `cmd/rally`,
- no behaviour changes are needed.

### C. Introduce a harness registry

Create a registry/factory that owns built-in executor construction and generic
executor construction from config.

Possible API:

```go
func BuildExecutors(cfg config.V2Config) (map[string]agent.Executor, error)
```

If importing `config` into the registry would create an undesirable dependency,
use a narrower input struct instead:

```go
type RegistryConfig struct {
    ClaudeModel string
    CodexModel string
    OpenCodeModel string
    AntigravityModel string
    Custom map[string]GenericConfig
}
```

Prefer the narrower input if it keeps harness construction decoupled from the
full config module.

### D. Split process/log support deliberately

Current adapters share process group setup and logged command behaviour. Move
that support into a package that harness adapters can import without depending
on unrelated adapter code.

Candidate names:

- `internal/agent/process`,
- `internal/harness/process`,
- `internal/execx`.

Keep this small. It should not become a dumping ground for parser helpers.

### E. Keep per-harness parsing local

Each harness module should own its CLI schema parsing and local disk-log
recovery logic. Shared helpers should only exist when two harnesses truly share
the same protocol or safety concern.

Examples:

- Claude session-log parsing stays in the Claude module.
- Codex session-log parsing stays in the Codex module.
- OpenCode server-log parsing stays in the OpenCode module.
- Antigravity glog parsing stays in the Antigravity module.

The `internal/reliability` package can continue to own failure-category parsers
if those parsers represent Rally's normalized taxonomy rather than harness
process execution.

### F. Add import guardrails after the first adapter move

After at least one adapter module exists, update `add-architecture-guardrails`
policy so harness adapter packages cannot import:

- `internal/relay`,
- `internal/config`, unless using a deliberate registry package,
- `internal/store`,
- `internal/progress`,
- `internal/telemetry`,
- `internal/cli`,
- future TUI/presentation packages.

## Testing Strategy

Before moving adapters:

- Run `go test -count=1 ./internal/agent ./internal/reliability ./internal/config ./internal/relay`.

For each adapter move:

- Move its unit tests with it.
- Run the adapter package tests.
- Run `go test -count=1 ./internal/agent/...` if subpackages are used.
- Run `go test -count=1 ./internal/relay` because relay consumes the executor
  interface.
- Run `go test -count=1 ./cmd/rally` because command wiring constructs
  executors.

For registry extraction:

- Unit-test built-in executor registration.
- Unit-test generic/custom harness registration from config-shaped input.
- Unit-test that configured model defaults reach the right executor.

Before completion:

- Run `go test -count=1 ./...`.
- If real harness tests are affected, run `RALLY_TEST_REAL_AGENTS=1 go test
  -count=1 ./internal/agent/... ./cmd/rally` where local credentials allow.

## Sequencing

1. Freeze and document the executor API.
2. Move generic or one built-in adapter as a spike.
3. Introduce registry construction and remove concrete adapter construction from
   `cmd/rally`.
4. Move remaining adapters one by one.
5. Add or tighten architecture guardrails for harness packages.
6. Update README architecture docs and add-new-harness guidance.

## Open Questions

- Is the current `agent` package name acceptable for the API, or should a later
  change introduce `harnessapi` or similar terminology?
- Should `agent.BuildPrompt` stay with the executor API, or move to
  `internal/agent_prompt` so harnesses consume a fully built prompt string?
- Should `internal/reliability` keep per-harness failure parsers, or should each
  adapter own parser details and return normalized evidence only?
- Should the registry accept full `config.V2Config` for convenience or a smaller
  adapter-specific config struct for cleaner boundaries?

## Out of Scope

- Adding a new harness.
- Changing CLI flags passed to existing harnesses.
- Changing failure classification semantics.
- Changing retry, routing, or telemetry behaviour.
