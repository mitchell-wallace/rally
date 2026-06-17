package telemetry

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOrCreateMachineID_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()

	id := resolveOrCreateMachineID(dir)

	if !isValidMachineID(id) {
		t.Fatalf("expected valid 32-char hex id, got %q", id)
	}

	// File should exist with the ID.
	idPath := filepath.Join(dir, machineIDFile)
	data, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("machine-id file not created: %v", err)
	}

	// File content should match the returned ID (with trailing newline).
	got := string(data)
	if got != id+"\n" {
		t.Errorf("file content = %q, want %q", got, id+"\n")
	}
}

func TestResolveOrCreateMachineID_StableOnSecondRead(t *testing.T) {
	dir := t.TempDir()

	id1 := resolveOrCreateMachineID(dir)
	id2 := resolveOrCreateMachineID(dir)

	if id1 != id2 {
		t.Errorf("second read returned different id: %q vs %q", id1, id2)
	}
}

func TestResolveOrCreateMachineID_RandomNotMachineDerived(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	id1 := resolveOrCreateMachineID(dir1)
	id2 := resolveOrCreateMachineID(dir2)

	// Two independent generations should produce different values.
	// This is probabilistically true for 128-bit random values.
	if id1 == id2 {
		t.Errorf("two independent IDs should differ: both = %q", id1)
	}

	// Verify the values are valid hex-encoded 128-bit values.
	if !isValidMachineID(id1) {
		t.Errorf("id1 is not valid hex: %q", id1)
	}
	if !isValidMachineID(id2) {
		t.Errorf("id2 is not valid hex: %q", id2)
	}

	// Ensure the ID is not derived from hostname, username, or MAC.
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}

	if hostname != "" && id1 == hostname {
		t.Error("machine ID matches hostname — must be random")
	}
	if username != "" && id1 == username {
		t.Error("machine ID matches username — must be random")
	}
}

func TestResolveOrCreateMachineID_PrivatePermissions(t *testing.T) {
	dir := t.TempDir()

	_ = resolveOrCreateMachineID(dir)

	idPath := filepath.Join(dir, machineIDFile)
	info, err := os.Stat(idPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != machineIDPerm {
		t.Errorf("file permissions = %o, want %o", perm, machineIDPerm)
	}
}

func TestResolveOrCreateMachineID_EmptyDataDir(t *testing.T) {
	// Empty data dir means no persistence — should return ephemeral ID.
	id := resolveOrCreateMachineID("")

	if !isValidMachineID(id) {
		t.Fatalf("expected valid hex id for empty data dir, got %q", id)
	}

	// Each call with empty dir produces a new ephemeral value.
	id2 := resolveOrCreateMachineID("")
	// Both should be valid.
	if !isValidMachineID(id2) {
		t.Fatalf("expected valid hex id for second call, got %q", id2)
	}
}

func TestResolveOrCreateMachineID_UnwritableDir(t *testing.T) {
	dir := t.TempDir()
	unwritable := filepath.Join(dir, "readonly")

	// Create the directory, then make it read-only.
	if err := os.MkdirAll(unwritable, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		// Restore permissions so t.TempDir cleanup can delete.
		os.Chmod(unwritable, 0o755)
	})

	// Should not fail — falls back to ephemeral.
	id := resolveOrCreateMachineID(unwritable)

	if !isValidMachineID(id) {
		t.Fatalf("expected valid hex id for unwritable dir, got %q", id)
	}

	// File should not have been created.
	idPath := filepath.Join(unwritable, machineIDFile)
	if _, err := os.Stat(idPath); !os.IsNotExist(err) {
		t.Errorf("machine-id file should not exist in unwritable dir, stat err: %v", err)
	}
}

func TestResolveOrCreateMachineID_NonexistentDataDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")

	// The nested path doesn't exist yet — MkdirAll should create it.
	id := resolveOrCreateMachineID(nested)

	if !isValidMachineID(id) {
		t.Fatalf("expected valid hex id, got %q", id)
	}

	// File should have been created in the nested directory.
	idPath := filepath.Join(nested, machineIDFile)
	if _, err := os.Stat(idPath); err != nil {
		t.Fatalf("machine-id file not created in nested dir: %v", err)
	}
}

func TestResolveOrCreateMachineID_InvalidFileContent(t *testing.T) {
	dir := t.TempDir()
	idPath := filepath.Join(dir, machineIDFile)

	// Write invalid content.
	if err := os.WriteFile(idPath, []byte("not-a-valid-id\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Should regenerate a valid ID.
	id := resolveOrCreateMachineID(dir)

	if !isValidMachineID(id) {
		t.Fatalf("expected valid hex id after invalid file, got %q", id)
	}

	// File should now contain the valid ID.
	data, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != id+"\n" {
		t.Errorf("file content = %q, want %q", string(data), id+"\n")
	}
}

func TestGenerateMachineID_Format(t *testing.T) {
	id, err := generateMachineID()
	if err != nil {
		t.Fatalf("generateMachineID: %v", err)
	}

	if len(id) != machineIDBytes*2 {
		t.Errorf("id length = %d, want %d", len(id), machineIDBytes*2)
	}

	// Must be valid hex.
	decoded, err := hex.DecodeString(id)
	if err != nil {
		t.Errorf("id is not valid hex: %v", err)
	}
	if len(decoded) != machineIDBytes {
		t.Errorf("decoded length = %d, want %d", len(decoded), machineIDBytes)
	}
}

func TestIsValidMachineID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"valid 32-char hex", "0123456789abcdef0123456789abcdef", true},
		{"valid uppercase hex", "0123456789ABCDEF0123456789ABCDEF", true},
		{"too short", "0123456789abcdef", false},
		{"too long", "0123456789abcdef0123456789abcdef00", false},
		{"non-hex chars", "0123456789ghijkl0123456789ghijkl", false},
		{"empty string", "", false},
		{"just spaces", "                                ", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isValidMachineID(tc.input)
			if got != tc.want {
				t.Errorf("isValidMachineID(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestInitWithIdentity_DisabledTelemetryNoFile(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(envKillSwitch, "0")
	t.Setenv(envNewRelicLicenseKey, testNewRelicLicense)

	result := InitWithIdentity(Config{
		DefaultNewRelicLicenseKey: testNewRelicLicense,
		DataDir:                   dir,
	})
	defer result.Cleanup()

	if _, ok := result.Sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink, got %T", result.Sink)
	}
	if result.MachineID != "" {
		t.Errorf("disabled telemetry should have empty MachineID, got %q", result.MachineID)
	}

	// No machine-id file should exist.
	idPath := filepath.Join(dir, machineIDFile)
	if _, err := os.Stat(idPath); !os.IsNotExist(err) {
		t.Errorf("machine-id file should not exist when telemetry is disabled, stat err: %v", err)
	}
}

func TestInitWithIdentity_NoLicenseNoFile(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, "")

	result := InitWithIdentity(Config{DataDir: dir})
	defer result.Cleanup()

	if _, ok := result.Sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink, got %T", result.Sink)
	}
	if result.MachineID != "" {
		t.Errorf("no-license telemetry should have empty MachineID, got %q", result.MachineID)
	}

	// No machine-id file should exist.
	idPath := filepath.Join(dir, machineIDFile)
	if _, err := os.Stat(idPath); !os.IsNotExist(err) {
		t.Errorf("machine-id file should not exist when no license, stat err: %v", err)
	}
}

func TestInitWithIdentity_ActiveTelemetryCreatesMachineID(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, "")

	result := InitWithIdentity(Config{DefaultNewRelicLicenseKey: testNewRelicLicense, DataDir: dir})
	defer result.Cleanup()

	if result.MachineID == "" {
		t.Fatal("active telemetry should produce a non-empty MachineID")
	}
	if !isValidMachineID(result.MachineID) {
		t.Errorf("MachineID is not a valid 32-char hex: %q", result.MachineID)
	}

	// Machine-id file should exist.
	idPath := filepath.Join(dir, machineIDFile)
	data, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("machine-id file not created: %v", err)
	}
	if string(data) != result.MachineID+"\n" {
		t.Errorf("file content = %q, want %q", string(data), result.MachineID+"\n")
	}
}

func TestInitWithIdentity_NoDataDirEphemeral(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, "")

	result := InitWithIdentity(Config{DefaultNewRelicLicenseKey: testNewRelicLicense, DataDir: ""})
	defer result.Cleanup()

	// Should get an ephemeral MachineID.
	if result.MachineID == "" {
		t.Fatal("active telemetry with no data dir should produce ephemeral MachineID")
	}
	if !isValidMachineID(result.MachineID) {
		t.Errorf("ephemeral MachineID is not valid hex: %q", result.MachineID)
	}
}
