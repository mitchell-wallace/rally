package textutil

// HeadTailTruncationMarker separates preserved text when the middle is removed.
const HeadTailTruncationMarker = "... [truncated] ..."

// TruncateHeadTailRunes caps text by rune count while preserving both ends.
func TruncateHeadTailRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}

	marker := []rune(HeadTailTruncationMarker)
	if maxRunes <= len(marker) {
		return string(marker[:maxRunes])
	}

	remaining := maxRunes - len(marker)
	headSize := remaining / 2
	tailSize := remaining - headSize
	return string(runes[:headSize]) + HeadTailTruncationMarker + string(runes[len(runes)-tailSize:])
}
