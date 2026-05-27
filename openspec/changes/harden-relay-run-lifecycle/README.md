# harden-relay-run-lifecycle

An OpenSpec change that hardens rally's run lifecycle and state tracking, driven
by a black-box QA review of a stalled `rally`-driven implementation run against
the sibling `Prayer-app` repo.

The canonical change artifacts are `proposal.md`, `design.md`, `tasks.md`, and
`specs/`. The QA reports below are preserved as **evidence** for the findings;
they are black-box (no rally/laps source was read), so each finding was
re-grounded against the current code before being scoped into the change — see
`design.md` for which claims were adopted and which were treated as motivation
only.

## Evidence (preserved QA review)
- `qa-report/findings.md` — detailed timeline, observed state drift, suspected
  failure modes
- `qa-report/recommendations.md` — suggested improvements, separated from
  confirmed findings
- `qa-report-2/` — second-pass findings, assumptions, process issues, state
  assessment, and remaining target-repo work
- `qa-suggestion/resolution-suggestion.md` — proposed resolutions (the only
  evidence file that spot-checked rally source)

## What this change does (summary)
1. **State integrity** — lap-ID pinning (rejects phantom lap completions),
   opt-in completion file cross-check, and role-aware freeze-recovery (VERIFY
   success requires a verdict, not just committed files).
2. **Freeze/retry/resume reliability** — freeze decay (no longer terminal),
   `--new` resets agent status, infra-only failure classification feeds the
   breaker, and less-timid hourly retries — so a harness can no longer be
   permanently frozen for a repo.
3. **Bounded prompt context** — caps recent-try context by count + character
   budget so the assembled prompt can't blow up the argv limit.

## Out of scope
- The `laps done`-from-subdirectory root cause — fixed upstream in `laps`
  v0.4.6.
- Prayer-app target-repo remediation (run tests, broken-SMTP smoke test, mark
  laps done, archive the change) — tracked separately; see
  `qa-report-2/remaining-work.md`.
- stdin prompt transport and a `rally reconcile` command — see `design.md`
  Non-Goals.
