package reliability

import (
	"testing"
	"time"
)

var testAEDT = time.FixedZone("AEDT", 11*3600)

func pinClock(t *testing.T, now time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = old })
}

func at(d, h, m, s int) time.Time {
	return time.Date(2026, time.November, d, h, m, s, 0, testAEDT)
}

func TestClockReset(t *testing.T) {
	codex := func(clock string) string {
		return "You've hit your usage limit. Try again at " + clock + "."
	}
	claude := func(clock string) string {
		return "rate_limit: usage limit reached · resets at " + clock
	}
	tests := []struct {
		name   string
		claude bool
		text   string
		now    time.Time
		want   time.Time
	}{
		{"R1 pray-f406 now 23:07:01", false, codex("11:07 PM"), at(15, 23, 7, 1), at(15, 23, 8, 1)},
		{"R2 now 23:06:50", false, codex("11:07 PM"), at(15, 23, 6, 50), at(15, 23, 8, 0)},
		{"R3 now 22:00:00", false, codex("11:07 PM"), at(15, 22, 0, 0), at(15, 23, 8, 0)},
		{"R4 23 min past", false, codex("11:07 PM"), at(15, 23, 30, 0), at(16, 23, 8, 0)},
		{"R5 base+14m59s", false, codex("11:07 PM"), at(15, 23, 21, 59), at(15, 23, 22, 59)},
		{"R5 base+15m00s", false, codex("11:07 PM"), at(15, 23, 22, 0), at(16, 23, 8, 0)},
		{"R6 12:00 AM", false, codex("12:00 AM"), at(15, 0, 0, 30), at(15, 0, 1, 30)},
		{"R6 12:05 PM", false, codex("12:05 PM"), at(15, 11, 0, 0), at(15, 12, 6, 0)},
		{"R7 claude 14:30 recent", true, claude("14:30"), at(15, 14, 30, 5), at(15, 14, 31, 5)},
		{"R7 claude 2:30 PM", true, claude("2:30 PM"), at(15, 13, 0, 0), at(15, 14, 31, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinClock(t, tt.now)
			var ev *FailureEvidence
			if tt.claude {
				ev = ParseClaudeError(tt.text)
			} else {
				ev = ParseCodexError(tt.text)
			}
			if ev == nil || ev.ResetAt == nil {
				t.Fatalf("no ResetAt parsed from %q", tt.text)
			}
			if !ev.ResetAt.Equal(tt.want) {
				t.Errorf("ResetAt = %v, want %v (now %v)", ev.ResetAt, tt.want, tt.now)
			}
		})
	}
}

func TestClockReset_ResetAfter(t *testing.T) {
	now := at(15, 23, 7, 1)
	pinClock(t, now)
	ev := ParseCodexError("You've hit your usage limit. Try again at 11:07 PM.")
	if ev == nil || ev.ResetAt == nil {
		t.Fatal("no ResetAt")
	}
	if want := ev.ResetAt.Sub(now); ev.ResetAfter != want || want != 60*time.Second {
		t.Errorf("ResetAfter = %v, ResetAt-now = %v, want 60s", ev.ResetAfter, want)
	}
	if ev.ResetAfter < 0 {
		t.Errorf("ResetAfter negative: %v", ev.ResetAfter)
	}
}
