# State Assessment: Code vs. Laps vs. OpenSpec

## Laps Task Status (from `.laps/laps.json` as of 2026-05-22)

| Task ID | Title | isDone | Code State | Notes |
|---------|-------|--------|------------|-------|
| pray-f623 | Reset-password: token never editable | **done** | Committed | Verified in prior VERIFY pass |
| pray-7a80 | Forgot-password: constant-time | **done** | Committed | Verified |
| pray-dc54 | SW: version-aware cache + v8 bump | **done** | Committed | Verified |
| pray-a536 | Extract checkForUpdates() | **done** | Committed | Verified |
| pray-9a1b | Settings "Check for updates" button | **done** | Committed | Verified |
| pray-5ed1 | Mid-task verify | **done** | N/A (verification pass) | Found work-e03d blockers |
| work-e03d | Fix SW v8 + timing parity blockers | **done** | Committed | Fixed in Run 9 |
| pray-0fda | Auth email queue: migration + service | **done** | Committed | Verified |
| pray-b893 | Worker tick: retry, backoff, dead | **done** | Committed | Verified |
| pray-43a5 | Wire controllers to queue | **done** (BOGUS) | Committed (later) | Marked done in Run 16 but code was NOT written. Code actually landed in Run 18 via c7a841f. |
| pray-1368 | Docs: queue + SW deploy note | **done** | Committed | Verified |
| **work-c905** | **Fix controller wiring blocker** | **NOT DONE** | **Committed (c7a841f)** | Code is fixed. Task tracking is stale. |
| **pray-a349** | **Final verify: tests + broken-SMTP smoke** | **NOT DONE** | **Not executed** | Full test run + manual SMTP smoke never done. |

## OpenSpec tasks.md Checkbox State

`openspec/changes/auth-email-delivery-hardening/tasks.md` has **0/39 checkboxes checked**. All 39 remain `- [ ]`. This file was never updated by any agent throughout the entire relay.

## Code Completeness Assessment

### §1 — Reset-password page (Complete)
- `ResetPasswordView.vue`: Token read from `route.query.token` only. No editable input.
- Missing/invalid token → "invalid or expired link" state with `/forgot-password` link.
- Server-side `field === 'token'` errors mapped to invalid-link state.

### §2 — Constant-time forgot-password (Complete)
- `forgot_password_controller.ts`: Present-user path calls `issuePasswordResetToken` + `enqueuePasswordReset`. Missing-user path calls `issueDiscardedPasswordResetToken` (same crypto cost, rolled-back insert).
- Controller is wired to `authEmailQueue` (not inline `authEmailDispatcher`).

### §3 — Version-aware SW cache (Complete)
- `service-worker.js`: `CACHE_NAME = 'prayer-app-shell-v8'`. `SET_FRESHER_VERSION_AVAILABLE` handler. `NETWORK_FIRST` mode for app-shell/assets when fresh version available.
- `updater.ts`: Posts `SET_FRESHER_VERSION_AVAILABLE` on reachable version mismatch.

### §3b — Check for updates (Complete)
- `checkForUpdates()` extracted with discriminated union return type.
- SettingsView "About" section with APP_VERSION + "Check for updates" button with all states.
- Force bypass, concurrent call coalescing.

### §4.1 — Migration (Complete)
- `1773500000000_create_auth_email_jobs_table.ts` exists with all specified columns, constraints, and index.

### §4.2 — Queue service (Complete)
- `auth_email_queue.ts` with `enqueuePasswordReset` and `enqueueEmailVerification`.
- Dedupe via `sha256(rawToken).slice(0, 16)` + unique constraint.
- DI-friendly (`createAuthEmailQueue` factory).

### §4.3 — Worker tick (Complete)
- `start/auth_email_worker.ts`: 5s tick, in-process mutex, FOR UPDATE SKIP LOCKED, mark-sending-then-commit pattern.
- Exponential backoff: `min(60s * 2^(attempts-1), 30min)`. Dead at 8 attempts.
- Registered in `start/kernel.ts`.
- Gated to `app.getEnvironment() === 'web'`.

### §4.4 — Controller wiring (Complete — but tracking is stale)
- `forgot_password_controller.ts`: Uses `authEmailQueue.enqueuePasswordReset`. Inline dispatch removed.
- `register_controller.ts`: Uses `authEmailQueue.enqueueEmailVerification` with defensive try/catch logging `auth.email_verification_enqueue_failed` on failure, still returns 201.
- Both verified by reading current file contents on host (2026-05-22).

### §4.5 — Observability (Likely complete)
- Worker tick summary logger in place. Raw tokens not logged.
- Registration enqueue failure logged with `auth.email_verification_enqueue_failed` event.

### §5 — Docs (Complete)
- `docs/systems/auth-email-queue.md` exists.
- `docs/operations/production-operations.md` has auth email delivery subsection.

### §6 — Cleanup and verification (NOT DONE)
- `pnpm test` (all workspaces) never run as part of final verify.
- `pnpm test:e2e` never run.
- `pnpm lint` / `pnpm typecheck` never run as final gate.
- **Broken-SMTP smoke test never performed**: Register with SMTP down → confirm 201, confirm auth_email_jobs row, confirm status transitions to dead/pending with backoff.

## Staleness Summary

| What | State | Action Needed |
|------|-------|---------------|
| `work-c905` in laps.json | `isDone: false` | Mark done (code is committed) |
| `pray-a349` in laps.json | `isDone: false` | Execute verify, then mark done |
| tasks.md checkboxes | 0/39 | Update all to `[x]` |
| Code | Complete | No changes needed |
| Test suite | Unknown | Run full suite |
| Broken-SMTP smoke | Not done | Perform manually |
