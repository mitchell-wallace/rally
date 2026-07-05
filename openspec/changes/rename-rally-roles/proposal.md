# Proposal: Roles v2 — authority-based built-in role set

Adopt the eight-role built-in set from `roles-v2-design.md` (GPT-5.5 Pro
baseline, reconciled against the current tree in `design.md`):

```
intern  junior  senior            (implementation ladder)
architect  review  verify  qa  recovery   (control and assurance)
```

Drop the built-in `ui` role — UI/branding guidance moves to repo-specific
skills. Existing `ui` role files and route entries keep working as custom-role
usage with a non-fatal advisory.

Core split: **roles = authority and operating mode; skills = method; routes =
concrete drivers.** Built-ins get a single-source-of-truth catalog
(`internal/roles`) carrying mode/write-policy so role behaviour stops being
scattered name checks.

Why now: the lap planner only has junior/senior/ui/verify/recovery to work
with; assignment gravitates to senior (see `laps-author-input-1.md`), review
has no home (verify absorbs it), and plan-repair has no plan-only role. This
lands before the TUI work so the TUI renders stable role concepts.

Non-goals: renaming `relay`/`outing`/`try` vocabulary (done elsewhere);
changing routing mechanics, retry budgets, or laps semantics; making role
names a closed enum (custom roles stay legal).
