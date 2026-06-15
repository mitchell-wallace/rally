package telemetry

import (
	"path/filepath"
	"testing"
)

const testMachineID = "0123456789abcdef0123456789abcdef"

func baseRallyContext() RallyContext {
	return RallyContext{
		RelayID:        7,
		RelayStartedAt: "2026-06-11T08:30:00Z",
		Repo:           "repo-abc123",
		RepoName:       "project",
		MachineID:      testMachineID,
		Cwd:            "/srv/work/project",
	}
}

func TestMachineIDPrefix(t *testing.T) {
	if got := MachineIDPrefix(testMachineID); got != "0123456789ab" {
		t.Errorf("MachineIDPrefix(full) = %q, want %q", got, "0123456789ab")
	}
	if got := MachineIDPrefix("abc"); got != "abc" {
		t.Errorf("MachineIDPrefix(short) = %q, want %q", got, "abc")
	}
	if got := MachineIDPrefix(""); got != "" {
		t.Errorf("MachineIDPrefix(empty) = %q, want empty", got)
	}
}

func TestRelayGUID_Stable(t *testing.T) {
	rc := baseRallyContext()
	first := RelayGUID(rc)
	if first != RelayGUID(rc) {
		t.Fatal("RelayGUID is not deterministic for identical input")
	}
	want := "0123456789ab-repo-abc123-20260611-7"
	if first != want {
		t.Errorf("RelayGUID = %q, want %q", first, want)
	}
}

func TestRelayGUID_DiffersAcrossMachineRepoDate(t *testing.T) {
	base := baseRallyContext()
	baseGUID := RelayGUID(base)

	otherMachine := base
	otherMachine.MachineID = "fedcba9876543210fedcba9876543210"
	if RelayGUID(otherMachine) == baseGUID {
		t.Error("RelayGUID must differ across machine ids")
	}

	otherRepo := base
	otherRepo.Repo = "repo-zzz999"
	if RelayGUID(otherRepo) == baseGUID {
		t.Error("RelayGUID must differ across repo keys")
	}

	otherDate := base
	otherDate.RelayStartedAt = "2026-06-12T08:30:00Z"
	if RelayGUID(otherDate) == baseGUID {
		t.Error("RelayGUID must differ across dates")
	}

	otherRelay := base
	otherRelay.RelayID = 8
	if RelayGUID(otherRelay) == baseGUID {
		t.Error("RelayGUID must differ across relay ids")
	}
}

func TestRelayGUID_UTCDateNormalization(t *testing.T) {
	// Same instant expressed in different zones must yield the same UTC date,
	// so the guid is stable regardless of the originating offset.
	utc := baseRallyContext()
	utc.RelayStartedAt = "2026-06-11T01:00:00Z"
	offset := baseRallyContext()
	offset.RelayStartedAt = "2026-06-11T03:00:00+02:00"
	if RelayGUID(utc) != RelayGUID(offset) {
		t.Errorf("RelayGUID differs for equivalent UTC instants: %q vs %q", RelayGUID(utc), RelayGUID(offset))
	}
}

func TestRelayGUID_UnparseableDateIsWellFormed(t *testing.T) {
	rc := baseRallyContext()
	rc.RelayStartedAt = "not-a-timestamp"
	if got := RelayGUID(rc); got != "0123456789ab-repo-abc123-00000000-7" {
		t.Errorf("RelayGUID with bad date = %q, want zero-date form", got)
	}
}

func TestMachineTags_IdentityTags(t *testing.T) {
	tags := MachineTags(baseRallyContext())

	if got := tags["relay_guid"]; got != "0123456789ab-repo-abc123-20260611-7" {
		t.Errorf("relay_guid tag = %q", got)
	}
	if got := tags["machine_id_prefix"]; got != "0123456789ab" {
		t.Errorf("machine_id_prefix tag = %q, want prefix", got)
	}
	if got := tags["relay_started_at"]; got != "2026-06-11T08:30:00Z" {
		t.Errorf("relay_started_at tag = %q", got)
	}
	// The full machine id must never be emitted as a tag.
	for k, v := range tags {
		if v == testMachineID {
			t.Errorf("full machine id leaked into tag %q", k)
		}
	}
	if _, found := tags["machine_id"]; found {
		t.Error("machine_id must not be a tag")
	}
}

func TestMachineTags_NoMachineIDOmitsGUIDAndPrefix(t *testing.T) {
	rc := baseRallyContext()
	rc.MachineID = ""
	tags := MachineTags(rc)

	if _, found := tags["relay_guid"]; found {
		t.Error("relay_guid must be omitted when machine id is empty")
	}
	if _, found := tags["machine_id_prefix"]; found {
		t.Error("machine_id_prefix must be omitted when machine id is empty")
	}
	// relay_started_at is independent of machine identity.
	if got := tags["relay_started_at"]; got != "2026-06-11T08:30:00Z" {
		t.Errorf("relay_started_at tag = %q, want it present without a machine id", got)
	}
}

func TestRallyContextBlock_MachineIDOnlyInContext(t *testing.T) {
	block := RallyContextBlock(baseRallyContext())

	if got := block["machine_id"]; got != testMachineID {
		t.Errorf("rally context machine_id = %v, want full id", got)
	}
	if got := block["repo_name"]; got != "project" {
		t.Errorf("rally context repo_name = %v, want project", got)
	}
	// Environment fields must be carried through.
	for _, key := range []string{"version", "go_os", "go_arch", "term"} {
		if _, ok := block[key]; !ok {
			t.Errorf("rally context block missing %q", key)
		}
	}
}

func TestRallyContextBlock_CwdHomeCollapsed(t *testing.T) {
	prev := homeDir
	homeDir = filepath.FromSlash("/home/alice")
	defer func() { homeDir = prev }()

	rc := baseRallyContext()
	rc.Cwd = filepath.FromSlash("/home/alice/code/rally")
	block := RallyContextBlock(rc)

	want := "~" + filepath.FromSlash("/code/rally")
	if got := block["cwd"]; got != want {
		t.Errorf("rally context cwd = %v, want %q (home collapsed)", got, want)
	}
}

func TestRallyContextBlock_EmptyMachineAndCwdOmitted(t *testing.T) {
	rc := RallyContext{RelayID: 1, RelayStartedAt: "2026-06-11T08:30:00Z"}
	block := RallyContextBlock(rc)

	if _, found := block["machine_id"]; found {
		t.Error("machine_id must be omitted from context when empty")
	}
	if _, found := block["cwd"]; found {
		t.Error("cwd must be omitted from context when empty")
	}
}

func TestTags_IncludesRepoName(t *testing.T) {
	tags := Tags(EventInfo{
		RelayID:  1,
		Repo:     "rally-2-abcd",
		RepoName: "rally",
	})

	if got := tags["repo"]; got != "rally-2-abcd" {
		t.Errorf("repo tag = %q, want path-hash key", got)
	}
	if got := tags["repo_name"]; got != "rally" {
		t.Errorf("repo_name tag = %q, want remote display name", got)
	}
}

func TestFailureFingerprint_StableGroupingFields(t *testing.T) {
	tags := Tags(EventInfo{
		RelayID:  1,
		RunID:    2,
		TryID:    3,
		Harness:  "opencode",
		Model:    "anthropic/claude-sonnet-4",
		Repo:     "rally-2-abcd",
		RepoName: "rally",
		LapID:    "lap-123",
	})
	tags["failure_category"] = "short_rate_limit"

	got := FailureFingerprint(tags)
	want := []string{"rally", "failure", "rally", "short_rate_limit", "opencode:anthropic/claude-sonnet-4"}
	if len(got) != len(want) {
		t.Fatalf("fingerprint length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fingerprint[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	changedAttempt := Tags(EventInfo{
		RelayID:  9,
		RunID:    9,
		TryID:    9,
		Harness:  "opencode",
		Model:    "anthropic/claude-sonnet-4",
		Repo:     "rally-2-abcd",
		RepoName: "rally",
		LapID:    "different-lap",
	})
	changedAttempt["failure_category"] = "short_rate_limit"
	if got2 := FailureFingerprint(changedAttempt); len(got2) != len(got) {
		t.Fatalf("fingerprint changed length: %v", got2)
	} else {
		for i := range got {
			if got2[i] != got[i] {
				t.Fatalf("fingerprint must ignore relay/run/try/lap ids: %v vs %v", got2, got)
			}
		}
	}
}

func TestFailureFingerprint_CategorySeparatesIssues(t *testing.T) {
	tags := map[string]string{"repo_name": "rally", "runner": "opencode:model", "failure_category": "short_rate_limit"}
	shortLimit := FailureFingerprint(tags)
	tags["failure_category"] = "usage_limit"
	usageLimit := FailureFingerprint(tags)

	if shortLimit[3] == usageLimit[3] {
		t.Fatalf("category component did not separate fingerprints: %v vs %v", shortLimit, usageLimit)
	}
}
