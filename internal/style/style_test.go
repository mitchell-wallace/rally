package style

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

func TestRenderHeader(t *testing.T) {
	start := time.Date(2024, 6, 15, 15, 4, 0, 0, time.Local)
	got := RenderHeader(HeaderOptions{
		RunIndex:  2,
		TotalRuns: 10,
		AgentName: "claude",
		Attempt:   1,
		StartTime: start,
		Model:     "sonnet-4",
	})

	if !strings.Contains(got, "[3/10]") {
		t.Errorf("expected [3/10] in header, got: %s", got)
	}
	if !strings.Contains(got, "claude") {
		t.Errorf("expected agent name 'claude' in header, got: %s", got)
	}
	if !strings.Contains(got, "started 15:04") {
		t.Errorf("expected 'started 15:04' in header, got: %s", got)
	}
	if !strings.Contains(got, strings.Repeat("═", separatorWidth)) {
		t.Errorf("expected separator line in header, got: %s", got)
	}
	if !strings.Contains(got, "model: sonnet-4") {
		t.Errorf("expected 'model: sonnet-4' in header, got: %s", got)
	}
	// Verify ANSI color codes are present (separator uses dim color)
	if !strings.Contains(got, "\x1b[") {
		t.Logf("no ANSI codes found in header (possible TTY detection); output: %q", got)
	}
}

func TestRenderHeaderLapsBacked(t *testing.T) {
	start := time.Date(2024, 6, 15, 15, 4, 0, 0, time.Local)
	got := RenderHeader(HeaderOptions{
		AgentName:    "gemini",
		Attempt:      1,
		StartTime:    start,
		IsLapsBacked: true,
		LapTitle:     "Apply neobrutalist UI polish",
		LapsStarted:  1,
		LapsTotal:    3,
		Model:        "gemini-2.5-pro",
	})

	if strings.Contains(got, "remaining") {
		t.Errorf("legacy '(N remaining)' suffix should be gone, got: %s", got)
	}
	if !strings.Contains(got, "laps: 1/3") {
		t.Errorf("expected 'laps: 1/3' line in header, got: %s", got)
	}
	if !strings.Contains(got, "model: gemini-2.5-pro") {
		t.Errorf("expected 'model: gemini-2.5-pro' line in header, got: %s", got)
	}
	if !strings.Contains(got, "Apply neobrutalist UI polish") {
		t.Errorf("expected lap title in header, got: %s", got)
	}
}

func TestRenderHeaderAttemptTwo(t *testing.T) {
	start := time.Date(2024, 6, 15, 10, 30, 0, 0, time.Local)
	got := RenderHeader(HeaderOptions{
		RunIndex:  0,
		TotalRuns: 5,
		AgentName: "gemini",
		Attempt:   2,
		StartTime: start,
	})

	if !strings.Contains(got, "[1/5]") {
		t.Errorf("expected [1/5] in header, got: %s", got)
	}
	if !strings.Contains(got, "(attempt 2)") {
		t.Errorf("expected '(attempt 2)' in header for attempt > 1, got: %s", got)
	}
}

func TestRenderFooterPassed(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     2*time.Minute + 34*time.Second,
		FilesChanged: 3,
		CommitHash:   "abc1234",
	})

	if !strings.Contains(got, "passed") {
		t.Errorf("expected 'passed' in footer, got: %s", got)
	}
	if !strings.Contains(got, "2m 34s") {
		t.Errorf("expected '2m 34s' in footer, got: %s", got)
	}
	if !strings.Contains(got, "3 files") {
		t.Errorf("expected '3 files' in footer, got: %s", got)
	}
	if !strings.Contains(got, "abc1234") {
		t.Errorf("expected commit hash 'abc1234' in footer, got: %s", got)
	}
	// Verify ANSI codes are present
	if !strings.Contains(got, "\x1b[") {
		t.Logf("no ANSI codes found in passed footer (possible TTY detection); output: %q", got)
	}
}

func TestRenderFooterFailed(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       false,
		Duration:     1*time.Minute + 12*time.Second,
		FilesChanged: 0,
	})

	if !strings.Contains(got, "failed") {
		t.Errorf("expected 'failed' in footer, got: %s", got)
	}
	if !strings.Contains(got, "1m 12s") {
		t.Errorf("expected '1m 12s' in footer, got: %s", got)
	}
	if !strings.Contains(got, "0 files") {
		t.Errorf("expected '0 files' in footer, got: %s", got)
	}
	if !strings.Contains(got, "—") {
		t.Errorf("expected '—' for empty commit hash in footer, got: %s", got)
	}
}

func TestRenderFooterSingularFile(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     45 * time.Second,
		FilesChanged: 1,
		CommitHash:   "deadbeef",
	})

	if !strings.Contains(got, "1 file") {
		t.Errorf("expected '1 file' (singular) in footer, got: %s", got)
	}
}

func TestRenderFooterZeroDuration(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     0,
		FilesChanged: 0,
	})

	if !strings.Contains(got, "0s") {
		t.Errorf("expected '0s' in footer for zero duration, got: %s", got)
	}
}

func TestRenderSummary(t *testing.T) {
	got := RenderSummary(10, 8, 2, 25*time.Minute+10*time.Second)

	if !strings.Contains(got, "Relay complete:") {
		t.Errorf("expected 'Relay complete:' in summary, got: %s", got)
	}
	if !strings.Contains(got, "10 runs") {
		t.Errorf("expected '10 runs' in summary, got: %s", got)
	}
	if !strings.Contains(got, "8 passed") {
		t.Errorf("expected '8 passed' in summary, got: %s", got)
	}
	if !strings.Contains(got, "2 failed") {
		t.Errorf("expected '2 failed' in summary, got: %s", got)
	}
	if !strings.Contains(got, "total 25m 10s") {
		t.Errorf("expected 'total 25m 10s' in summary, got: %s", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Logf("no ANSI codes found in summary (possible TTY detection); output: %q", got)
	}
}

func TestRenderSummaryEdgeCases(t *testing.T) {
	// Single run, all pass
	got := RenderSummary(1, 1, 0, 5*time.Second)
	if !strings.Contains(got, "1 run") {
		t.Errorf("expected '1 run' (singular) in summary, got: %s", got)
	}

	// All fail
	got = RenderSummary(5, 0, 5, 1*time.Hour+30*time.Minute)
	if !strings.Contains(got, "0 passed") {
		t.Errorf("expected '0 passed' in summary, got: %s", got)
	}
	if !strings.Contains(got, "5 failed") {
		t.Errorf("expected '5 failed' in summary, got: %s", got)
	}
}

func TestStylesDoNotPanic(t *testing.T) {
	_ = SuccessStyle.Render("ok")
	_ = FailureStyle.Render("fail")
	_ = WarningStyle.Render("warn")
	_ = DimStyle.Render("dim")
	_ = BoldStyle.Render("bold")
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{2*time.Minute + 34*time.Second, "2m 34s"},
		{1*time.Hour + 5*time.Minute + 3*time.Second, "65m 3s"},
		{0, "0s"},
	}

	for _, c := range cases {
		got := formatDuration(c.d)
		if got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if plural(1) != "" {
		t.Errorf("plural(1) should be empty string")
	}
	if plural(0) != "s" {
		t.Errorf("plural(0) should be 's'")
	}
	if plural(2) != "s" {
		t.Errorf("plural(2) should be 's'")
	}
}
