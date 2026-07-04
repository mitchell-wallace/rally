# Presentation wiring inventory — regenerated (task 1.2)

Regenerated against the **live tree at HEAD `56f2b9d`** (design baselined at
`f55712c`; #4 `modularize-harness-adapters` landed AND archived at HEAD). This
is the emit-site checklist phases 3–5 consume. Line numbers are current;
where they drifted from the design's `f55712c` references, the drift is flagged
inline. No production changes were made producing this note.

Baseline gate (all green at HEAD `56f2b9d`): `go build ./...`,
`go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./...`,
`go run ./tools/archguard --ci` (exit 0).

## (a) Runner import sites — `internal/style` / `internal/keyboard`

`internal/style` (runner is the sole production importer — the
`tools/archguard/policy/deps.go` hit is a policy-table string, not an import):

| File | Line |
|---|---|
| `run_one.go` | 22 |
| `handoff_only.go` | 18 |
| `relay_steps.go` | 14 |
| `terminal.go` | 11 |

`internal/keyboard`:

| File | Line |
|---|---|
| `action_loop.go` | 10 |
| `run_one.go` | 16 |
| `terminal.go` | 10 |

Phase-6 Decision-7 target: runner allow-list drops `style` + `keyboard`, adds
`relay/runner/runtimeevent`.

## (b) Direct `os.Stdout` / `os.Stderr` / `os.Stdin` uses

| File:Line | Stream | Use |
|---|---|---|
| `run_one.go:497` | Stdin + Stdout | `keyboard.NewKeyboard(os.Stdin, os.Stdout)` (active-try ctor) |
| `run_one.go:526` | Stdout | `mon.Start(os.Stdout)` — **residual, stays (Decision 8)** |
| `run_one.go:1340` | Stdin | pause `bufio.NewReader(os.Stdin).ReadString('\n')` → `WaitResume` |
| `relay_steps.go:58` | Stderr | route warnings loop (`for _, w := range routeRuntime.Warnings()`) |
| `relay_steps.go:171` | Stderr | `selection.Route.Warning` |
| `task.go:62` | Stderr | laps-instructions-file warning |
| `task.go:79` | Stderr | free-run-prompt-file warning |
| `terminal.go:48` | Stdin + Stdout | `keyboard.NewKeyboard(os.Stdin, os.Stdout)` (wait-countdown ctor) |
| `terminal.go:55` | Stdout | `waitLoop(..., os.Stdout, ...)` countdown target |
| `handoff_only.go` | — | none direct; footer via `r.outWriter()` at 217 |

## (c) Keyboard construction sites

| Site | Phase |
|---|---|
| `terminal.go:48` | wait countdown → `ControlSource.Start` session |
| `run_one.go:497` | active try → `ControlSource.Start` session |

## (d) Monitor lifecycle calls — **stay runner-driven (Decision 8)**

| Site | Call |
|---|---|
| `run_one.go:502` | `monitor.NewMonitor(...)` |
| `run_one.go:514` | `mon.Tick()` (initial status → `TryStatusSnapshot` data) |
| `run_one.go:526` | `mon.Start(os.Stdout)` (the one permitted direct-stream residual) |
| `run_one.go:589` | `mon.Stop()` |
| `run_one.go:735` | `renderRunFooter(...)` (cancelled) |
| `run_one.go:1084` | `renderRunFooter(...)` (pass/fail) |
| `handoff_only.go:217` | `renderRunFooter(...)` (handoff-only) |
| `action_loop.go:196` | `mon.SetArmed(keyboard.ArmMessage(...), keyboard.ConfirmWindow)` — text moves to `runtimeevent` helpers |
| `action_loop.go:201`, `:209` | `mon.SetActing(keyboard.ActMessage(...))` — text moves to `runtimeevent` helpers |

`renderRunFooter` itself defined in `terminal.go:21`; writes at `terminal.go:24`
(interim, `\r\x1b[2K%s\r`) and `terminal.go:27` (final, `\r\x1b[2K%s\n`).

## (e) Print-site output inventory — Decision 3 vocabulary cross-check

Every design-table row verified against current source. **Design ref** = the
`f55712c` line the design cites; **Current** = live line at HEAD `56f2b9d`.

| Event | Design ref | Current site | Bytes / notes | Drift |
|---|---|---|---|---|
| `RelayStarted` / `RelayCompleted` | no print | — | data-only; terminal sink no-ops | — |
| (`Relay complete.` owner) | `relay_start.go:201` | `internal/app/relay_start.go:201` `fmt.Fprintln(out, "Relay complete.")` | stays app-owned, NOT an event | ✓ exact |
| `RouteWarning` | `relay_steps.go:57,170` | `relay_steps.go:58` (warnings loop) + `relay_steps.go:171` (`Route.Warning`) → `os.Stderr` | also mirrored to `log` at 172 (log stays) | **+1 line** (57→58, 170→171) |
| `TaskFileWarning` | `task.go:62,79` | `task.go:62`, `task.go:79` → `os.Stderr` | | ✓ exact |
| `RunHeaderReady` | `run_one.go:456` | `style.RenderHeader` at `run_one.go:456`; printed `fmt.Fprintln(r.outWriter(), header)` at `run_one.go:469` | | ✓ exact |
| `TryStatusSnapshot` + `ShortcutHintReady` | `run_one.go:514–524` | `mon.Tick()` at 514; initial-status print `fmt.Printf("\r\x1b[2K%s\n", initialStatus)` at 521; `fmt.Printf("\r\x1b[2K%s\n", style.ShortcutHint())` at 524 | | ✓ in range |
| `RetryFooterUpdated` | interim `renderRunFooter` | `terminal.go:24` `\r\x1b[2K%s\r` (in-place redraw) | | ✓ |
| `AttemptFinished` | `run_one.go:1084` | `renderRunFooter(r.outWriter(), ...)` at `run_one.go:1084` | | ✓ exact |
| `AttemptCancelled` | `run_one.go:735` | `renderRunFooter(r.outWriter(), ...)` at `run_one.go:735` | | ✓ exact |
| `HandoffAttemptFinished` | `handoff_only.go:217` | `renderRunFooter(r.outWriter(), ...)` at `handoff_only.go:217` | | ✓ exact |
| `RateLimitWaitStarted` | `run_one.go:1014` | `fmt.Println(style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", cooldown)))` at `run_one.go:1014` | | ✓ exact |
| `WaitStarted`/`WaitTick`/`WaitFinished` | `terminal.go` | `terminal.go:80` dim line render; frame write `terminal.go:107` `\r\x1b[J%s\r\n%s\x1b[1A\r`; clear `terminal.go:112` `\r\x1b[J` | raw-mode `\r\n` cadence | ✓ |
| `OperatorActionArmed`/`OperatorActionApplied` | armed hint `terminal.go:87` | `terminal.go:87` `style.WarningStyle.Render("⌨ " + keyboard.ArmMessage(armedAction))` (real print); active-try arm/act = monitor indicators (`action_loop.go:196/201/209`, no-op in sink) | | ✓ exact |
| `PausePromptShown` | `run_one.go:1339` | `fmt.Println("Paused — press Enter to resume")` at `run_one.go:1339`; the blocking read is at `run_one.go:1340` | design Context said the bufio read was at `:1339` — read is now `:1340` (print at 1339) | **+1** on the read |
| `RelaySummaryReady` | `relay_steps.go:481` | `style.RenderSummary(...)` at `relay_steps.go:481`; `fmt.Println(summary)` at `relay_steps.go:482` | | ✓ exact |

### Other verified reference

- App-level OS-signal double-Ctrl+C path: design cites `relay_start.go:172`
  for the `r.RequestStop()` call. Current: `signal.Notify` block starts at
  `relay_start.go:172`, and the `r.RequestStop()` call is now at
  **`relay_start.go:187`** (block grew). **Untouched by this change** (OS
  concern, Decision 4). Drift on the RequestStop line only.

### Relay-log-only lines (NOT events — confirmed residual, Decision 3/8)

`fmt.Fprintf(log, ...)` sites remain log-only and are not part of the
vocabulary: `run_one.go` 646/668/676/782/893/908/910/921/925/1133;
`relay_steps.go` 67/117/129/146/150/161/163/172/215/366/382/470;
`handoff_only.go` 213/328.

## Archive-order caveat (task 1.3) — **status changed; surface to operator**

Task 1.3 / the design were written assuming **#4 `modularize-harness-adapters`
was landed-but-un-archived**. That premise is now **stale**: at HEAD `56f2b9d`
#4 is **already archived** (commit `2da5e06 "chore: archive
modularise-harness-adapters"`; dir
`openspec/changes/archive/2026-07-02-modularize-harness-adapters/`). So #4
archived FIRST — the exact order task 1.3 requires.

Consequence for the remaining risk: because this change archives **last**, its
`composition-root-structure` delta must contain #4's full "Presentation-neutral
relay-start seam" requirement text or archiving it would silently drop #4's
contract from the main spec. **Verified it does.** This change's delta
(`specs/composition-root-structure/spec.md`) is a proper **superset** of the
requirement now in `openspec/specs/composition-root-structure/spec.md`:

- Retains all six of #4's scenarios, including the harnessapi-bearing
  *"Executor registry feeds the runner from the composition root"* scenario
  (`app.BuildExecutors`, `harnessapi.Executor`).
- Retains the base requirement prose + the `app.InspectResume` sentence
  verbatim.
- ADDS the presentation-adapter sentence (`RelayStartOptions` carries
  `runtimeevent.Sink`/`ControlSource`; `internal/app` imports
  `internal/relay/runner/runtimeevent` types only, never
  `internal/presentation/*`) and the new scenario *"Presentation adapters pass
  through the seam opaquely."*

**Net: the archive-order risk is CLOSED, not open** — no rebase-to-merge-text
step is required. The only action for the operator is to note that the
design/tasks wording describing #4 as "un-archived" is out of date. Do not
re-open or re-resolve archive ordering.
