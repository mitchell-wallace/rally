package tuicore

import (
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// DemoStep is one timed runtime event in the synthetic TUI demo playback.
type DemoStep struct {
	Delay time.Duration
	Event runtimeevent.Event
}

// DemoScript returns a plausible relay event stream for prototype demo mode.
func DemoScript() []DemoStep {
	start := time.Date(2026, 7, 4, 14, 30, 0, 0, time.Local)
	return []DemoStep{
		{100 * time.Millisecond, runtimeevent.RelayStarted{RelayID: 42, TargetIterations: 14, AgentMix: "prototype"}},
		outingHeaderStep(150*time.Millisecond, 0, "opencode", "zai-coding-plan/glm-5.2", "Baseline check and capture agent's exported surface", 1, start),
		statusStep(120*time.Millisecond, "⏱ 5s  │  📁 0 files  │  last activity: < 1m ago"),
		{100 * time.Millisecond, runtimeevent.ShortcutHintReady{}},
		{300 * time.Millisecond, runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: 4*time.Minute + 23*time.Second, FilesChanged: 2, CommitHash: "769cfae", Attempt: 1, MaxAttempts: 1}}},
		outingHeaderStep(250*time.Millisecond, 1, "codex", "gpt-5.5", "Phase 1: introduce internal/harnessapi contract and re-point all consumers", 2, start.Add(5*time.Minute)),
		statusStep(120*time.Millisecond, "⏱ 1m 05s  │  📁 2 files  │  last activity: < 1m ago"),
		{100 * time.Millisecond, runtimeevent.ShortcutHintReady{}},
		{450 * time.Millisecond, runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: 15*time.Minute + 53*time.Second, FilesChanged: 62, CommitHash: "c69b719", Attempt: 1, MaxAttempts: 1}}},
		{150 * time.Millisecond, runtimeevent.WaitStarted{Message: "next run starts in 3s", Hint: "press Ctrl+S again to skip wait", Total: 3 * time.Second, Remaining: 3 * time.Second}},
		{300 * time.Millisecond, runtimeevent.WaitTick{Message: "next run starts in 2s", Hint: "", Remaining: 2 * time.Second}},
		{300 * time.Millisecond, runtimeevent.WaitFinished{}},
		outingHeaderStep(250*time.Millisecond, 2, "codex", "gpt-5.4", "Verify Phase 1 contract seam", 3, start.Add(22*time.Minute)),
		statusStep(120*time.Millisecond, "⏱ 7m 40s  │  📁 0 files  │  last activity: 2m ago"),
		{100 * time.Millisecond, runtimeevent.ShortcutHintReady{}},
		{350 * time.Millisecond, runtimeevent.RetryFooterUpdated{FooterData: runtimeevent.FooterData{Duration: 12*time.Minute + 8*time.Second, FilesChanged: 0, FailReason: "usage limit", Interim: true, Attempt: 1, MaxAttempts: 2}}},
		{500 * time.Millisecond, runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Duration: 33*time.Minute + 31*time.Second, FilesChanged: 0, FailReason: "usage limit", Attempt: 2, MaxAttempts: 2}}},
		{200 * time.Millisecond, runtimeevent.RateLimitWaitStarted{Wait: 2 * time.Minute}},
		outingHeaderStep(350*time.Millisecond, 3, "opencode", "glm-5.2", "Phase 2: extract internal/harness/process support package", 4, start.Add(56*time.Minute)),
		statusStep(120*time.Millisecond, "⏱ 42s  │  📁 3 files  │  last activity: < 1m ago"),
		{100 * time.Millisecond, runtimeevent.ShortcutHintReady{}},
		{450 * time.Millisecond, runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: 6*time.Minute + 49*time.Second, FilesChanged: 10, CommitHash: "3d1fb2b", Attempt: 1, MaxAttempts: 1}}},
		{250 * time.Millisecond, runtimeevent.RelaySummaryReady{TotalOutings: 4, Passed: 3, Failed: 1, TotalDuration: time.Hour + 35*time.Second}},
		{100 * time.Millisecond, runtimeevent.RelayCompleted{RelayID: 42, TotalOutings: 4, Passed: 3, Failed: 1, TotalDuration: time.Hour + 35*time.Second}},
	}
}

// DemoStatusFrames returns monitor-like status lines for demo tickers.
func DemoStatusFrames() []string {
	return []string{
		"⏱ 12s  │  📁 0 files  │  last activity: < 1m ago",
		"⏱ 1m 05s  │  📁 2 files  │  last activity: < 1m ago",
		"⏱ 4m 18s  │  📁 2 files  │  last activity: 1m ago",
		"⏱ 11m 42s  │  📁 18 files  │  last activity: < 1m ago",
		"⏱ 15m 53s  │  📁 62 files  │  last activity: < 1m ago",
	}
}

func outingHeaderStep(delay time.Duration, outingIndex int, agent, model, lapTitle string, lapsStarted int, start time.Time) DemoStep {
	return DemoStep{Delay: delay, Event: runtimeevent.OutingHeaderReady{
		OutingIndex:  outingIndex,
		TotalOutings: 14,
		AgentName:    agent,
		Attempt:      1,
		StartTime:    start,
		IsLapsBacked: true,
		LapTitle:     lapTitle,
		LapsStarted:  lapsStarted,
		LapsTotal:    14,
		Model:        model,
	}}
}

func statusStep(delay time.Duration, status string) DemoStep {
	return DemoStep{Delay: delay, Event: runtimeevent.TryStatusSnapshot{Status: status}}
}
