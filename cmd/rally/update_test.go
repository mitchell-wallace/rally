package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/release"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()

	os.Stdout = wOut
	os.Stderr = wErr

	outChan := make(chan string)
	errChan := make(chan string)

	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, rOut)
		outChan <- buf.String()
	}()

	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, rErr)
		errChan <- buf.String()
	}()

	fn()

	_ = wOut.Close()
	_ = wErr.Close()

	return <-outChan, <-errChan
}

func TestUpdateCommandWiring_LapsAlreadyUpToDate(t *testing.T) {
	oldUpdateCurrentBinary := release.UpdateCurrentBinary
	oldUpdateTool := release.UpdateTool
	oldInstalledVersion := laps.CompanionVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.CompanionVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = true
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "0.8.2", true, nil
	}

	// Mock laps.CompanionVersion: returns "0.1.0", true
	laps.CompanionVersion = func() (string, bool) {
		return "0.1.0", true
	}

	// Mock release.UpdateTool: returns updated = false for Laps (already up to date)
	release.UpdateTool = func(tool release.Tool, currentVersion, destination string) (string, string, bool, error) {
		if tool.BinaryName == "laps" {
			return "0.1.0", "0.1.0", false, nil
		}
		return "", "", false, fmt.Errorf("unexpected tool: %s", tool.BinaryName)
	}

	stdout, stderr := captureOutput(t, func() {
		err := updateCmd.RunE(updateCmd, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Updated rally from dev to 0.8.2") {
		t.Errorf("expected stdout to mention rally update, got: %q", stdout)
	}
	if !strings.Contains(stdout, "laps is already up to date (0.1.0)") {
		t.Errorf("expected stdout to mention laps already up to date, got: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected stderr to be empty, got: %q", stderr)
	}
}

func TestUpdateCommandDoesNotInitializeTelemetryWithBakedNewRelicLicense(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RALLY_TELEMETRY", "")
	t.Setenv("NEW_RELIC_LICENSE_KEY", "")

	oldUpdateCurrentBinary := release.UpdateCurrentBinary
	oldUpdateTool := release.UpdateTool
	oldInstalledVersion := laps.CompanionVersion
	prevDefaultLicense := DefaultNewRelicLicenseKey
	prevTelemetry := activeTelemetry
	prevMachineID := activeMachineID
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.CompanionVersion = oldInstalledVersion
		DefaultNewRelicLicenseKey = prevDefaultLicense
		activeTelemetry = prevTelemetry
		activeMachineID = prevMachineID
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	}()

	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "dev", false, nil
	}
	laps.CompanionVersion = func() (string, bool) {
		return "0.1.0", true
	}
	release.UpdateTool = func(tool release.Tool, currentVersion, destination string) (string, string, bool, error) {
		return currentVersion, currentVersion, false, nil
	}

	DefaultNewRelicLicenseKey = "0123456789012345678901234567890123456789"
	activeTelemetry = telemetry.NoopSink{}
	activeMachineID = ""

	rootCmd.SetArgs([]string{"update"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	captureOutput(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("update command failed: %v", err)
		}
	})

	machineIDPath := filepath.Join(home, ".local", "share", "rally", "machine-id")
	if _, err := os.Stat(machineIDPath); !os.IsNotExist(err) {
		t.Fatalf("update command must not create machine-id file, stat err=%v", err)
	}
	if activeMachineID != "" {
		t.Fatalf("update command initialized activeMachineID = %q", activeMachineID)
	}
}

func TestUpdateCommandWiring_LapsNotInstalled(t *testing.T) {
	oldUpdateCurrentBinary := release.UpdateCurrentBinary
	oldUpdateTool := release.UpdateTool
	oldInstalledVersion := laps.CompanionVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.CompanionVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = false (already up to date)
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "dev", false, nil
	}

	// Mock laps.CompanionVersion: returns "", false (not installed)
	laps.CompanionVersion = func() (string, bool) {
		return "", false
	}

	// Mock release.UpdateTool: installs laps
	release.UpdateTool = func(tool release.Tool, currentVersion, destination string) (string, string, bool, error) {
		if tool.BinaryName == "laps" {
			return "", "0.1.0", true, nil
		}
		return "", "", false, fmt.Errorf("unexpected tool: %s", tool.BinaryName)
	}

	stdout, stderr := captureOutput(t, func() {
		err := updateCmd.RunE(updateCmd, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "rally is already up to date (dev)") {
		t.Errorf("expected stdout to mention rally is up to date, got: %q", stdout)
	}
	if !strings.Contains(stdout, "Installed laps 0.1.0") {
		t.Errorf("expected stdout to mention laps installed, got: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected stderr to be empty, got: %q", stderr)
	}
}

func TestUpdateCommandWiring_LapsUpdateFailsNonFatally(t *testing.T) {
	oldUpdateCurrentBinary := release.UpdateCurrentBinary
	oldUpdateTool := release.UpdateTool
	oldInstalledVersion := laps.CompanionVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.CompanionVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = true
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "0.8.2", true, nil
	}

	// Mock laps.CompanionVersion: returns "0.1.0", true
	laps.CompanionVersion = func() (string, bool) {
		return "0.1.0", true
	}

	// Mock release.UpdateTool: returns error for Laps
	release.UpdateTool = func(tool release.Tool, currentVersion, destination string) (string, string, bool, error) {
		if tool.BinaryName == "laps" {
			return "", "", false, fmt.Errorf("network failure")
		}
		return "", "", false, fmt.Errorf("unexpected tool: %s", tool.BinaryName)
	}

	stdout, stderr := captureOutput(t, func() {
		err := updateCmd.RunE(updateCmd, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if !strings.Contains(stdout, "Updated rally from dev to 0.8.2") {
		t.Errorf("expected stdout to mention rally update, got: %q", stdout)
	}
	if !strings.Contains(stderr, "warning: could not update laps: network failure") {
		t.Errorf("expected stderr to mention warning, got: %q", stderr)
	}
}
