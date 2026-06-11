package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// machineIDFile is the name of the file storing the persistent machine identity.
	machineIDFile = "machine-id"

	// machineIDBytes is the number of random bytes (128 bits) used for the identity.
	machineIDBytes = 16

	// machineIDPerm is the file permission for the machine-id file.
	// Owner read/write only — the identity has no reason to be world-readable.
	machineIDPerm = os.FileMode(0o600)
)

// resolveOrCreateMachineID reads or creates a persistent anonymous machine
// identity at <dataDir>/machine-id. The identity is a 128-bit random hex
// string generated with crypto/rand, never derived from hostname, username,
// MAC, or any host attribute.
//
// If dataDir is empty, or the file cannot be read/created/written, the
// function returns an ephemeral per-process identity (generated once per call)
// and a nil error. It never fails: callers can always use the returned value.
//
// This function should only be called when telemetry is active (after DSN
// resolution confirms telemetry is enabled). Disabled telemetry must not
// call this — doing so is a logic error but still safe.
func resolveOrCreateMachineID(dataDir string) string {
	if dataDir == "" {
		return generateEphemeralID()
	}

	idPath := filepath.Join(dataDir, machineIDFile)

	// Try to read an existing identity first.
	if data, err := os.ReadFile(idPath); err == nil {
		id := strings.TrimSpace(string(data))
		if isValidMachineID(id) {
			return id
		}
		// Invalid content — fall through to regenerate.
	}

	// Generate a new identity.
	id, err := generateMachineID()
	if err != nil {
		return generateEphemeralID()
	}

	// Ensure the data directory exists.
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return id // use ephemeral — can't persist but still valid
	}

	// Write the identity file with restrictive permissions.
	if err := os.WriteFile(idPath, []byte(id+"\n"), machineIDPerm); err != nil {
		return id // use ephemeral — write failed but identity is valid
	}

	return id
}

// generateMachineID generates a new random 128-bit machine identity as a
// hex-encoded string.
func generateMachineID() (string, error) {
	b := make([]byte, machineIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// generateEphemeralID creates a best-effort ephemeral identity for cases
// where persistent storage is unavailable. Each call produces a new value
// (callers should cache if stability within a process is needed).
func generateEphemeralID() string {
	id, err := generateMachineID()
	if err != nil {
		// Absolute last resort — should never happen with crypto/rand.
		return "ephemeral-unknown"
	}
	return id
}

// isValidMachineID checks whether a string looks like a valid hex-encoded
// 128-bit identity (32 hex characters).
func isValidMachineID(id string) bool {
	if len(id) != machineIDBytes*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
