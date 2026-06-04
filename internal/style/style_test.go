package style

import (
	"regexp"
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
	if !strings.Contains(got, strings.Repeat("═", maxSepWidth)) {
		t.Errorf("expected full-width separator line in header, got: %s", got)
	}
	if !strings.Contains(got, "model: sonnet-4") {
		t.Errorf("expected 'model: sonnet-4' in header, got: %s", got)
	}
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

	plain := stripAnsi(got)
	if !strings.Contains(plain, "passed") {
		t.Errorf("expected 'passed' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "2m 34s") {
		t.Errorf("expected '2m 34s' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "3 files") {
		t.Errorf("expected '3 files' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "abc1234") {
		t.Errorf("expected commit hash 'abc1234' in footer, got: %s", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Logf("no ANSI codes found in passed footer; output: %q", got)
	}
	if !strings.Contains(plain, strings.Repeat("─", maxSepWidth)) {
		t.Errorf("expected full-width sub-separator in footer")
	}
	if !strings.Contains(plain, strings.Repeat("═", maxSepWidth)) {
		t.Errorf("expected full-width separator in footer")
	}
}

func TestRenderFooterFailed(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       false,
		Duration:     1*time.Minute + 12*time.Second,
		FilesChanged: 0,
	})

	plain := stripAnsi(got)
	if !strings.Contains(plain, "failed") {
		t.Errorf("expected 'failed' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "1m 12s") {
		t.Errorf("expected '1m 12s' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "0 files") {
		t.Errorf("expected '0 files' in footer, got: %s", got)
	}
	if !strings.Contains(plain, "—") {
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

	plain := stripAnsi(got)
	if !strings.Contains(plain, "1 file") {
		t.Errorf("expected '1 file' (singular) in footer, got: %s", got)
	}
}

func TestRenderFooterZeroDuration(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     0,
		FilesChanged: 0,
	})

	plain := stripAnsi(got)
	if !strings.Contains(plain, "0s") {
		t.Errorf("expected '0s' in footer for zero duration, got: %s", got)
	}
}

func TestRenderFooterTerminalRecovery(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     30 * time.Second,
		FilesChanged: 2,
		CommitHash:   "abc1234",
		Attempt:      3,
		MaxAttempts:  5,
	})

	plain := stripAnsi(got)
	if !strings.Contains(plain, "passed on try 3/5") {
		t.Errorf("expected 'passed on try 3/5' in recovery footer, got: %s", plain)
	}
	// A terminal success is coloured (green), not dim-only.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI colour in terminal footer, got: %q", got)
	}
	// Terminal footers keep the three-line block.
	if !strings.Contains(plain, strings.Repeat("═", maxSepWidth)) {
		t.Errorf("expected full-width separator in terminal footer, got: %s", plain)
	}
}

func TestRenderFooterTerminalExhausted(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:      false,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     5,
		MaxAttempts: 5,
	})

	plain := stripAnsi(got)
	if !strings.Contains(plain, "failed after 5 tries") {
		t.Errorf("expected 'failed after 5 tries', got: %s", plain)
	}
	if !strings.Contains(plain, "agent error") {
		t.Errorf("expected fail reason in footer, got: %s", plain)
	}
}

func TestRenderFooterSingleAttemptStaysBare(t *testing.T) {
	// A single-attempt (maxAttempts==1) failure is terminal on its first
	// attempt: it should read "failed" with no "after N tries" suffix.
	got := stripAnsi(RenderFooter(FooterOptions{
		Passed:      false,
		Duration:    5 * time.Second,
		FailReason:  "no changes made",
		Attempt:     1,
		MaxAttempts: 1,
	}))
	if !strings.Contains(got, "failed") {
		t.Errorf("expected 'failed' in single-attempt footer, got: %s", got)
	}
	if strings.Contains(got, "after") {
		t.Errorf("single-attempt footer should not say 'after N tries', got: %s", got)
	}
}

func TestRenderFooterInterimRetryLine(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       false,
		Interim:      true,
		Duration:     12 * time.Second,
		FilesChanged: 0,
		FailReason:   "agent error",
		Attempt:      2,
		MaxAttempts:  5,
	})

	plain := stripAnsi(got)
	if !strings.Contains(plain, "↻ retrying 2/5") {
		t.Errorf("expected '↻ retrying 2/5' in interim line, got: %s", plain)
	}
	if !strings.Contains(plain, "last: agent error") {
		t.Errorf("expected last reason in interim line, got: %s", plain)
	}
	if !strings.Contains(plain, "12s") {
		t.Errorf("expected duration in interim line, got: %s", plain)
	}
	if !strings.Contains(plain, "0 files") {
		t.Errorf("expected files count in interim line, got: %s", plain)
	}
	// The interim line is a single line — no separator block.
	if strings.Contains(plain, "\n") {
		t.Errorf("interim line should be a single line, got: %q", plain)
	}
	if strings.Contains(plain, strings.Repeat("═", maxSepWidth)) {
		t.Errorf("interim line should not carry the separator block, got: %s", plain)
	}
	// Interim states are neutral/dim, never red. The whole line must be the
	// dim style — exactly what DimStyle.Render produces — not FailureStyle.
	if got != DimStyle.Render(plain) {
		t.Errorf("interim line should be dim-styled, got: %q want: %q", got, DimStyle.Render(plain))
	}
	if got == FailureStyle.Render(plain) {
		t.Errorf("interim line must not use FailureStyle (red), got: %q", got)
	}
}

func TestRenderSummary(t *testing.T) {
	got := RenderSummary(10, 8, 2, 25*time.Minute+10*time.Second)

	plain := stripAnsi(got)
	if !strings.Contains(plain, "Relay complete:") {
		t.Errorf("expected 'Relay complete:' in summary, got: %s", got)
	}
	if !strings.Contains(plain, "10 runs") {
		t.Errorf("expected '10 runs' in summary, got: %s", got)
	}
	if !strings.Contains(plain, "8 passed") {
		t.Errorf("expected '8 passed' in summary, got: %s", got)
	}
	if !strings.Contains(plain, "2 failed") {
		t.Errorf("expected '2 failed' in summary, got: %s", got)
	}
	if !strings.Contains(plain, "total 25m 10s") {
		t.Errorf("expected 'total 25m 10s' in summary, got: %s", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Logf("no ANSI codes found in summary; output: %q", got)
	}
	if !strings.Contains(plain, strings.Repeat("═", maxSepWidth)) {
		t.Errorf("expected full-width separator in summary")
	}
}

func TestRenderSummaryEdgeCases(t *testing.T) {
	got := RenderSummary(1, 1, 0, 5*time.Second)
	plain := stripAnsi(got)
	if !strings.Contains(plain, "1 run") {
		t.Errorf("expected '1 run' (singular) in summary, got: %s", got)
	}

	got = RenderSummary(5, 0, 5, 1*time.Hour+30*time.Minute)
	plain = stripAnsi(got)
	if !strings.Contains(plain, "0 passed") {
		t.Errorf("expected '0 passed' in summary, got: %s", got)
	}
	if !strings.Contains(plain, "5 failed") {
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

var ansiStripRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripAnsi(s string) string {
	return ansiStripRe.ReplaceAllString(s, "")
}

func TestShortcutHintForWidthTiers(t *testing.T) {
	full := shortcutHintTiers[0]
	medium := shortcutHintTiers[1]
	narrow := shortcutHintTiers[2]
	minimal := shortcutHintTiers[3]

	fullW := lipgloss.Width(full)
	mediumW := lipgloss.Width(medium)
	narrowW := lipgloss.Width(narrow)
	minimalW := lipgloss.Width(minimal)

	t.Logf("tier widths: full=%d medium=%d narrow=%d minimal=%d",
		fullW, mediumW, narrowW, minimalW)

	cases := []struct {
		width    int
		wantTier int
		label    string
	}{
		{120, 0, "wide terminal gets full tier"},
		{fullW, 0, "exact full width fits full tier"},
		{fullW - 1, 1, "one less than full gets medium tier"},
		{mediumW, 1, "exact medium width fits medium tier"},
		{mediumW - 1, 2, "one less than medium gets narrow tier"},
		{narrowW, 2, "exact narrow width fits narrow tier"},
		{narrowW - 1, 3, "one less than narrow gets minimal tier"},
		{minimalW, 3, "exact minimal width fits minimal tier"},
		{1, 3, "very narrow terminal gets minimal tier"},
	}

	for _, c := range cases {
		got := shortcutHintForWidth(c.width)
		plain := stripAnsi(got)
		want := shortcutHintTiers[c.wantTier]
		if plain != want {
			t.Errorf("width=%d (%s): got %q, want %q", c.width, c.label, plain, want)
		}
	}
}

func TestShortcutHintForWidthNoNewlines(t *testing.T) {
	for w := 1; w <= 120; w++ {
		got := shortcutHintForWidth(w)
		if strings.Contains(got, "\n") {
			t.Errorf("width=%d: output contains newline: %q", w, got)
		}
	}
}

func TestShortcutHintForWidthAlwaysSingleLine(t *testing.T) {
	for w := 1; w <= 200; w++ {
		got := stripAnsi(shortcutHintForWidth(w))
		if strings.Contains(got, "\n") {
			t.Fatalf("width=%d: visible text contains newline", w)
		}
	}
}

func TestSeparatorForWidth(t *testing.T) {
	got := stripAnsi(separatorForWidth(80))
	if got != strings.Repeat("═", 80) {
		t.Errorf("separatorForWidth(80) visible width = %d, want 80", len([]rune(got)))
	}

	got = stripAnsi(separatorForWidth(40))
	if got != strings.Repeat("═", 40) {
		t.Errorf("separatorForWidth(40) visible width = %d, want 40", len([]rune(got)))
	}

	got = stripAnsi(separatorForWidth(1))
	if got != "═" {
		t.Errorf("separatorForWidth(1) = %q, want single ═", got)
	}

	got = stripAnsi(separatorForWidth(0))
	if got != "═" {
		t.Errorf("separatorForWidth(0) should clamp to 1, got %q", got)
	}
}

func TestSubSeparatorForWidth(t *testing.T) {
	got := stripAnsi(subSeparatorForWidth(80))
	if got != strings.Repeat("─", 80) {
		t.Errorf("subSeparatorForWidth(80) visible width = %d, want 80", len([]rune(got)))
	}

	got = stripAnsi(subSeparatorForWidth(1))
	if got != "─" {
		t.Errorf("subSeparatorForWidth(1) = %q, want single ─", got)
	}
}

func TestTruncateToVisible(t *testing.T) {
	if got := truncateToVisible("hello", 10); got != "hello" {
		t.Errorf("short string should pass through, got %q", got)
	}
	if got := truncateToVisible("hello", 5); got != "hello" {
		t.Errorf("exact fit should pass through, got %q", got)
	}
	if got := truncateToVisible("hello world", 6); got != "hello…" {
		t.Errorf("overlong should truncate with ellipsis, got %q", got)
	}
	if got := truncateToVisible("hello", 1); got != "h" {
		t.Errorf("single-rune budget should take one rune, got %q", got)
	}
	if got := truncateToVisible("hello", 0); got != "" {
		t.Errorf("zero budget should return empty, got %q", got)
	}
	if got := truncateToVisible("héllo wörld", 7); got != "héllo …" {
		t.Errorf("unicode truncation, got %q", got)
	}
}

func TestSepWidthDefaultIs80(t *testing.T) {
	w := sepWidth()
	if w != maxSepWidth {
		t.Errorf("sepWidth() in non-TTY test env = %d, want %d", w, maxSepWidth)
	}
}

func TestRenderHeaderLabelTruncationNarrow(t *testing.T) {
	for _, width := range []int{20, 10, 5, 3} {
		label := "this is a very long label that should be truncated"
		truncated := truncateToVisible(label, width-labelIndent)
		header := separatorForWidth(width) + "\n  " + truncated + "\n" + subSeparatorForWidth(width)
		plain := stripAnsi(header)
		lines := strings.Split(plain, "\n")
		if len(lines) < 1 {
			t.Errorf("width=%d: no lines in output", width)
			continue
		}
		sepRunes := []rune(lines[0])
		if len(sepRunes) != width {
			t.Errorf("width=%d: separator has %d runes, want %d", width, len(sepRunes), width)
		}
		if len(lines) < 2 {
			t.Errorf("width=%d: missing label line", width)
			continue
		}
		labelLine := lines[1]
		labelRunes := []rune(labelLine)
		if len(labelRunes) > width {
			t.Errorf("width=%d: label line has %d runes, exceeds width", width, len(labelRunes))
		}
	}
}

func TestRenderFooterHasSeparators(t *testing.T) {
	got := RenderFooter(FooterOptions{
		Passed:       true,
		Duration:     1 * time.Second,
		FilesChanged: 0,
	})

	plain := stripAnsi(got)
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 {
		t.Fatalf("footer should have 3 lines (sub-sep, content, sep), got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], strings.Repeat("─", maxSepWidth)) {
		t.Errorf("footer first line should be full-width sub-separator, got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], strings.Repeat("═", maxSepWidth)) {
		t.Errorf("footer last line should be full-width separator, got: %q", lines[2])
	}
	if !strings.Contains(lines[1], "passed") {
		t.Errorf("footer middle line should contain outcome, got: %q", lines[1])
	}
}

func TestRenderSummaryHasSeparators(t *testing.T) {
	got := RenderSummary(5, 3, 2, 10*time.Minute)

	plain := stripAnsi(got)
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 {
		t.Fatalf("summary should have 3 lines (sep, content, sep), got %d: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], strings.Repeat("═", maxSepWidth)) {
		t.Errorf("summary first line should be full-width separator, got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], strings.Repeat("═", maxSepWidth)) {
		t.Errorf("summary last line should be full-width separator, got: %q", lines[2])
	}
	if !strings.Contains(lines[1], "Relay complete:") {
		t.Errorf("summary middle line should contain content, got: %q", lines[1])
	}
}
