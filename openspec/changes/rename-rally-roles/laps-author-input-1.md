# Role taxonomy — laps author input #1

Feedback from preparing the `harden-relay-run-lifecycle` queue (18 laps).

## The problem

The JUNIOR/SENIOR labels imply a skill hierarchy, but the routing is
really about **what kind of judgment the work requires**, not how capable
the model is. The models currently on JUNIOR are strong enough to handle
most architecture work — the distinction is whether the *lap* needs
design judgment or just needs to execute a bounded scope well.

This mismatch causes two practical issues:

1. **Under-utilization.** Laps authors default toward SENIOR for anything
   non-trivial because "junior" feels wrong for important work, even
   when the work is well-scoped and pattern-following. The
   harden-relay-run-lifecycle queue came out 5 JUNIOR / 7 SENIOR / 6
   VERIFY — SENIOR-heavy because the label creates a gravitational pull
   toward "this is important, therefore senior."

2. **Unclear guidance.** The prepare-laps skill says "architecture-sensitive
   → SENIOR, mechanical → JUNIOR" but the boundary is fuzzy. A refactor
   that touches 20 call sites is mechanical *and* architecture-sensitive.
   Laps authors spend time debating the label when the real question is
   simpler: does this lap require design decisions that could sink later
   laps?

## Proposed rename

| Current | Proposed | Routes on | Typical share |
|---------|----------|-----------|---------------|
| SENIOR | **architect** | Design judgment, risk, correctness constraints | ~10–15% |
| JUNIOR | **builder** | Volume execution, pattern-following, bounded scope | ~50–60% |
| UI | **designer** | Visual/interaction design judgment | 5–15% |
| VERIFY | **analyst** | Review, verification, follow-up creation | ~20–25% |

The key shift: **builder becomes the default.** Most implementation laps
go to builder. Architect is reserved for laps where a wrong design
decision would require rework across multiple later laps.

### How the harden queue would look under this model

Laps that moved from SENIOR → builder:

- "Extend ClassifyError" — the enum design is specified in the change;
  the lap is executing a known design, not making one. → **builder**
- "Implement lap-ID pinning" — the pinning design is fully specified in
  design.md; the lap is implementing it. → **builder** (arguably)
- "Implement incomplete class behavior" — behavioral rules are specified;
  the lap wires them. → **builder**

Laps that stay architect:

- "ResilienceKey refactor" — wide refactor touching 20+ call sites with
  judgment calls about signature design. → **architect**
- "Probation state machine" — complex state machine with scheduler
  interaction, one-shot enforcement, multiple promotion/demotion paths.
  → **architect**

This shifts the distribution from 5/7/0/6 (J/S/UI/V) to roughly
8/4/0/6 (builder/architect/designer/analyst) — a much healthier split
that uses the builder route for its full capacity.

## The critical 10% question

Two options were discussed:

### Option A: architect *is* the critical path (recommended lean)

If builder handles most architecture work because the design is
pre-specified in change artifacts, then architect naturally becomes the
rare, high-stakes route — reserved for:

- State machines with non-obvious invariants
- Wide refactors where signature design matters
- Correctness-critical paths (auth, sync, data integrity)
- Laps where the outcome shapes the design of later laps

The prepare-laps skill would say: "default to builder; use architect only
when a wrong design decision would require rework across later laps."

Pros: no fifth role, naming communicates intent, simple routing.
Cons: architect gets a mixed bag of "truly critical" and "moderately
critical" — no way to express "this one is *really* important, use the
best model we have."

### Option B: add a `principal` role for the true top 10%

A fifth role mapped to the highest-reliability model. Used for:

- The single most dangerous lap in a queue
- Laps where a subtle bug would be catastrophic and hard to catch in
  VERIFY
- Design laps whose output shapes the entire change

Pros: explicit signal for critical work, can map to a more expensive
model.
Cons: boundary between architect and principal is fuzzy, adds routing
complexity, prepare-laps skill needs guidance on when each applies.

If principal is added, architect shifts to "design-aware but not
critical" — essentially a promoted builder with more discretion. The
distribution might be: 55% builder, 15% architect, 5% principal, 5%
designer, 20% analyst.

### My lean

Option A for now. The architect label is strong enough to carry the
critical signal, and the boundary problem with principal ("is this
*really* critical or just important?") is the same problem JUNIOR/SENIOR
has today. If you find that architect is getting too diluted — too many
laps routing there when they could be builder — then adding principal
makes sense. But let the rename settle first.

## The mechanical/low-tier route

A `grunt` or `worker` role mapped to a cheaper/faster model for purely
mechanical work:

- Pure renames (file renames + all callers)
- Import fixups after a package move
- Template-following boilerplate (add a new JSONL store method matching
  the existing pattern)
- Doc-only updates with no judgment

**Not worth adding by default.** In the harden queue, the JUNIOR laps
(renames, hourly retry, bounded prompt, docs) are all bounded but still
require reading context and making small decisions. A truly mechanical
model would struggle with things like "update all callers" where the
call sites aren't enumerated. The cost savings from a cheaper model only
matter at high volume.

**Worth adding per-queue** when a specific queue has 20%+ laps that are
genuinely mechanical — like a large rename or migration where every lap
follows the same template. In that case, `grunt` (or `worker`) could be
declared in config.toml with a cheap/fast model route and used for those
specific laps.

## Impact on prepare-laps skill

The skill would need:

1. Updated role definitions in section 3 (Assign roles)
2. Updated default guidance: "default to builder; architect for design
   decisions that shape later laps"
3. If principal is added: explicit criteria for when to escalate from
   architect to principal
4. If grunt is added: note that it's optional and per-queue, not a
   default role

The role files in `.rally/agents/` would also need updating via
`rally init roles` (or a manual pass).

## Open questions

1. Should the rename be a breaking change (update all existing queues,
   config files, agent docs) or a migration (support both old and new
   names for a release)?
2. Does the rename affect `rally routes check` or is it purely a
   config + agent-doc change?
3. If principal is added later, should it be a config-level alias
   ("this queue's principal maps to claude-opus") or a first-class role
   with its own agent doc?
