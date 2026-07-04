# Draft: Redesign Messaging

Status: drafted 2026-07-04 during TUI prototype selection (branch
`feat/tui-prototypes`). Concept-only capture; symbol/file references will
drift — re-verify when picked up.

## Why

The operator→agent messaging system exists but has never had a usable front
door, and the TUI prototype work exposed how underbaked it is. Decision
(2026-07-04): the selected TUI ships **without** its Messages tab; messaging
gets redesigned from the ground up before any UI commits to the current
semantics.

What exists today:

- A store-level inbox: `.rally/state/messages.jsonl` via
  `internal/store/store_messages.go`. `MessageRecord` carries id, body,
  status (`pending`/`addressed`/`cancelled`), an operator-set `position` for
  ordering, scope (`run` default, or `relay`), and consumption tracking
  (`consumed_by_run_id` / `consumed_by_relay_id`).
- Runner-side consumption in `internal/relay/runner/relay_steps.go`: at most
  one pending run-scoped message is consumed per run, relay-scoped messages
  are attached per relay, and the body is injected into the agent prompt.
- **No authoring surface.** There is no `rally message` command and no TUI
  write path; the `rally init` docs tell operators to inspect the file with
  `jq`. The only writers are tests and hand edits.

Problems observed while building the read-only TUI tab:

1. **Lifecycle semantics are one-way.** A message flips to `addressed` on
   consumption, but nothing records what the agent actually did with it — no
   ack, no excerpt of the response, no way to see it was ignored.
2. **Scope vocabulary is too coarse.** `run` vs `relay` doesn't cover the
   real cases operators want: "next run only", "every run until addressed",
   "runs routed to role X", "this specific lap".
3. **Ordering by mutable `position`** is an editing model (reorder the
   queue) bolted onto an append-only JSONL file, with truncation
   (`maybeTruncateMessages`) silently discarding history.
4. **The presentation boundary has no write channel.** Presentation packages
   cannot import the store (archguard), so a TUI compose/reorder/cancel flow
   needs an operator-intent callback seam (akin to
   `runtimeevent.ControlSource`) that was never designed.
5. **Naming predates `adopt-racing-terminology`** — `run`-scoped fields like
   `consumed_by_run_id` will be renamed under that change; a redesign should
   land on the post-rename vocabulary (outing/driver) rather than churn
   twice.

## Intent

Rethink messaging end to end, treating it as the operator's steering channel
into a live relay:

- **Authoring**: first-class compose/edit/cancel/reorder via both a CLI
  command and the TUI (through a proper operator-intent seam).
- **Delivery semantics**: define precisely when a message is injected (which
  prompt boundary, which retry behavior), and what scopes exist.
- **Feedback loop**: a message's record should show it was delivered, to
  which outing/try, and ideally carry the agent's acknowledgment.
- **Storage**: revisit append-only JSONL + mutable position + truncation;
  an event-sourced status trail or a small state file may fit better.
- **TUI reintroduction**: the Messages tab returns only once the above is
  settled.

## Open questions

- Should messages be able to target roles/laps, or only the temporal scopes?
- Is mid-try delivery (injecting into a running session) ever wanted, or is
  prompt-boundary delivery the contract?
- Does the laps `handoff` flow subsume any of this (agent→agent messaging is
  laps-owned; operator→agent is rally-owned)?
- Migration: is anything in existing `messages.jsonl` files worth preserving,
  or is this pre-adoption enough to break cleanly?

## Out of scope

- Agent→agent handoff messaging (owned by the laps tool surface).
- The rename itself (`adopt-racing-terminology`); this change just adopts
  its outcome.
- General TUI work tracked under `build-new-tui`.
