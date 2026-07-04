# Draft: Adopt Racing Terminology

Status: drafted 2026-07-04. Concept-only capture; low priority. Anchored
loosely to `a9badbf` (tip of `feat/tui-prototypes`, after the TUI prototypes).
Symbol/file references below will drift — re-verify when picked up.
Updated same day: the original run → stint pick was superseded by
run → **outing** after `stint` was claimed by an upcoming laps feature; the
conflict and the naming principles it produced are recorded below.

## Why

Rally's naming straddles track-and-field and motor racing, and the racing side
is consistently winning: the product is `rally`, the task queue is `laps`, the
route-selection code already talks about a "lane" stalling in operator-facing
warnings (`route_runtime_construct.go`), telemetry is a racing concept
natively, and the skill vocabulary growing around the repo is `test-driving-
rally`, `crew-chief`, and a future `rally-driver`. Meanwhile the core
execution hierarchy — relay > run > try, executed by a *runner* — is
track-and-field (baton relay, runners running).

Beyond theme, the split causes concrete comprehension costs that showed up
while orchestrating the TUI-prototype build (a session that touched runner
internals, the store, presentation, and CLI):

1. **"runner" means two unrelated things.** AGENTS.md defines a runner as a
   harness+model combination, but `internal/relay/runner` is the orchestrator
   package and `runner.Runner` is the engine that drives a whole relay. An
   agent reading "the runner emits events" and "route the lap to a runner"
   must resolve the collision from context every time.
2. **"run" is un-greppable and overloaded.** It is simultaneously the middle
   entity of the hierarchy (`RunID`, `run_id`, `RunHeaderReady`, "run: 1/3"),
   a Go convention (`Run()` methods, cobra `RunE`), an English verb in half
   the comments, and part of unrelated names (`RunOptions`, `RunDemo`,
   `RunView`, `RunFeed`, `RunEntry`). Searching for the *entity* is hopeless.
3. **try vs attempt is already inconsistent.** The store says `TryRecord` /
   `tries.jsonl` / `reliability.TryOutcome` / `harnessapi.TryResult`, but the
   presentation events say `AttemptFinished` / `FooterData.Attempt` /
   `MaxAttempts`, and `TryRecord` itself carries `AttemptNumber`. Two words
   for one concept across layer boundaries.

A rename is cheap comprehension leverage for agents *if* the target words are
distinct, greppable, and collision-free — and expensive churn if it touches
persisted state or external names carelessly. The analysis below is organized
around that tradeoff.

## Naming candidates and analysis

### runner (harness+model) → **driver** — recommended, highest value

| Option | For | Against |
|---|---|---|
| **driver** | The exact racing counterpart (the one who drives); resolves the runner/orchestrator collision outright; already emergent (`test-driving-rally`, planned `rally-driver` skill); no collisions in this codebase | "driver" is overloaded in the wider software world (device/db drivers) — but not here |
| pilot | Distinct | Wrong sport register (aviation/F1 slang only) |
| crew | Rally-authentic (driver+co-driver = crew) | Wanted for the *team* concept (crew-chief skill); too collective for one harness+model |

The orchestrator package/type can then keep `Runner` unambiguously, or later
take a racing name of its own (**race control** — `racecontrol` — is the
authentic term for the thing that starts/stops running and enforces rules).
That package rename is optional tier-3 polish; freeing the word is the win.

### run (one runner on one lap) → **outing** — recommended, biggest clarity gain

**Why not stint (decision, 2026-07-04):** `stint` was the original pick — it
is the racing-precise term for one driver's continuous turn, ended by a driver
change, which matches the skip/reroute semantic exactly. But an upcoming laps
feature already uses **stint** for a composable (potentially nested) sequence
of laps inside `laps.json` — independently decomposed queues become stints. We
considered reassigning: give laps the itinerary-pure terms (`leg` or
`section` — rallies divide into legs, legs into sections) and take stint for
rally. Rejected, on a principle worth keeping: **laps-facing vocabulary must
carry safe first-read intuition for humans who author queues without rally in
the picture.** "Leg" misleads a non-racing reader (it sounds like a *part of*
a lap, not a group of them); "section" is generic and collides with markdown
document sections in the very OpenSpec plans laps queues are prepared from.
"Stint" for a run of consecutive laps trips neither wire even for readers who
don't know racing. So stint is laps-owned; rally does not use it.

| Option | For | Against |
|---|---|---|
| **outing** | Real motorsport usage ("a strong outing"); zero collisions in this tree, in Go idiom, or in comment prose; perfectly greppable | Mildly obscure on first read — acceptable here because operators only ever *consume* the term (headers, footers, records) and can pick it up from usage; nobody has to produce it the way laps authors produce laps vocabulary |
| stint | Racing-precise for exactly this semantic | Taken by laps (above) |
| drive | Natural noun, no Go-idiom collision | `drive`/`driver` substring adjacency degrades grep and reads awkwardly ("the drive's driver") |
| leg | Rally-authentic | Reads as part-of-a-lap to non-racing readers; communicates little in code |
| pass | Recce passes are real rallying | Dead on arrival: footers print "passed" — direct outcome collision |

This is the highest-churn rename (persisted `run_id` in both `tries.jsonl`
records and `summary.jsonl` `RunEntry`; `RunHeaderReady` and friends in the
runtimeevent contract; operator-facing "run: 1/3" headers) and also the
highest-payoff one: after it, the entity greps cleanly and prose like "the
outing failed after two tries" is unambiguous where "the run failed" never
was.

### Naming principles (recorded from the stint conflict)

1. **Identifiers optimize for agents**: greppable, collision-free against Go
   idiom, this codebase, and test-outcome words. This is the acceptance test
   for any rename.
2. **Terms humans must *produce*** (laps CLI verbs, queue authoring, config
   keys) additionally need safe first-read intuition without racing knowledge
   — laps' surface is used standalone, without rally in the picture.
3. **Terms humans only *consume*** (operator output, record names) may be
   mildly unfamiliar if they are unambiguous and learnable from usage —
   `outing` qualifies; a misleading-but-familiar word does not.
4. **Vocabulary ownership is split by tool**: laps owns the schedule/itinerary
   nouns (lap, stint), rally owns the cockpit/execution nouns (driver, outing,
   try, lane). Neither tool reaches across; conflicts get resolved in favor of
   the owner.

### try (one invocation) → **keep "try"** — recommended; standardize the convention instead

| Option | For | Against |
|---|---|---|
| **keep try** | Already the persisted noun across four layers (`TryRecord`, `tries.jsonl`, `TryOutcome`, `TryResult`, telemetry `RallyTry`); short; not actually track-and-field (it's rugby at worst, generic English at best) | Leaves the try/attempt split unless the convention is written down |
| attempt | Presentation events already use it; unambiguous | Longer; churns the just-stabilized runtimeevent contract or the store+telemetry — one side must move either way |
| restart / start | Racing flavor for retries specifically | Overloads `rally start`; wrong for the first invocation |

Recommendation: keep `try` as *the entity* and codify the existing de facto
rule — "attempt N of M" is the ordinal phrasing for operator-facing output,
`try` is the record/result noun. Write that into AGENTS.md terminology.
(Racing footnote for docs flavor only: WRC's restart-after-retirement rule is
literally called **super rally** — a lovely name for the retry budget in
prose, too cute for an identifier.)

### relay (the campaign) → **race**, or keep — genuinely contestable

| Option | For | Against |
|---|---|---|
| **race** | The correct container of laps — "a race over a queue of laps" makes the whole hierarchy coherent with the `laps` tool; short; natural operator prose ("race #42 complete") | "race" collides with race-detector prose (`go test -race`, "data race") in docs/comments, though barely in identifiers; churns persisted `relays.jsonl` + `relay_id` |
| heat | Distinct, greppable, authentically racing | A heat is a short preliminary sprint — wrong weight for a long campaign |
| round / meeting | Motorsport-calendar accurate | Bland in code; "round" collides with rounding |
| rally | Maximally on-brand | Rejected: collides with the product name everywhere (module path, binary, telemetry prefix `RallyTry` → `RallyRally…`), and grep for "rally" is already useless |
| stage | THE rally-racing unit | Wrong level: stages are the *task-sized* unit in real rallying — that seat is taken by laps; also "staged" collides with git prose |
| **keep relay** | Cheapest; "relay" is greppable and, read generously, is also an electronics/message-relaying metaphor; the pain here is thematic, not practical | Keeps the track-and-field flagship term at the top of the hierarchy |

Recommendation: if the theme consolidation is worth doing at all, `race` is
the right target and the `-race` collision is docs-level noise. But this is
the one rename where "keep" is defensible — unlike runner/run, `relay` caused
no observed agent confusion. Sequence it last; drop it under time pressure.

### Secondary candidates (flavor tier — docs and prose only, no identifiers)

- **routes → lanes**: the code's own warnings already say "lane" for a route's
  runner list. Adopt "lane" in docs/prose as the informal term for a route's
  fallback sequence; renaming the `[routes]` config key is not worth breaking
  every config file.
- **role instructions → pace notes**: rally-authentic (the co-driver reads
  pace notes telling the driver how to take the stage — exactly what
  `.rally/agents/<role>.md` does for a lap). Charming in docs; do not rename
  the folder or flags.
- **handoff**: keep. It is owned by the `laps` tool surface (`laps handoff`),
  ships separately, and "driver change" — the racing equivalent — is worse in
  every practical way.
- **roles (junior/senior/ui/verify)**: out of scope — `rename-rally-roles`
  already covers them and deliberately chose semantic names (architect/
  builder/designer/analyst), not thematic ones. Racing-theming roles would
  fight that change.
- **monitor / telemetry / benched**: telemetry is already racing-native;
  "pit wall" for the monitor is available flavor for docs; `benched` is
  sports-generic and fine.
- **harness**: keep, obviously — and enjoy that harness racing is a real
  (horse) sport, so the term was never track-and-field either.

## The schemes, compared

| | Hierarchy | Executor | Churn | Agent-facing clarity gained |
|---|---|---|---|---|
| A (full) | race > outing > try | driver | High (persisted names, events, CLI prose) | Full theme coherence; every entity greppable |
| **B (recommended)** | relay > outing > try | driver | Medium (no relays.jsonl churn) | Fixes both observed confusions (runner collision, run overload); relay stays greppable |
| C (minimal) | relay > run > try | driver | Low (docs + config/routing prose) | Fixes the worst collision only; "run" stays hostile to grep |

Test sentence, scheme B: *"Rally routes the lap down its lane to a driver; the
driver's outing may take several tries; if the driver can't finish, the lap
gets a driver change — a new outing."* Every noun is distinct, greppable, and
resolves without AGENTS.md open. That property — not metaphor purity — is the
acceptance test for any final scheme.

## Migration tiers (whatever scheme is chosen)

1. **Docs/prompts first** (AGENTS.md terminology section, README, agent_prompt
   sources, skill docs): full rename, immediate, zero risk.
2. **Go identifiers/packages**: mechanical rename laps, gated by the full test
   suite and archguard; runtimeevent kinds and store types are internal and
   fair game.
3. **Persisted state** (`tries.jsonl`, `relays.jsonl`, `summary.jsonl` field
   names like `run_id`, `relay_id`): tolerant readers (accept old + new keys)
   or a `store/migration.go` entry — never a silent format break; these files
   are committed to user repos.
4. **External names**: telemetry event names (`RallyTry` etc. — New Relic
   dashboards depend on them: keep, or dual-emit through a deprecation
   window), CLI command/flag surface (keep `relay` as an alias the way `start`
   already aliases it today), and anything owned by the `laps` companion
   (out of scope for this change entirely).

## Timing

Low priority. Sequence after `rename-rally-roles` (avoid two simultaneous
vocabulary migrations confusing in-flight agents and laps queues) and after
the TUI prototype selection lands (the TUI renders the operator-facing prose
that a rename would touch; renaming mid-prototype doubles the churn).

## Out of Scope

- The `laps` tool's name, commands, and hook vocabulary (separate repo/tool).
- Role taxonomy (owned by `rename-rally-roles`).
- Any behavior change; this is vocabulary only.
- Renaming the `[routes]` config key or CLI flags without aliases.
