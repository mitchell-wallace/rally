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
