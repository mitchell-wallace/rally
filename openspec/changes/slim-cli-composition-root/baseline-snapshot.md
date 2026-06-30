# Baseline Snapshot — `slim-cli-composition-root`

Pre-change reference captured for tasks **1.1–1.5**. The later VERIFY laps
(tasks 7.x) diff post-change state against **this** file, so it must be
faithful and complete. Captured on commit `d2f5edb` (`chore: archive
decompose-relay-runner`).

This lap modifies **no production code**. It only records state.

---

## 1.1 — Baseline test command (must be green)

Command (verbatim from task 1.1):

```
go test -count=1 ./cmd/rally ./internal/cli ./internal/config ./internal/app ./internal/gitx
```

Result: **GREEN** (captured at HEAD `d2f5edb`).

```
ok      github.com/mitchell-wallace/rally/cmd/rally        3.059s
ok      github.com/mitchell-wallace/rally/internal/cli     0.036s
ok      github.com/mitchell-wallace/rally/internal/config  0.054s
?       github.com/mitchell-wallace/rally/internal/app     [no test files]
ok      github.com/mitchell-wallace/rally/internal/gitx    0.739s
```

Note: `internal/app` currently has **no test files**. Any post-change app
package must keep this baseline green and (per task 3.7/7.2) gains new
`StartRelay` tests — those are additive, not part of the baseline.

If a later lap re-runs 1.1 and finds red on a tree this snapshot recorded as
green, that is a regression introduced by the change, **not** a pre-existing
failure. Do not fold unrelated fixes.

---

## 1.2 — `internal/config` exported surface

The Phase-1 file split (task 2.2) must be **surface-preserving**: `go doc
./internal/config` is identical before and after, differing only in which
source file a declaration lives in. This is the contract task 2.3 / 7.4 audits.

### Exported identifiers (`go doc ./internal/config`, verbatim)

```
package config // import "github.com/mitchell-wallace/rally/internal/config"

const DefaultRunTimeoutSecs = 4500 ...
const ExpectedSchemaVersion = 2
func RemovedGeminiAlias(err error) (string, bool)
func RemovedGeminiAliasWarning(role, routeEntry, alias string) string
func SaveV2(workspaceDir string, cfg V2Config) error
func SaveV2File(path string, cfg V2Config) error
func V2Path(workspaceDir string) string
func ValidateRoutesTable(routes map[string][]string) error
type DefaultsConfig struct{ ... }
type FallbackConfig = FreeRunConfig
type FreeRunConfig struct{ ... }
type HarnessConfig struct{ ... }
type LapsConfig struct{ ... }
type ProviderConfig struct{ ... }
type ReliabilityConfig struct{ ... }
type RemovedGeminiAliasError struct{ ... }
type TelemetryConfig struct{ ... }
type V2Config struct{ ... }
    func LoadV2(workspaceDir string) (V2Config, error)
    func LoadV2File(path string) (V2Config, error)
```

### Pre-split source-file inventory (`internal/config/`)

```
config_v2.go        <- the 993-line catch-all being split in task 2.2
config_v2_test.go
layering_test.go
main_test.go
providers.go        <- STAYS unchanged (design: providers.go unchanged)
providers_test.go
```

Target post-split files (from `design.md` "Package & file manifest"): `types.go`,
`load.go`, `decode.go`, `validate.go`, `resolve.go`, `save.go`, plus unchanged
`providers.go`. The exported-identifier set above must be identical after the
move; only the source file each decl lives in changes.

---

## 1.3 — `internal/app` surface, imports, and deps

This is the invariant base for tasks 3.8 / 7.3. The Phase-2 seam extraction
adds `StartRelay` / `InspectResume` / `BuildExecutors` to `internal/app` and
makes `app → internal/relay/runner`. **After the change, `internal/app` must
still NOT directly import `internal/user_prompt` or `internal/laps`, and the
new `app → runner` edge must not create an import cycle.** (Today's `app` is a
leaf consumed by `internal/release`; the cycle risk is
`app → runner → laps → release → app`, which Phase-2 task 3.1 pre-empts by
moving the release-facing constants out first.)

### Exported surface (`go doc ./internal/app`, verbatim)

```
package app // import "github.com/mitchell-wallace/rally/internal/app"

const BinaryName = "rally" ...
func ContainerDataDir(containerName string) string
func ContainerEnv(containerName string) map[string]string
func RepoProgressPath(workspaceDir string) string
func SessionDir(dataDir string, sessionID int) string
```

### Release-facing constants currently living in `internal/app` (must move in 3.1)

`internal/app/app.go` const block (lines 9–26):

```
BinaryName          = "rally"
EnvNoUpdateCheck    = "RALLY_NO_UPDATE_CHECK"
ReleaseOwner        = "mitchell-wallace"
ReleaseRepo         = "rally"
```

These four are the `release → app` metadata edge that task 3.1 relocates to
`internal/release` **before** `app` adds the `runner` import.

### Direct imports (`go list -f '{{.Imports}}' ./internal/app`, verbatim)

```
[github.com/mitchell-wallace/rally/internal/store path/filepath]
```

i.e. today `internal/app` directly imports **only** `internal/store` and
`path/filepath`. It does **not** directly import `internal/user_prompt` or
`internal/laps` (invariant holds at baseline).

### Deps (`go list -deps ./internal/app`, verbatim)

```
internal/goarch
unsafe
internal/abi
internal/unsafeheader
internal/cpu
internal/bytealg
internal/byteorder
internal/chacha8rand
internal/coverage/rtcov
internal/godebugs
internal/goexperiment
internal/goos
internal/profilerecord
internal/runtime/atomic
internal/runtime/syscall/linux
math/bits
internal/strconv
internal/runtime/cgroup
internal/runtime/exithook
internal/runtime/gc
internal/runtime/sys
internal/runtime/gc/scan
internal/asan
internal/msan
internal/race
internal/runtime/math
internal/runtime/maps
internal/runtime/pprof/label
internal/stringslite
internal/trace/tracev2
runtime
internal/reflectlite
errors
sync/atomic
internal/sync
internal/synctest
sync
io
iter
unicode
unicode/utf8
bytes
strings
bufio
cmp
encoding
slices
strconv
encoding/base64
math
reflect
internal/fmtsort
internal/oserror
path
internal/bisect
internal/godebug
syscall
time
io/fs
internal/filepathlite
internal/syscall/unix
internal/poll
internal/syscall/execenv
internal/testlog
os
fmt
unicode/utf16
encoding/json
context
path/filepath
os/exec
github.com/mitchell-wallace/rally/internal/monitor
sort
regexp/syntax
regexp
github.com/mitchell-wallace/rally/internal/reliability
github.com/mitchell-wallace/rally/internal/textutil
github.com/mitchell-wallace/rally/internal/store
github.com/mitchell-wallace/rally/internal/app
```

Note: deps today does **not** contain `internal/laps`, `internal/relay`, or
`internal/user_prompt`. Post-change it is expected to gain `internal/laps`
**transitively** through `runner` (allowed), but never `internal/user_prompt`.

---

## 1.4 — `cmd/rally` test/helper migration checklist

Phase 3 (tasks 4.x) moves handlers + helpers + their tests out of
`package main` into `internal/cli` (and `commitSetupFiles` → `internal/gitx`
in 3.3). **Every pre-change test below must appear exactly once after the
move.** This table is how the VERIFY lap audits that — each `(file, Test*)`
row is a migration unit; a missing or duplicated row after Phase 3 is a
misclassification.

### Source symbols and where they live at baseline

```
cmd/rally/main.go
    resolveWorkspaceDir       (158)
    telemetryConfigForRelay   (169)
    runRelay                  (194)
    commitSetupFiles          (502)
cmd/rally/init_roles.go
    syncRoleFolders           (186)
    migrateLegacyRoleFiles    (230)
    initRolesCmd / initAllCmd + hidden "init-roles" alias (init() at 289)
cmd/rally/relay_flags.go
    expandRelayFlag           (8)
    chooseRelayAgentSpecs     (22)
cmd/rally/tail.go             (runTail, tailTarget, activeTryRecorded, followFile, init())
cmd/rally/tail_highlight.go   (highlightWriter + apply* helpers)
```

### Test inventory by migration bucket

> `main_test.go` also defines `func TestMain(m *testing.M)` (line 24) — the
> package `TestMain`. If/when `package main` no longer needs it after the
> drain (task 4.4), call that out explicitly; if it stays to set package-main
> globals for ldflag/version smoke tests, note that too. It is **not** a
> command/helper test and moves only with whatever `package main` test
> scaffolding remains.

#### `commitSetupFiles` → `internal/gitx` (task 3.3)

File: `cmd/rally/main_test.go`

```
TestCommitSetupFiles_InitCreatesExactlyOneCommit            (723)
TestCommitSetupFiles_InitRerunIsNoOp                        (756)
TestCommitSetupFiles_DirtyWorkingTreeNotSwept               (781)
TestCommitSetupFiles_HookInstallCreatesCommit               (824)
TestCommitSetupFiles_HookReinstallIsNoOp                    (866)
TestCommitSetupFiles_SpecialCharMessage_DoubleQuotesAndDollars   (924)
TestCommitSetupFiles_SpecialCharMessage_SingleQuotesAndNewlines  (951)
TestCommitSetupFiles_NonGitDirIsNoOp                        (979)
TestCommitSetupFiles_CorruptedGitDirIsNoOp                  (999)
```

(9 tests — all move with `commitSetupFiles` into `internal/gitx`.)

#### `chooseRelayAgentSpecs` (+ `expandRelayFlag`) → `internal/cli/relay_flags.go` (task 4.2)

File: `cmd/rally/relay_flags_test.go`

```
TestExpandRelayFlag                          (5)
TestExpandRelayFlag_EmptyValueRejected       (22)
TestChooseRelayAgentSpecs_AgentWinsOverMix   (29)
TestChooseRelayAgentSpecs_UsesMixWhenAgentMissing   (45)
TestChooseRelayAgentSpecs_FallsBackToDefaultsMix    (67)
```

(5 tests.)

#### `syncRoleFolders` (+ `migrateLegacyRoleFiles`) → `internal/cli` (task 4.3)

File: `cmd/rally/roles_sync_test.go`

```
TestSyncRoleFolders_MigratesFlatFilesAndRegeneratesBuiltin   (14)
TestSyncRoleFolders_IdempotentAndDoesNotClobberUser          (74)
```

(2 tests.)

#### telemetry-config → `internal/cli` (task 4.2, with `telemetryConfigForRelay` mapping)

These assert the **baked-license / telemetry-activation** behaviour that
`telemetryConfigForRelay` (main.go:169) and the `RootOptions` build vars
drive. They are the telemetry-config wiring tests; `telemetry_test.go`
(separately) is telemetry *sink/event* behaviour.

File: `cmd/rally/main_test.go`

```
TestVersionCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense   (150)
TestHelpCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense      (154)
```

File: `cmd/rally/update_test.go`

```
TestUpdateCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense    (100)
```

File: `cmd/rally/telemetry_test.go` (telemetry sink/event behaviour — moves
with the relay command path; tracked here for completeness so no test is
dropped during the drain):

```
TestTelemetryIssueCriteriaAndPromptSize   (89)
TestTelemetry_PromptBreakdown             (152)
TestTelemetry_AgentClassRetry_NoIssue     (302)
TestTelemetry_InfraFailure_Issue          (345)
TestTelemetry_RelayStall_Issue            (393)
```

(5 tests. The VERIFY lap should re-derive line numbers from the post-move
tree rather than trusting these — line numbers are a navigation aid, the
`Test*` name is the migration key.)

#### `init` (+ role bootstrap, `init all`) → `internal/cli/init.go` (task 4.3)

File: `cmd/rally/main_test.go`

```
TestRunInit_WritesNewShapeConfig                 (164)
TestRunInit_DoesNotOverwriteExistingConfig       (257)
TestRunInit_UpdatesExistingGitignore             (285)
TestRunInit_UpdatesExistingGitignoreNoTrailingNewline   (320)
TestRunInitRoles_InstallsRoutesAndRoleInstructions      (355)
TestRunInitAll_RunsBothInSequence                       (413)
TestRunInitRoles_OnlyTouchesRoleConfig                  (462)
TestRunInitAll_IdempotentRerun                          (530)
TestRunInitRoles_IdempotentRerun                        (566)
```

(9 tests. These exercise both `init`, `init roles`, and `init all`.)

#### hidden `init-roles` alias (task 4.1 / 4.3)

The alias is registered in `init_roles.go` `init()` (line 289–297):

```go
rootCmd.AddCommand(&cobra.Command{
    Use:    "init-roles",
    Short:  "Alias for `rally init all`",
    Hidden: true,
    RunE:   runInitAll,
})
```

It has **no dedicated test** — it is behaviourally covered by the
`TestRunInitAll_*` tests (the alias's `RunE` is `runInitAll`, the same
handler `init all` uses). The VERIFY lap must confirm the alias still
resolves to `runInitAll` after the move into `internal/cli`, and remains
`Hidden: true`. If a dedicated `TestInitRolesAlias` is added during the
change, it is additive (not in this baseline).

#### `tail` (+ highlight helpers) → `internal/cli/tail.go` + `tail_highlight.go` (task 4.3)

File: `cmd/rally/tail_test.go`

```
TestTailLatest                               (25)
TestTailTryNValid                            (65)
TestTailTryNInvalid                          (115)
TestTailEmptyTries                           (140)
TestTailTargetReadsStateDir                  (157)
TestFollowFileGrowing                        (189)
TestTailMultiRepoSharedDataDir               (231)
TestTailHighlight                            (314)
TestTailActiveMetadata                       (364)
TestTailActiveMetadataWarnsAndFallsBack      (494)
TestTailActiveMetadataRecordedTryFallsBack   (540)
TestFallbackToNewestUncompleted              (595)
```

(12 tests.)

#### command-registration / relay command wiring → `internal/cli` (tasks 4.1 / 4.2)

These assert command construction and the `start`/`relay` `RunE` behaviour
(`runRelay`, main.go:194) that becomes `cli.start`'s handler.

File: `cmd/rally/main_test.go`

```
TestRunRelayLoadsInstructions                 (53)
TestStartCommandSilencesUsageForRuntimeErrors (158)
TestRunRelayNewResetsAgentStatus              (597)
```

File: `cmd/rally/update_test.go` (update-command wiring — moves with `update`
into `internal/cli` in 4.3):

```
TestUpdateCommandWiring_LapsAlreadyUpToDate      (54)
TestUpdateCommandWiring_LapsNotInstalled         (156)
TestUpdateCommandWiring_LapsUpdateFailsNonFatally (202)
```

#### Summary count (migration audit total)

```
commitSetupFiles bucket         :  9   (-> internal/gitx)
chooseRelayAgentSpecs bucket    :  5   (-> internal/cli)
syncRoleFolders bucket          :  2   (-> internal/cli)
telemetry-config bucket         :  3 baked + 5 sink = 8
init bucket                     :  9
init-roles alias                :  0 dedicated (covered by TestRunInitAll_*)
tail bucket                     : 12
command-registration/update     :  3 relay + 3 update = 6
                                -----
plus TestMain (package scaffolding, moves only if package main drops it)
```

The VERIFY lap should reconcile: every `func Test*` in the pre-change
`cmd/rally/*_test.go` (the full grep in this snapshot) must appear exactly
once in the post-change tree, whether it stayed in `cmd/rally` (e.g. ldflag
smoke) or moved to `internal/cli` / `internal/gitx`.

---

## 1.5 — `.goreleaser.yaml` ldflags targets

The build vars `main.Version`, `main.DefaultNewRelicLicenseKey`, and
`main.DefaultNewRelicAppName` **must remain in `package main`** after the
change (design Decision 3; tasks 4.4 / 7.5). `.goreleaser.yaml` is **not
touched** by this change.

### ldflags (`.goreleaser.yaml` line 18, verbatim)

```yaml
ldflags:
  - -s -w -X main.Version={{ .Version }} -X main.DefaultNewRelicLicenseKey={{ .Env.RALLY_NEW_RELIC_LICENSE_KEY }} -X 'main.DefaultNewRelicAppName={{ .Env.RALLY_NEW_RELIC_APP_NAME }}'
```

### The three required `main.*` targets

| GoReleaser ldflag target                  | Source          | Must stay in        |
| ----------------------------------------- | --------------- | ------------------- |
| `main.Version`                            | `{{ .Version }}` | `package main`      |
| `main.DefaultNewRelicLicenseKey`          | `RALLY_NEW_RELIC_LICENSE_KEY` env | `package main` |
| `main.DefaultNewRelicAppName`             | `RALLY_NEW_RELIC_APP_NAME` env   | `package main` |

After the change they thread `main → cli.RootOptions → app.RelayStartOptions.Telemetry`
(design Decision 3 / `TelemetryBuild`). The VERIFY lap (task 7.5) runs:

```
go build -ldflags "-X main.Version=test -X main.DefaultNewRelicLicenseKey=k -X main.DefaultNewRelicAppName=n"
```

and confirms it succeeds and `rally version` reflects the injected value. The
targets above must resolve at link time, so the three `main.*` package-level
vars cannot move out of `package main` even when the command bodies drain into
`internal/cli`.

### `.goreleaser.yaml` full file (for byte-identity audit in 7.6)

```
version: 2

project_name: rally

builds:
  - id: rally
    main: ./cmd/rally
    binary: rally
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w -X main.Version={{ .Version }} -X main.DefaultNewRelicLicenseKey={{ .Env.RALLY_NEW_RELIC_LICENSE_KEY }} -X 'main.DefaultNewRelicAppName={{ .Env.RALLY_NEW_RELIC_APP_NAME }}'

archives:
  - id: rally
    ids:
      - rally
    formats:
      - tar.gz
    name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: checksums.txt

release:
  extra_files:
    - glob: ./install.sh
```

Task 7.6 asserts this file is byte-identical pre/post change.

---

## How the VERIFY laps use this file

- **7.1 / 7.2** re-run the 1.1 command (+ `./...` and `-race -shuffle`) and
  compare against the 1.1 green baseline.
- **7.4** diffs the 1.2 exported surface against post-split `go doc
  ./internal/config` — must be identical.
- **7.3** checks the 1.3 invariants: `go list -f '{{.Imports}}' ./internal/app`
  contains neither `internal/user_prompt` nor `internal/laps`, and
  `go list -deps ./internal/app` has no cycle (allowed to gain `laps`
  transitively via `runner`).
- **Phase 3 review** walks the 1.4 checklist to confirm every pre-change
  `func Test*` appears exactly once post-move.
- **7.5 / 7.6** confirm the 1.5 ldflags targets still resolve and
  `.goreleaser.yaml` is byte-identical.
