## Implementation Handoff

This document captures the follow-up implementation work from branch review against `consolidate-rally-gry`.

Legacy `.rally/config` migration is intentionally out of scope here.

## Confirmed Behavior Decisions

### Relay resume and agent mix

When `rally relay` finds an unfinished relay and the user chooses `resume` from the interactive prompt:

1. If no `--agent` flags were passed, continue with the relay's stored mix.
2. If `--agent` flags were passed, prompt again to choose whether to keep the stored mix or overwrite it with the new CLI mix.

When `rally relay --resume` is used:

1. If no `--agent` flags were passed, continue with the relay's stored mix.
2. If `--agent` flags were passed, overwrite the stored mix with the new CLI mix without an extra prompt.

If a resumed relay adopts a new mix, the relay record should also be updated so future resumes reflect that choice.

### Message consumption

Run-scoped messages:

1. If a run fails, the consumed message remains eligible for future runs until it is addressed.
2. Eligible-but-unaddressed consumed messages should be re-consumed at the front of the queue.
3. Retries within the same run should continue using the same message.

Relay-scoped messages:

1. A relay-scoped message should be consumed once per relay.
2. It should continue to apply across all runs in that relay.
3. If that relay is resumed, the same relay-scoped message should be restored.
4. A different relay must not consume the same relay-scoped message again.

## Remaining Work

### 1. Commit Rally state as durable git-backed state

Problem:
Store writes currently happen after workspace auto-commit logic, and normal `.rally/*.jsonl` writes are not committed unless window truncation fires. That leaves Rally state dirty in the working tree instead of durable in git. It also lets later tries inherit dirty Rally state and interfere with no-op failure detection.

Current code:
`internal/relay/runner.go`
`internal/store/store.go`
`internal/store/window.go`
`internal/relay/runner_test.go`

Required end state:

1. Try/workspace change detection must be based on user-agent workspace changes, not on Rally's own JSONL writes.
2. Rally state writes should end each try/run in a clean git state when running inside a git repo.
3. Operational Rally state commits must not overwrite or blur the recorded try commit hash semantics.
4. Tests should stop masking the problem by excluding `.rally/*.jsonl` from git status.

Suggested acceptance tests:

1. Successful run with no agent-created commit leaves repo clean after the run.
2. Successful run with agent-created commit still records the agent commit hash, while Rally state is also persisted cleanly.
3. No-op try remains a failure even if Rally writes state during the attempt.

### 2. Resolve workspace state from repo root, not current subdirectory

Problem:
`relay`, `init`, and `instructions` use `os.Getwd()` directly. Running from a subdirectory can create or look up `.rally/` in the wrong place.

Current code:
`cmd/rally/main.go`
`internal/gitx/git.go`

Required end state:

1. If the current directory is inside a git repo, Rally should operate on the repo root.
2. `rally init` should initialize the repo root when already inside a repo.
3. If not inside a git repo, `rally init` should continue to initialize the current directory.

Suggested acceptance tests:

1. `rally relay` from a nested subdirectory uses `<repo-root>/.rally`.
2. `rally instructions edit/show` from a nested subdirectory uses `<repo-root>/.rally/instructions.md`.
3. `rally init` from a nested subdirectory inside an existing repo does not create a nested repo.

### 3. Fix resume-time agent mix handling

Problem:
Resumed relays currently use the mix derived from the current CLI invocation instead of the relay's stored mix.

Current code:
`cmd/rally/main.go`
`internal/relay/runner.go`
`internal/relay/relay.go`
`internal/relay/mix.go`

Required end state:

1. Interactive resume should default to the stored relay mix.
2. Interactive resume with new `--agent` flags should ask whether to keep the stored mix or overwrite it.
3. `--resume` should be non-interactive, but new `--agent` flags should overwrite the stored mix.
4. The runner should execute with the final chosen mix, not the default fallback mix.
5. If the mix is overwritten, the relay record should persist the new `agent_mix`.

Suggested acceptance tests:

1. Resume with no `--agent` flags preserves the stored mix.
2. Resume with `--resume --agent ...` overwrites the stored mix without prompting.
3. Interactive resume with `--agent ...` can keep the old mix or replace it, based on the user's choice.

### 4. Fix message window truncation

Problem:
`maybeTruncateMessages()` computes the correct kept set, but `commitThenTruncate()` truncates by trailing line count instead of that kept set. The follow-up rewrite then leaves `messages.jsonl` dirty.

Current code:
`internal/store/store.go`
`internal/store/window.go`

Required end state:

1. The pre-truncation commit should archive the real full file.
2. The truncation commit should contain the exact intended kept message set.
3. The store should not leave `messages.jsonl` dirty after truncation.

Suggested acceptance tests:

1. Mixed pending and resolved messages truncate to the intended kept set.
2. Git history shows full-file archive commit followed by correct truncation commit.
3. Git status is clean after truncation completes.

### 5. Align message eligibility with the agreed semantics

Problem:
Pending-message selection currently ignores consumption state and can both re-consume the wrong messages and fail to restore the right relay-scoped message on resume.

Current code:
`internal/store/store.go`
`internal/relay/runner.go`
`internal/store/records.go`

Required end state:

1. Run-scoped pending messages with `status == "pending"` remain eligible until addressed.
2. If such a message was consumed by a failed run, it should be selected again ahead of untouched pending run-scoped messages.
3. Retries inside the same run should continue reusing the already-consumed message.
4. Relay-scoped message lookup for a new relay should exclude messages already consumed by a different relay.
5. Relay resume should restore the relay-scoped message already consumed by that relay.
6. Relay-scoped messages should not be re-consumed by later relays unless the same relay is being resumed.
7. If a run-scoped message is re-consumed by a later run, its `consumed_by_run_id` can be updated to that latest run.

Suggested acceptance tests:

1. A failed run leaves its message pending and first in line for the next run.
2. A later successful run can address that same message and remove it from future eligibility.
3. A relay-scoped message consumed in relay 1 is restored on relay 1 resume.
4. That same relay-scoped message is not offered to relay 2.

### 6. Record relay-scoped message consumption at consume time

Problem:
`RelayRecord.ConsumedMessageIDs` is only updated when a relay-scoped message is addressed, but the spec requires tracking consumed relay-level message IDs.

Current code:
`internal/relay/runner.go`
`internal/store/records.go`

Required end state:

1. Consuming a relay-scoped message should append its ID to `consumed_message_ids` immediately.
2. Addressing that message later should not be required for the relay record to show consumption.
3. Resume logic should preserve that consumed ID without duplicating it.

Suggested acceptance tests:

1. Relay-scoped message consumed but not addressed still appears in `ConsumedMessageIDs`.
2. Resuming the same relay does not append the same message ID twice.

## Suggested Implementation Order

1. Fix repo-root resolution first so CLI behavior is stable in tests.
2. Fix resume-time agent mix selection next, since it is a pure control-flow change.
3. Fix message eligibility and relay-scoped resume behavior together.
4. Fix `ConsumedMessageIDs` at the same time as message eligibility.
5. Fix message truncation.
6. Finish with the Rally-state commit/durability work, since it cuts across runner and store behavior and will likely require the broadest test updates.
