package telemetry

import (
	"fmt"
	"strconv"
	"time"
)

// EventInfo carries the correlation identifiers attached to every telemetry
// event so events are filterable and correlate with the local summary.jsonl
// digest.
type EventInfo struct {
	RelayID  int
	RunID    int
	TryID    int
	Role     string
	Harness  string
	Model    string
	Repo     string
	RepoName string
	LapID    string
}

// RunnerLabel renders the harness+model runner identity used by the `runner`
// tag. The model is omitted when empty.
func RunnerLabel(harness, model string) string {
	if model == "" {
		return harness
	}
	if harness == "" {
		return model
	}
	return harness + ":" + model
}

// Tags builds the standard correlation tag map. Empty/zero values are omitted
// so filters aren't polluted with blanks.
func Tags(info EventInfo) map[string]string {
	tags := make(map[string]string, 7)
	if info.RelayID != 0 {
		tags["relay_id"] = strconv.Itoa(info.RelayID)
	}
	if info.RunID != 0 {
		tags["run_id"] = strconv.Itoa(info.RunID)
	}
	if info.TryID != 0 {
		tags["try_id"] = strconv.Itoa(info.TryID)
	}
	if info.Role != "" {
		tags["role"] = info.Role
	}
	if runner := RunnerLabel(info.Harness, info.Model); runner != "" {
		tags["runner"] = runner
	}
	if info.Repo != "" {
		tags["repo"] = info.Repo
	}
	if info.RepoName != "" {
		tags["repo_name"] = info.RepoName
	}
	if info.LapID != "" {
		tags["lap_id"] = info.LapID
	}
	return tags
}

// FailureFingerprint returns explicit grouping keys for operator-worthy
// failures. It intentionally excludes relay/run/try/lap ids and free-text
// messages, which would fragment identical failures across attempts. The repo
// display name is preferred over the path-hash key so dev checkout names such
// as "rally-2" do not leak into grouping when the Git remote says "rally".
func FailureFingerprint(tags map[string]string) []string {
	category := tags["failure_category"]
	if category == "" {
		category = "unknown"
	}
	runner := tags["runner"]
	if runner == "" {
		runner = "unknown"
	}
	repo := tags["repo_name"]
	if repo == "" {
		repo = tags["repo"]
	}
	if repo == "" {
		repo = "unknown"
	}
	return []string{"rally", "failure", repo, category, runner}
}

// machineIDPrefixLen is the number of leading hex chars of the full anonymous
// machine id used as the low-cardinality `machine_id_prefix` tag and as the
// machine component of relay_guid. The full id stays context-only to keep
// Sentry tag cardinality bounded.
const machineIDPrefixLen = 12

// RallyContext carries the relay-level identity and run environment used to
// build the `rally` context block and the machine-identity tags. The same
// values are attached to the relay span and to every captured failure so an
// event can be correlated across machines, repos, and dates.
type RallyContext struct {
	RelayID        int
	RelayStartedAt string // relay StartedAt (RFC3339); also emitted as relay_started_at
	Repo           string // existing repo path-hash key (also the `repo` tag)
	RepoName       string // stable display name from Git remote or checkout basename
	MachineID      string // full anonymous machine id — context-only, never a tag
	Cwd            string // working directory; home prefix collapsed before send
}

// MachineIDPrefix returns the low-cardinality grouping prefix of the full
// anonymous machine id. It returns the whole id when shorter than the prefix
// length, and "" for an empty id.
func MachineIDPrefix(machineID string) string {
	if len(machineID) <= machineIDPrefixLen {
		return machineID
	}
	return machineID[:machineIDPrefixLen]
}

// RelayGUID composes the globally-unique relay identifier
//
//	<machine-id-prefix>-<repo-key>-<YYYYMMDD>-<relay_id>
//
// from the machine id, the existing repo key, the relay start date, and the
// local relay id. The machine prefix plus repo key plus date make it unique
// across machines, repos, and days while staying human-greppable. The date is
// parsed from RelayStartedAt (RFC3339); an unparseable timestamp yields a
// stable zero date so the guid is always well-formed.
func RelayGUID(rc RallyContext) string {
	return fmt.Sprintf("%s-%s-%s-%d", MachineIDPrefix(rc.MachineID), rc.Repo, relayDate(rc.RelayStartedAt), rc.RelayID)
}

func relayDate(startedAt string) string {
	if t, err := time.Parse(time.RFC3339, startedAt); err == nil {
		return t.UTC().Format("20060102")
	}
	return "00000000"
}

// MachineTags returns the machine-identity and globally-unique relay tags:
// relay_guid, relay_started_at, and the low-cardinality machine_id_prefix. The
// full machine id is intentionally excluded — it belongs in the rally context
// only. relay_guid and machine_id_prefix are emitted only when a machine id is
// present (telemetry active); relay_started_at whenever it is known.
func MachineTags(rc RallyContext) map[string]string {
	tags := make(map[string]string, 3)
	if rc.MachineID != "" {
		tags["relay_guid"] = RelayGUID(rc)
		tags["machine_id_prefix"] = MachineIDPrefix(rc.MachineID)
	}
	if rc.RelayStartedAt != "" {
		tags["relay_started_at"] = rc.RelayStartedAt
	}
	return tags
}

// RallyContextBlock builds the `rally` context block: the process environment
// (version/os/arch/term) plus the full anonymous machine id and the
// home-collapsed working directory. The full machine id lives here and never in
// a tag; cwd is run through the home-prefix collapse so the username is not
// transmitted.
func RallyContextBlock(rc RallyContext) map[string]interface{} {
	ctx := EnvironmentContext()
	if rc.MachineID != "" {
		ctx["machine_id"] = rc.MachineID
	}
	if rc.Cwd != "" {
		ctx["cwd"] = collapseHomePaths(rc.Cwd)
	}
	if rc.RepoName != "" {
		ctx["repo_name"] = rc.RepoName
	}
	return ctx
}
