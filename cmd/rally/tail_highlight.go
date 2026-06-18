package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/lipgloss"
)

type highlightWriter struct {
	w       io.Writer
	mode    string
	buf     []byte
}

func newHighlightWriter(w io.Writer, mode string) io.Writer {
	return &highlightWriter{
		w:    w,
		mode: mode,
	}
}

func (h *highlightWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx == -1 {
			h.buf = append(h.buf, p...)
			break
		}

		line := append(h.buf, p[:idx+1]...)
		h.buf = h.buf[:0]
		p = p[idx+1:]

		var outLine []byte
		if h.mode == "heuristic" {
			outLine = h.applyHeuristic(line)
		} else if h.mode == "chroma" {
			outLine = h.applyChroma(line)
		} else {
			outLine = line
		}

		if _, err := h.w.Write(outLine); err != nil {
			return 0, err
		}
	}
	return n, nil
}

var (
	errorColor = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	timeColor  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	cmdColor   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan
	urlColor   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Underline(true)
	jsonColor  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green

	urlRe   = regexp.MustCompile(`https?://[^\s"']+`)
	timeRe  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?\b|\b\d{2}:\d{2}:\d{2}\b`)
	errorRe = regexp.MustCompile(`(?i)\b(error|panic|fatal|fail|failed)\b`)
	cmdRe   = regexp.MustCompile(`^(\$|>)\s+.*`)
)

func (h *highlightWriter) applyHeuristic(line []byte) []byte {
	s := string(line)
	hasNewline := strings.HasSuffix(s, "\n")
	if hasNewline {
		s = strings.TrimSuffix(s, "\n")
	}

	trimmed := strings.TrimSpace(s)
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		res := jsonColor.Render(s)
		if hasNewline {
			res += "\n"
		}
		return []byte(res)
	}

	s = urlRe.ReplaceAllStringFunc(s, func(m string) string {
		return urlColor.Render(m)
	})

	s = timeRe.ReplaceAllStringFunc(s, func(m string) string {
		return timeColor.Render(m)
	})

	s = errorRe.ReplaceAllStringFunc(s, func(m string) string {
		return errorColor.Render(m)
	})

	s = cmdRe.ReplaceAllStringFunc(s, func(m string) string {
		return cmdColor.Render(m)
	})

	if hasNewline {
		s += "\n"
	}
	return []byte(s)
}

func (h *highlightWriter) applyChroma(line []byte) []byte {
	s := string(line)
	hasNewline := strings.HasSuffix(s, "\n")
	if hasNewline {
		s = strings.TrimSuffix(s, "\n")
	}

	trimmed := strings.TrimSpace(s)
	lexer := "bash"
	if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
		lexer = "json"
	}

	var buf bytes.Buffer
	err := quick.Highlight(&buf, s, lexer, "terminal256", "monokai")
	if err != nil {
		if hasNewline {
			return append(line[:len(line)-1], '\n')
		}
		return line
	}

	res := buf.Bytes()
	res = bytes.TrimSuffix(res, []byte("\n"))

	if hasNewline {
		res = append(res, '\n')
	}
	return res
}
