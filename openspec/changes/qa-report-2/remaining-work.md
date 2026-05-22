# Remaining Work

The code for `auth-email-delivery-hardening` is complete. What remains is verification and bookkeeping.

## Immediate Actions

### 1. Run full test suite
```bash
pnpm --filter @mbtw/backend test
pnpm --filter @mbtw/frontend test
pnpm lint
pnpm typecheck
```
Confirm all pass. Fix any failures before proceeding.

### 2. Broken-SMTP smoke test (manual)
This is the critical test from tasks.md §6.3:

1. Stop or misconfigure SMTP locally (point `AUTH_EMAIL_SMTP_HOST` at unreachable host)
2. Register a fresh account against local backend:
   - Expect HTTP 201 returned promptly (no SMTP timeout)
   - Expect refresh + session cookies set
   - Check `auth_email_jobs` table: one row with `kind='email_verification'`, `status='pending'` or `'pending'` with `attempts > 0` after worker tick
3. Fix SMTP config, wait one worker tick (≤5s):
   - Row should transition to `status='sent'` with `sent_at` set
   - Email should arrive
4. POST `/auth/forgot-password` for present and missing emails:
   - Present: auth_email_jobs row created
   - Missing: no row, same 200 body
5. Visit `/reset-password` without token → invalid-link state
6. Settings → Check for updates → test online/offline/up-to-date states

### 3. Confirm no raw tokens in logs
Search server logs, browser network tab, test snapshots for any raw token leakage.

### 4. Verify SW cache name is v8
DevTools → Application → Cache Storage → only `prayer-app-shell-v8` present, no v7.

## Bookkeeping

### 5. Update laps.json
Mark as done:
- `work-c905` → `isDone: true`, set `completedAt`
- `pray-a349` → `isDone: true`, set `completedAt` (after verify passes)

### 6. Update tasks.md
Check all 39 boxes that have been implemented. This is cosmetic but signals completion.

### 7. Archive the OpenSpec change
If the project uses `openspec-archive-change`, run that workflow. Otherwise, move or tag the change as complete.

## Optional Follow-ups

### 8. Re-run final verify through rally
If desired, reset `pray-a349` to `isDone: false` and let rally pick it up with a fresh relay. This validates the rally pipeline end-to-end with the current code state.

### 9. git log audit
Confirm the two key commits are on `staging`:
- `c7a841f` fix auth email queue controller wiring
- `0f4a8b7` fix app version update comparison

Both currently on `staging` branch.

### 10. Test the worker tick end-to-end
Beyond broken-SMTP, verify the full retry/dead cycle:
1. Queue a job, keep SMTP down
2. Watch attempts increment over multiple ticks
3. After 8 attempts, confirm status transitions to `dead` and `logger.error` fires with `auth.email_job_dead`
