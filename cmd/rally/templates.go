package main

// repoConfigTemplate is the comments-only repo-level config written by
// `rally init`. The active configuration lives in the user-level file; this file
// documents the knobs and carries per-repo overrides only.
const repoConfigTemplate = `# Rally repo-level config — OVERRIDES ONLY.
#
# Your main rally configuration lives in the user-level file:
#   ~/.config/rally/config.toml   (or $XDG_CONFIG_HOME/rally/config.toml)
#
# Rally loads that user file first, then applies anything set HERE on top of it
# (per key: a value here wins over the same key in the user file). Leave a
# setting commented out to inherit it from the user file. Use this file only for
# settings that should differ in THIS repository.
#
# Uncomment and edit any of the examples below to override them for this repo:
#
# schema_version = 2
#
# [defaults]
# iterations = 5
# mix = "cc cx"
#
# [routes]
# # Map a role to an ordered runner fallback list (first that works wins).
# junior = ["op:zai", "cc:sonnet"]
# senior = ["cc:opus", "cx:g55"]
#
# [providers]
# # Group runners that share one usage-limit budget. When any member hits a
# # usage limit, every member is benched until the reset — so rally stops
# # retrying models that draw from the same exhausted account, even across
# # harnesses. Entries are model aliases, harness:model specs, or wildcards
# # such as codex:* and opencode-go/*.
# codex = ["g55", "g54", "opencode:openai/gpt-5.5"]
# # Use the table form to disable a whole provider (e.g. a known monthly cap,
# # or to conserve usage while another session runs a big task):
# # [providers.claude]
# # models   = ["cc:opus", "cc:sonnet"]
# # disabled = true
#
# [reliability]
# retry_budget = 5
#
# [telemetry]
# new_relic_app_name = ""
`

// userConfigSeed is the active base config written to ~/.config/rally/config.toml
// when it does not yet exist. This is the main source of truth; repo-level files
// override individual keys.
const userConfigSeed = `# Rally user-level config — the base for every repo on this machine.
# Repo-level .rally/config.toml files override individual keys set here.
# Run ` + "`rally init roles`" + ` to add default role routing.
schema_version = 2
laps_instructions = ""
run_hooks_on_autocommit = false
data_dir = ""

[defaults]
iterations = 5
mix = "cc cx"
claude_model = ""
codex_model = ""
opencode_model = ""
antigravity_model = ""

[telemetry]
# enabled = false
new_relic_app_name = ""
new_relic_host_display_name = ""
`
