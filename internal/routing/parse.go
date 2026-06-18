package routing

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var singleQuotaRe = regexp.MustCompile(`^\d+$`)

var rangeQuotaRe = regexp.MustCompile(`^(\d+)-(\d+)$`)

type ParsedEntry struct {
	Raw           string
	Spec          string
	QuotaMin      int
	QuotaMax      int
	HasQuota      bool
	ExplicitModel bool
}

func (e ParsedEntry) QuotaSingle() bool {
	return e.HasQuota && e.QuotaMin == e.QuotaMax
}

func (e ParsedEntry) QuotaRange() bool {
	return e.HasQuota && e.QuotaMin != e.QuotaMax
}

func ParseEntry(raw string) (ParsedEntry, error) {
	if raw == "" {
		return ParsedEntry{}, fmt.Errorf("routing: empty entry")
	}

	segments := strings.Split(raw, ":")

	quotaMin, quotaMax, hasQuota, idSegments, err := extractQuota(segments, raw)
	if err != nil {
		return ParsedEntry{}, err
	}

	switch len(idSegments) {
	case 1:
	case 2:
	default:
		return ParsedEntry{}, fmt.Errorf(
			"routing: entry %q has %d identifier segments (expected 1 for shortcut or 2 for harness:model)",
			raw, len(idSegments),
		)
	}

	spec := strings.Join(idSegments, ":")

	return ParsedEntry{
		Raw:           raw,
		Spec:          spec,
		QuotaMin:      quotaMin,
		QuotaMax:      quotaMax,
		HasQuota:      hasQuota,
		ExplicitModel: len(idSegments) == 2,
	}, nil
}

func ParseEntries(entries []string) ([]ParsedEntry, error) {
	result := make([]ParsedEntry, len(entries))
	for i, raw := range entries {
		p, err := ParseEntry(raw)
		if err != nil {
			return nil, err
		}
		result[i] = p
	}
	return result, nil
}

func extractQuota(segments []string, raw string) (qMin, qMax int, hasQuota bool, idSegments []string, err error) {
	if len(segments) < 2 {
		return 0, 0, false, segments, nil
	}

	last := segments[len(segments)-1]

	if m := rangeQuotaRe.FindStringSubmatch(last); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if lo <= 0 {
			return 0, 0, false, nil, fmt.Errorf("routing: entry %q has quota min %d; must be positive", raw, lo)
		}
		if lo > hi {
			return 0, 0, false, nil, fmt.Errorf("routing: entry %q has quota range %d-%d; min must not exceed max", raw, lo, hi)
		}
		return lo, hi, true, segments[:len(segments)-1], nil
	}

	if singleQuotaRe.MatchString(last) {
		n, _ := strconv.Atoi(last)
		if n <= 0 {
			return 0, 0, false, nil, fmt.Errorf("routing: entry %q has quota %d; must be positive", raw, n)
		}
		return n, n, true, segments[:len(segments)-1], nil
	}

	return 0, 0, false, segments, nil
}
