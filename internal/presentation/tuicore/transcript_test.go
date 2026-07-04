package tuicore

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/style"
)

func TestTranscriptUsesStyleRenderers(t *testing.T) {
	start := time.Date(2026, 7, 4, 9, 0, 0, 0, time.Local)
	header := runtimeevent.RunHeaderReady{
		RunIndex:     0,
		TotalRuns:    2,
		AgentName:    "codex",
		Attempt:      1,
		StartTime:    start,
		IsLapsBacked: true,
		LapTitle:     "Build shared TUI transcript",
		LapsStarted:  1,
		LapsTotal:    2,
		Model:        "gpt-5.5",
	}
	footer := runtimeevent.FooterData{
		Passed:       true,
		Duration:     2*time.Minute + 3*time.Second,
		FilesChanged: 4,
		CommitHash:   "abc1234",
		Attempt:      1,
		MaxAttempts:  1,
	}
	summary := runtimeevent.RelaySummaryReady{
		TotalRuns:     1,
		Passed:        1,
		Failed:        0,
		TotalDuration: 2*time.Minute + 3*time.Second,
	}

	var tr Transcript
	tr.Apply(header)
	tr.Apply(runtimeevent.TryStatusSnapshot{Status: "⏱ 3s  │  📁 0 files  │  last activity: < 1m ago"})
	tr.Apply(runtimeevent.AttemptFinished{FooterData: footer})
	tr.Apply(summary)

	var want []string
	want = append(want, strings.Split(style.RenderHeader(headerOptions(header)), "\n")...)
	want = append(want, "⏱ 3s  │  📁 0 files  │  last activity: < 1m ago")
	want = append(want, strings.Split(style.RenderFooter(footerOptions(footer)), "\n")...)
	want = append(want, strings.Split(style.RenderSummary(summary.TotalRuns, summary.Passed, summary.Failed, summary.TotalDuration, summary.Cancelled), "\n")...)
	if got := tr.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("Lines:\n got %#v\nwant %#v", got, want)
	}
}

func TestTranscriptInterimLifecycle(t *testing.T) {
	interim := runtimeevent.FooterData{
		Duration:     time.Minute,
		FilesChanged: 1,
		FailReason:   "usage limit",
		Interim:      true,
		Attempt:      1,
		MaxAttempts:  2,
	}
	final := runtimeevent.FooterData{
		Duration:     3 * time.Minute,
		FilesChanged: 0,
		FailReason:   "usage limit",
		Attempt:      2,
		MaxAttempts:  2,
	}

	var tr Transcript
	tr.Apply(runtimeevent.RetryFooterUpdated{FooterData: interim})
	if got, want := tr.LiveTail(), []string{style.RenderFooter(footerOptions(interim))}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LiveTail after interim:\n got %#v\nwant %#v", got, want)
	}
	if got := tr.Lines(); len(got) != 0 {
		t.Fatalf("interim footer should not append completed lines, got %#v", got)
	}

	tr.Apply(runtimeevent.AttemptFinished{FooterData: final})
	if got := tr.LiveTail(); len(got) != 0 {
		t.Fatalf("LiveTail after final footer = %#v, want empty", got)
	}
	wantLines := strings.Split(style.RenderFooter(footerOptions(final)), "\n")
	if got := tr.Lines(); !reflect.DeepEqual(got, wantLines) {
		t.Errorf("Lines after final footer:\n got %#v\nwant %#v", got, wantLines)
	}
}

func TestTranscriptWaitFrameLifecycle(t *testing.T) {
	var tr Transcript
	tr.Apply(runtimeevent.WaitStarted{Message: "next run starts in 3s", Hint: "press Ctrl+S again to skip wait"})
	want := []string{
		style.DimStyle.Render("next run starts in 3s"),
		style.WarningStyle.Render("⌨ press Ctrl+S again to skip wait"),
	}
	if got := tr.LiveTail(); !reflect.DeepEqual(got, want) {
		t.Fatalf("LiveTail after wait start:\n got %#v\nwant %#v", got, want)
	}

	tr.Apply(runtimeevent.WaitTick{Message: "next run starts in 2s"})
	if got := tr.LiveTail(); len(got) != 2 || got[0] != style.DimStyle.Render("next run starts in 2s") {
		t.Fatalf("LiveTail after wait tick = %#v", got)
	}

	tr.Apply(runtimeevent.WaitFinished{})
	if got := tr.LiveTail(); len(got) != 0 {
		t.Fatalf("LiveTail after wait finish = %#v, want empty", got)
	}
}
