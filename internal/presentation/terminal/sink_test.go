package terminal

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/style"
)

func TestSinkRoutesWarningsToErr(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.RouteWarning{Message: "warning: route fallback"})
	sink.Emit(context.Background(), runtimeevent.TaskFileWarning{Message: "warning: task file"})

	if got := out.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	want := "warning: route fallback\nwarning: task file\n"
	if got := err.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestSinkRendersWaitFramesAndClear(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.WaitStarted{Message: "agents paused, waiting 5s..."})
	sink.Emit(context.Background(), runtimeevent.WaitTick{Message: "agents paused, waiting 4s...", Hint: "press Ctrl+S again to skip"})
	sink.Emit(context.Background(), runtimeevent.WaitFinished{})

	want := fmt.Sprintf("\r\x1b[J%s\r\n%s\x1b[1A\r", style.DimStyle.Render("agents paused, waiting 5s..."), style.ShortcutHint())
	want += fmt.Sprintf("\r\x1b[J%s\r\n%s\x1b[1A\r", style.DimStyle.Render("agents paused, waiting 4s..."), style.WarningStyle.Render("⌨ press Ctrl+S again to skip"))
	want += "\r\x1b[J"
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

// TestSinkRendersWaitFrameOnNewLineSafely pins the raw-mode fix relocated from
// the runner wait-loop tests: countdown and hint lines are separated by CRLF,
// not bare LF.
func TestSinkRendersWaitFrameOnNewLineSafely(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.WaitStarted{Message: "agents paused, waiting 5s..."})

	got := out.String()
	if strings.Contains(got, "...\n") && !strings.Contains(got, "...\r\n") {
		t.Errorf("countdown line followed by bare LF (raw-mode unsafe): %q", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Errorf("expected a CR+LF between countdown and hint, got %q", got)
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestSinkRenderRunFooterInterimRedrawsInPlace(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.RetryFooterUpdated{FooterData: runtimeevent.FooterData{
		Passed:      false,
		Interim:     true,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     2,
		MaxAttempts: 5,
	}})

	want := fmt.Sprintf("\r\x1b[2K%s\r", style.RenderFooter(style.FooterOptions{
		Passed:      false,
		Interim:     true,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     2,
		MaxAttempts: 5,
	}))
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.HasSuffix(out.String(), "\r") {
		t.Errorf("interim footer should park the cursor at the line start, got: %q", out.String())
	}
	if strings.Contains(out.String(), "\n") {
		t.Errorf("interim footer must not commit a newline, got: %q", out.String())
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestSinkRenderRunFooterTerminalCommits(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{
		Passed:      false,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     5,
		MaxAttempts: 5,
	}})

	want := fmt.Sprintf("\r\x1b[2K%s\n", style.RenderFooter(style.FooterOptions{
		Passed:      false,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     5,
		MaxAttempts: 5,
	}))
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("terminal footer should commit with a trailing newline, got: %q", out.String())
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestSinkRendersFooterRedrawBytes(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)
	footer := runtimeevent.FooterData{
		Passed:       false,
		Duration:     3 * time.Second,
		FilesChanged: 2,
		FailReason:   "rate limit",
		Interim:      true,
		Attempt:      1,
		MaxAttempts:  3,
	}

	sink.Emit(context.Background(), runtimeevent.RetryFooterUpdated{FooterData: footer})
	footer.Interim = false
	sink.Emit(context.Background(), runtimeevent.AttemptFinished{FooterData: footer})

	interim := style.FooterOptions{
		Duration:     3 * time.Second,
		FilesChanged: 2,
		FailReason:   "rate limit",
		Interim:      true,
		Attempt:      1,
		MaxAttempts:  3,
	}
	terminal := interim
	terminal.Interim = false
	want := fmt.Sprintf("\r\x1b[2K%s\r", style.RenderFooter(interim))
	want += fmt.Sprintf("\r\x1b[2K%s\n", style.RenderFooter(terminal))
	if got := out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

// TestSinkDispatchRendersEachCase pins every render branch of the sink switch
// that the byte-specific tests above do not already cover. A dropped case would
// otherwise silently vanish operator-facing output while the suite stays green;
// this guards the byte-identity contract for the cancelled/handoff footers, the
// pause prompt, the header/status/shortcut/rate-limit lines, and the summary.
func TestSinkDispatchRendersEachCase(t *testing.T) {
	tests := []struct {
		name string
		emit func(s *Sink)
		want string
	}{
		{
			name: "OutingHeaderReady",
			emit: func(s *Sink) {
				s.Emit(context.Background(), runtimeevent.OutingHeaderReady{OutingIndex: 1, TotalOutings: 3, AgentName: "sonnet", Attempt: 1, Model: "claude-4", RoleLabel: "senior"})
			},
			want: style.RenderHeader(headerOptions(runtimeevent.OutingHeaderReady{OutingIndex: 1, TotalOutings: 3, AgentName: "sonnet", Attempt: 1, Model: "claude-4", RoleLabel: "senior"})) + "\n",
		},
		{
			name: "TryStatusSnapshot",
			emit: func(s *Sink) { s.Emit(context.Background(), runtimeevent.TryStatusSnapshot{Status: "running..."}) },
			want: "\r\x1b[2Krunning...\n",
		},
		{
			name: "ShortcutHintReady",
			emit: func(s *Sink) { s.Emit(context.Background(), runtimeevent.ShortcutHintReady{Width: 0}) },
			want: fmt.Sprintf("\r\x1b[2K%s\n", style.ShortcutHint()),
		},
		{
			name: "AttemptCancelled",
			emit: func(s *Sink) {
				s.Emit(context.Background(), runtimeevent.AttemptCancelled{FooterData: runtimeevent.FooterData{Cancelled: true, Duration: 5 * time.Second, CancellationSource: "quit_now", Attempt: 2, MaxAttempts: 5}})
			},
			want: fmt.Sprintf("\r\x1b[2K%s\n", style.RenderFooter(footerOptions(runtimeevent.FooterData{Cancelled: true, Duration: 5 * time.Second, CancellationSource: "quit_now", Attempt: 2, MaxAttempts: 5}))),
		},
		{
			name: "HandoffAttemptFinished",
			emit: func(s *Sink) {
				s.Emit(context.Background(), runtimeevent.HandoffAttemptFinished{FooterData: runtimeevent.FooterData{Passed: false, Duration: 9 * time.Second, FailReason: "blocked", Attempt: 3, MaxAttempts: 3}})
			},
			want: fmt.Sprintf("\r\x1b[2K%s\n", style.RenderFooter(footerOptions(runtimeevent.FooterData{Passed: false, Duration: 9 * time.Second, FailReason: "blocked", Attempt: 3, MaxAttempts: 3}))),
		},
		{
			name: "RateLimitWaitStarted",
			emit: func(s *Sink) { s.Emit(context.Background(), runtimeevent.RateLimitWaitStarted{Wait: 30 * time.Second}) },
			want: style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", 30*time.Second)) + "\n",
		},
		{
			name: "PausePromptShown",
			emit: func(s *Sink) {
				s.Emit(context.Background(), runtimeevent.PausePromptShown{Message: "Paused — press Enter to resume"})
			},
			want: "Paused — press Enter to resume\n",
		},
		{
			name: "RelaySummaryReady",
			emit: func(s *Sink) {
				s.Emit(context.Background(), runtimeevent.RelaySummaryReady{TotalOutings: 4, Passed: 3, Failed: 1, Cancelled: 0, TotalDuration: 2 * time.Minute})
			},
			want: style.RenderSummary(4, 3, 1, 2*time.Minute, 0) + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			sink := NewSink(&out, &errw)
			tt.emit(sink)
			if got := out.String(); got != tt.want {
				t.Fatalf("stdout = %q, want %q", got, tt.want)
			}
			if got := errw.String(); got != "" {
				t.Fatalf("stderr = %q, want empty", got)
			}
		})
	}
}

func TestSinkNoopsLifecycleAndOperatorActions(t *testing.T) {
	var out, err bytes.Buffer
	sink := NewSink(&out, &err)

	sink.Emit(context.Background(), runtimeevent.RelayStarted{})
	sink.Emit(context.Background(), runtimeevent.RelayCompleted{})
	sink.Emit(context.Background(), runtimeevent.OperatorActionArmed{Action: runtimeevent.OperatorActionSkip, Message: runtimeevent.ArmMessage(runtimeevent.OperatorActionSkip)})
	sink.Emit(context.Background(), runtimeevent.OperatorActionApplied{Action: runtimeevent.OperatorActionSkip, Message: runtimeevent.ActMessage(runtimeevent.OperatorActionSkip)})

	if got := out.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := err.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}
