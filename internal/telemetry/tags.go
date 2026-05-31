package telemetry

import "strconv"

// EventInfo carries the correlation identifiers attached to every telemetry
// event so events are filterable and correlate with the local summary.jsonl
// digest.
type EventInfo struct {
	RelayID int
	RunID   int
	TryID   int
	Role    string
	Harness string
	Model   string
	Repo    string
	LapID   string
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
	if info.LapID != "" {
		tags["lap_id"] = info.LapID
	}
	return tags
}
