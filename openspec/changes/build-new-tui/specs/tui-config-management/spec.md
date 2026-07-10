## ADDED Requirements

### Requirement: Role routes are operable in the TUI
The TUI SHALL display ordered route entries for the default role, every
built-in role, and every custom role in the machine config. It SHALL allow an
operator to add a configured runner shorthand, remove an entry, reorder
entries, and create a custom role route.

#### Scenario: Add a configured runner
- **WHEN** an operator opens a role route and selects a configured shorthand from the add picker
- **THEN** the shorthand is appended to that role's ordered route and persisted

#### Scenario: Reorder route fallback priority
- **WHEN** an operator moves a route entry up or down
- **THEN** the stored route array reflects the new order

#### Scenario: Remove a route entry safely
- **WHEN** an operator arms removal for a route entry and confirms it
- **THEN** that exact entry is removed and no different cursor target is affected

#### Scenario: Create a custom role route
- **WHEN** an operator enters a valid new custom role name
- **THEN** the role appears in the route editor and accepts the same route operations as a built-in role

### Requirement: Per-role reasoning effort is operable in the TUI
The TUI SHALL display and set the machine-config reasoning value independently
for each built-in or custom role, including clearing it to the default.

#### Scenario: Set reasoning effort
- **WHEN** an operator selects a reasoning effort for a role
- **THEN** the corresponding `[reasoning]` key is persisted and shown after reload

#### Scenario: Clear reasoning effort
- **WHEN** an operator selects the default/empty reasoning choice
- **THEN** the role key is removed from `[reasoning]`

### Requirement: Providers can be enabled or disabled
The TUI SHALL show every configured provider, its resolved member count, and
its enabled/disabled state. It SHALL persist a confirmed toggle through the
provider's `disabled` value.

#### Scenario: Disable an enabled provider
- **WHEN** an operator confirms disabling a provider
- **THEN** its runners are marked disabled by the same provider index construction used for relay startup

#### Scenario: Enable a disabled provider
- **WHEN** an operator confirms enabling a provider
- **THEN** its runners are no longer marked disabled for subsequent relay startup

### Requirement: Configuration writes use machine scope
The TUI SHALL read and write these tables in the machine config path, honouring
`XDG_CONFIG_HOME`, and SHALL identify that scope and path in the UI. Repo-local
configuration SHALL remain unchanged.

#### Scenario: Edit from a repository with a repo config
- **WHEN** an operator saves a route, reasoning, or provider edit from the TUI
- **THEN** the machine config changes and `.rally/config.toml` remains byte-identical

### Requirement: Persistence preserves unrelated TOML
Each edit SHALL validate the final v2 document, write it atomically, and avoid
marshalling or reformatting unrelated config tables and comments.

#### Scenario: Edit a route in a commented config
- **WHEN** a route mutation is persisted in a machine config containing unrelated comments and custom formatting
- **THEN** unrelated bytes and comments remain unchanged and the resulting config passes `rally routes check`

#### Scenario: Disable a concise provider
- **WHEN** a provider declared as an array under `[providers]` is disabled
- **THEN** only that provider is converted to table form with its models retained and `disabled = true`

### Requirement: Confirm modes do not leak or retarget keys
While a route removal or provider toggle is awaiting confirmation, the TUI
SHALL consume navigation and unrelated command keys and SHALL retain the exact
armed target until confirmation or cancellation.

#### Scenario: Navigation pressed during confirmation
- **WHEN** an operator arms an action, presses a cursor key, and then confirms
- **THEN** the cursor key does not reach the underlying list and the action cannot apply to a different target

### Requirement: Persisted changes apply at supported lifecycle boundaries
Config changes SHALL be used by relays whose configuration is loaded after the
write. The UI SHALL NOT claim to dynamically reconfigure an in-flight relay
where no runtime replacement seam exists.

#### Scenario: Relaunch after editing
- **WHEN** an operator saves changes, quits, and launches `rally tui` again
- **THEN** the Config tab displays the saved values and subsequent relay startup uses them
