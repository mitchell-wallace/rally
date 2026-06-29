package runner

import (
	"fmt"
	"time"

	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

func formatCategorizedDisplay(cat reliability.FailureCategory, cooldown time.Duration, evidence *reliability.FailureEvidence) string {
	label := reliability.CategoryDisplayLabel(cat)
	switch cat {
	case reliability.CategoryUsageLimit:
		// Only show a reset time backed by parsed evidence. The classifier's
		// cooldown is a legacy wait default, not the quota reset — the actual
		// bench window without evidence is BenchDefaultDuration, so echoing
		// the cooldown would display a deadline that does not match the bench.
		if dur := usageResetDuration(evidence); dur > 0 {
			return fmt.Sprintf("%s, resets in %s", label, formatHoursMinutes(dur))
		}
		return label
	case reliability.CategoryShortRateLimit:
		if cooldown > 0 {
			return fmt.Sprintf("%s, waiting %s", label, formatMinutesSeconds(cooldown))
		}
		return label
	default:
		return label
	}
}

func usageResetDuration(evidence *reliability.FailureEvidence) time.Duration {
	if evidence == nil {
		return 0
	}
	if evidence.ResetAfter > 0 {
		return evidence.ResetAfter
	}
	if evidence.ResetAt != nil {
		if remaining := time.Until(*evidence.ResetAt); remaining > 0 {
			return remaining
		}
	}
	return 0
}

func formatHoursMinutes(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatMinutesSeconds(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// benchResetDeadline derives the bench-window end from parsed reset evidence,
// preferring an absolute ResetAt, then a relative ResetAfter, and finally a
// conservative BenchDefaultDuration fallback when a usage_limit carried no
// parsed deadline.
func benchResetDeadline(ev *reliability.FailureEvidence, now time.Time) time.Time {
	if ev != nil {
		if ev.ResetAt != nil {
			return *ev.ResetAt
		}
		if ev.ResetAfter > 0 {
			return now.Add(ev.ResetAfter)
		}
	}
	return now.Add(relaycore.BenchDefaultDuration)
}
