package main

// grandfather is the committed size baseline. It is regenerated from HEAD via
// `archguard --report` and pasted here verbatim: each cap is the file's actual
// physical line count at the time the baseline was regenerated, so the clean
// tree is green by construction (every over-budget file is grandfathered at its
// current size, and every other file is under its hard budget).
//
// A file in this map is exempt from the standard hard budget (800 production /
// 1000 test) but FAILS if it grows above its recorded cap. A file NOT in this
// map fails if it exceeds the standard hard budget — that is how a new oversize
// file is caught. Ratchet a cap down, never up; drop an entry once the tree has
// shrunk the file below its standard hard budget.
var grandfather = map[string]int{
	"internal/agent/agent_test.go":                           2812,
	"internal/agent/opencode.go":                             801,
	"internal/config/config_v2_test.go":                      1801,
	"internal/relay/resilience_test.go":                      1063,
	"internal/relay/runner/relay_steps_test.go":              2238,
	"internal/relay/runner/route_runtime_test.go":            1392,
	"internal/relay/runner/run_one.go":                       1510,
	"internal/relay/runner/run_one_test.go":                  2355,
	"internal/relay/runner/runner_failure_telemetry_test.go": 2331,
	"internal/relay/runner/runner_outcome_test.go":           1038,
	"internal/store/store_test.go":                           1112,
}
