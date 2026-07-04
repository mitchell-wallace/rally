package tuicore

import (
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

const (
	OutcomeRunning   = "running"
	OutcomePassed    = "passed"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
	OutcomeHandoff   = "handoff"
)

type FeedItem struct {
	OutingIndex    int
	Agent          string
	Model          string
	RoleLabel      string
	Title          string
	StartedAt      time.Time
	Outcome        string
	Duration       time.Duration
	Files          int
	CommitHash     string
	CommitTitle    string
	FailReason     string
	Attempt        int
	MaxAttempts    int
	Summary        string
	Classification string
	Followups      []string
}

type FeedMeta struct {
	RelayID       int
	Mix           string
	Target        int
	LapsStarted   int
	LapsTotal     int
	Passed        int
	Failed        int
	Cancelled     int
	TotalDuration time.Duration
	Completed     bool
}

// OutingFeed reduces runtime events into a structured outing feed for panel UIs.
type OutingFeed struct {
	items     []FeedItem
	liveIndex int
	meta      FeedMeta
}

// Apply folds one runtime event into the feed.
func (f *OutingFeed) Apply(e runtimeevent.Event) {
	if e == nil {
		return
	}
	if f.liveIndex == 0 && len(f.items) == 0 {
		f.liveIndex = -1
	}

	switch e := e.(type) {
	case runtimeevent.RelayStarted:
		f.meta.RelayID = e.RelayID
		f.meta.Target = e.TargetIterations
		f.meta.Mix = e.AgentMix
	case runtimeevent.OutingHeaderReady:
		f.applyHeader(e)
	case runtimeevent.RetryFooterUpdated:
		f.updateFooter(e.FooterData, OutcomeRunning)
	case runtimeevent.AttemptFinished:
		outcome := OutcomeFailed
		if e.Passed {
			outcome = OutcomePassed
		}
		f.updateFooter(e.FooterData, outcome)
	case runtimeevent.AttemptCancelled:
		f.updateFooter(e.FooterData, OutcomeCancelled)
	case runtimeevent.HandoffAttemptFinished:
		f.updateFooter(e.FooterData, OutcomeHandoff)
	case runtimeevent.RelaySummaryReady:
		f.meta.Passed = e.Passed
		f.meta.Failed = e.Failed
		f.meta.Cancelled = e.Cancelled
		f.meta.TotalDuration = e.TotalDuration
	case runtimeevent.RelayCompleted:
		f.meta.RelayID = e.RelayID
		f.meta.Passed = e.Passed
		f.meta.Failed = e.Failed
		f.meta.Cancelled = e.Cancelled
		f.meta.TotalDuration = e.TotalDuration
		f.meta.Completed = true
		f.liveIndex = -1
	}
}

// Seed prepends already-finished historical items. Seeded rows are copied and
// marked non-running so they do not become the live row.
func (f *OutingFeed) Seed(items []FeedItem) {
	if len(items) == 0 {
		return
	}
	seeded := make([]FeedItem, len(items))
	for i, item := range items {
		if item.Outcome == "" || item.Outcome == OutcomeRunning {
			item.Outcome = OutcomePassed
		}
		item.Followups = append([]string(nil), item.Followups...)
		seeded[i] = item
	}
	f.items = append(seeded, f.items...)
	if f.liveIndex >= 0 {
		f.liveIndex += len(seeded)
	}
}

// Enrich adds optional summary data that is not carried by runtime events.
func (f *OutingFeed) Enrich(outingIndex int, summary, classification string, followups []string) {
	for i := range f.items {
		if f.items[i].OutingIndex != outingIndex {
			continue
		}
		f.items[i].Summary = summary
		f.items[i].Classification = classification
		f.items[i].Followups = append([]string(nil), followups...)
		return
	}
}

// SetLiveStats updates the running row from monitor frames when structured
// status-tick events are not available.
func (f *OutingFeed) SetLiveStats(duration time.Duration, files int) {
	idx := f.LiveIndex()
	if idx < 0 {
		return
	}
	if duration > 0 {
		f.items[idx].Duration = duration
	}
	if files >= 0 {
		f.items[idx].Files = files
	}
}

// Items returns a copy of the current feed items.
func (f *OutingFeed) Items() []FeedItem {
	out := make([]FeedItem, len(f.items))
	for i, item := range f.items {
		item.Followups = append([]string(nil), item.Followups...)
		out[i] = item
	}
	return out
}

// LiveIndex returns the index of the current running item, or -1 when none.
func (f *OutingFeed) LiveIndex() int {
	if f.liveIndex < 0 || f.liveIndex >= len(f.items) {
		return -1
	}
	return f.liveIndex
}

// Meta returns relay-level feed metadata.
func (f *OutingFeed) Meta() FeedMeta {
	return f.meta
}

func (f *OutingFeed) applyHeader(e runtimeevent.OutingHeaderReady) {
	f.meta.Target = firstPositive(f.meta.Target, e.TotalOutings)
	f.meta.LapsStarted = e.LapsStarted
	f.meta.LapsTotal = e.LapsTotal

	item := FeedItem{
		OutingIndex: e.OutingIndex,
		Agent:       e.AgentName,
		Model:       e.Model,
		RoleLabel:   e.RoleLabel,
		Title:       e.LapTitle,
		StartedAt:   e.StartTime,
		Outcome:     OutcomeRunning,
		Attempt:     e.Attempt,
		MaxAttempts: e.Attempt,
	}
	if item.Title == "" {
		item.Title = "Outing"
	}
	for i := range f.items {
		if f.items[i].OutingIndex == e.OutingIndex && f.items[i].Outcome == OutcomeRunning {
			item.Duration = f.items[i].Duration
			item.Files = f.items[i].Files
			item.CommitHash = f.items[i].CommitHash
			item.CommitTitle = f.items[i].CommitTitle
			item.FailReason = f.items[i].FailReason
			item.Summary = f.items[i].Summary
			item.Classification = f.items[i].Classification
			item.Followups = append([]string(nil), f.items[i].Followups...)
			f.items[i] = item
			f.liveIndex = i
			return
		}
	}
	f.items = append(f.items, item)
	f.liveIndex = len(f.items) - 1
}

func (f *OutingFeed) updateFooter(data runtimeevent.FooterData, outcome string) {
	idx := f.liveIndex
	if idx < 0 || idx >= len(f.items) {
		return
	}
	f.items[idx].Outcome = outcome
	f.items[idx].Duration = data.Duration
	f.items[idx].Files = data.FilesChanged
	f.items[idx].CommitHash = data.CommitHash
	f.items[idx].CommitTitle = data.CommitTitle
	f.items[idx].FailReason = data.FailReason
	f.items[idx].Attempt = firstPositive(data.Attempt, f.items[idx].Attempt)
	f.items[idx].MaxAttempts = firstPositive(data.MaxAttempts, f.items[idx].MaxAttempts)
	if outcome != OutcomeRunning {
		f.liveIndex = -1
	}
}

func firstPositive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
