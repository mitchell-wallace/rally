## 1. Accepted TUI Baseline

- [x] 1.1 Implement and select the tabbed Bubble Tea TUI over the runtime presentation seam
- [x] 1.2 Provide Dashboard, Transcript, Agents, and Laps tabs plus workspace-free demo and historical view modes
- [x] 1.3 Preserve arm/confirm runtime controls and enforce presentation boundaries with archguard

## 2. Configuration Persistence Seam

- [x] 2.1 Add atomic targeted TOML mutation helpers for route, reasoning, and provider disabled keys
- [x] 2.2 Cover comment preservation, multiline/quoted values, concise provider conversion, validation failure, and file mode in config tests
- [x] 2.3 Add an app-layer machine-config snapshot/mutation service with built-in/custom roles, configured runner shorthands, and provider member counts
- [x] 2.4 Add app service tests and update archguard policy for the new app-to-role-catalog dependency

## 3. TUI Config Surface

- [x] 3.1 Add presentation-neutral config snapshot/mutation view models and demo fixtures to tuicore
- [x] 3.2 Add a Config tab showing the machine path, role routes/reasoning, and provider state
- [x] 3.3 Implement route entry add/remove/reorder and custom role creation interactions
- [x] 3.4 Implement reasoning effort selection and confirmed provider enable/disable interactions
- [x] 3.5 Make confirmation modes capture exact targets and consume navigation/unrelated keys; add model-level regression tests
- [x] 3.6 Wire async app service loading/mutations through the CLI for demo, historical, and live TUI modes

## 4. Verification and Handoff

- [x] 4.1 Run gofmt, `go build ./...`, `go vet ./...`, `go test ./...`, and `go run ./tools/archguard` for each reviewable commit
- [x] 4.2 Build `./bin/rally` without modifying global binaries
- [x] 4.3 Drive live tmux verification for route edit/reorder/add/remove, reasoning effort, provider toggle, confirmation key isolation, persistence, routes check, and relaunch
- [x] 4.4 Record SHAs, decisions, transcript evidence, gaps, and Rally friction in `tmp/tui-session-2026-07-10.md`
- [x] 4.5 Leave a clean dev worktree and push dev without touching staging, main, VERSION, or tags
