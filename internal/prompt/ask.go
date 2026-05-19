// Package prompt centralises rally's user-facing CLI prompts. It uses
// charmbracelet/huh for interactive TUI prompts when stdin is a terminal, and
// falls back to a plain bufio reader otherwise so unit tests and piped
// invocations keep working unchanged.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// isInteractive reports whether the given reader is an interactive terminal.
// huh only renders meaningfully against a real TTY; for piped/stub readers we
// drop back to plain-text prompts.
func isInteractive(in io.Reader) bool {
	type fdHolder interface{ Fd() uintptr }
	f, ok := in.(fdHolder)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Option is a labelled choice for Select prompts.
type Option struct {
	Label string
	Value string
}

// Confirm asks the user a yes/no question. `def` is returned on EOF and when
// the input is not a TTY without an answer line.
func Confirm(in io.Reader, out io.Writer, title, description string, def bool) (bool, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stderr
	}
	if isInteractive(in) {
		result := def
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description(description).
					Affirmative("Yes").
					Negative("No").
					Value(&result),
			),
		).WithInput(in).WithOutput(out).WithShowHelp(false).Run()
		if err != nil {
			return def, err
		}
		return result, nil
	}
	// Plain-text fallback for tests and piped input.
	prompt := title
	if description != "" {
		prompt = description + " " + title
	}
	suffix := " (y/N): "
	if def {
		suffix = " (Y/n): "
	}
	fmt.Fprint(out, prompt+suffix)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return def, err
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return def, nil
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

// Select asks the user to pick one of several options. `def` is the value
// returned on EOF/non-TTY input with no answer.
func Select(in io.Reader, out io.Writer, title, description string, options []Option, def string) (string, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stderr
	}
	if isInteractive(in) {
		result := def
		huhOpts := make([]huh.Option[string], 0, len(options))
		for _, o := range options {
			huhOpts = append(huhOpts, huh.NewOption(o.Label, o.Value))
		}
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(title).
					Description(description).
					Options(huhOpts...).
					Value(&result),
			),
		).WithInput(in).WithOutput(out).WithShowHelp(false).Run()
		if err != nil {
			return def, err
		}
		return result, nil
	}
	// Plain-text fallback — accept either the label or the value.
	labels := make([]string, 0, len(options))
	for _, o := range options {
		labels = append(labels, o.Label)
	}
	fmt.Fprintf(out, "%s [%s]: ", title, strings.Join(labels, "/"))
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return def, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" {
		return def, nil
	}
	for _, o := range options {
		if strings.EqualFold(answer, o.Value) || strings.EqualFold(answer, o.Label) {
			return o.Value, nil
		}
		// Allow single-letter shortcuts when the value is unique on its first char.
		if len(answer) == 1 && strings.HasPrefix(strings.ToLower(o.Value), answer) {
			return o.Value, nil
		}
	}
	return def, nil
}

// Input asks for a free-text value, showing the current value as a default.
func Input(in io.Reader, out io.Writer, title, description, current string) (string, error) {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stderr
	}
	if isInteractive(in) {
		value := current
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(title).
					Description(description).
					Placeholder(current).
					Value(&value),
			),
		).WithInput(in).WithOutput(out).WithShowHelp(false).Run()
		if err != nil {
			return current, err
		}
		// huh returns "" if the user just submitted the placeholder; treat
		// that as "keep current".
		if value == "" {
			return current, nil
		}
		return value, nil
	}
	if current != "" {
		fmt.Fprintf(out, "%s [%s]: ", title, current)
	} else {
		fmt.Fprintf(out, "%s: ", title)
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return current, err
	}
	answer := strings.TrimRight(line, "\r\n")
	if answer == "" {
		return current, nil
	}
	return answer, nil
}
