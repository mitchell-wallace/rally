package reliability

import "time"

// nowFunc is the clock seam for reset-time parsing; tests pin it.
var nowFunc = time.Now

const (
	clockResetPad   = 60 * time.Second
	clockResetGrace = 15 * time.Minute
)

// nextClockReset resolves a minute-truncated wall-clock reset (hour:minute) to
// the instant to retry. The quoted minute is truncated, so the real reset can
// be up to 59 s after hh:mm:00; wait out the whole minute. A clock only
// seconds/minutes in the past means the reset is happening now, not tomorrow.
func nextClockReset(now time.Time, hour, minute int) time.Time {
	base := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	eff := base.Add(clockResetPad)
	if now.Before(base) {
		return eff
	}
	if now.Sub(base) < clockResetGrace {
		return now.Add(clockResetPad)
	}
	return eff.Add(24 * time.Hour)
}
