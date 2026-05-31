package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/release"
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
	oldInstalledVersion := laps.InstalledVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.InstalledVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = true
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "0.8.2", true, nil
	}

	// Mock laps.InstalledVersion: returns "0.1.0", true
	laps.InstalledVersion = func() (string, bool) {
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

func TestUpdateCommandWiring_LapsNotInstalled(t *testing.T) {
	oldUpdateCurrentBinary := release.UpdateCurrentBinary
	oldUpdateTool := release.UpdateTool
	oldInstalledVersion := laps.InstalledVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.InstalledVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = false (already up to date)
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "dev", false, nil
	}

	// Mock laps.InstalledVersion: returns "", false (not installed)
	laps.InstalledVersion = func() (string, bool) {
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
	oldInstalledVersion := laps.InstalledVersion
	defer func() {
		release.UpdateCurrentBinary = oldUpdateCurrentBinary
		release.UpdateTool = oldUpdateTool
		laps.InstalledVersion = oldInstalledVersion
	}()

	// Mock release.UpdateCurrentBinary: returns updated = true
	release.UpdateCurrentBinary = func(currentVersion, destination string) (string, string, bool, error) {
		return "dev", "0.8.2", true, nil
	}

	// Mock laps.InstalledVersion: returns "0.1.0", true
	laps.InstalledVersion = func() (string, bool) {
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
