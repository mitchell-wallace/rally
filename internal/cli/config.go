package cli

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/spf13/cobra"
)

// NewConfigCmd returns the `rally config` command group.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure rally interactively",
		Long: `Interactively edit .rally/config.toml.

Walks through default models, default mix and iterations, role routing
fallbacks, reliability tuning, and custom harness aliases. Existing
values are shown in brackets; press Enter to keep them.`,
		RunE: runConfig,
	}
	return cmd
}

func runConfig(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	cfg, err := loadConfig(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Harnesses == nil {
		cfg.Harnesses = make(map[string]*config.HarnessConfig)
	}
	if cfg.Routes == nil {
		cfg.Routes = make(map[string][]string)
	}

	in := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "rally config — press Enter to keep the current value, type a new one to change it.")
	fmt.Fprintln(out)

	if err := promptDefaults(in, out, &cfg); err != nil {
		return err
	}
	if err := promptModels(in, out, &cfg); err != nil {
		return err
	}
	if err := promptRoutes(in, out, &cfg); err != nil {
		return err
	}
	if err := promptReliability(in, out, &cfg); err != nil {
		return err
	}
	if err := promptHarnessAliases(in, out, &cfg); err != nil {
		return err
	}

	fmt.Fprintln(out)
	confirm, err := readLine(in, out, "Save changes to .rally/config.toml?", "y")
	if err != nil {
		return err
	}
	if !isYes(confirm) {
		fmt.Fprintln(out, "Aborted; nothing written.")
		return nil
	}

	if err := config.SaveV2(workspaceDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(out, "Wrote .rally/config.toml.")
	return nil
}

func promptDefaults(in *bufio.Reader, out io.Writer, cfg *config.V2Config) error {
	section(out, "Defaults")

	mix, err := readLine(in, out, "Default agent mix", cfg.Defaults.Mix)
	if err != nil {
		return err
	}
	cfg.Defaults.Mix = mix

	itersStr, err := readLine(in, out, "Default iterations (0 = let rally decide)", strconv.Itoa(cfg.Defaults.Iterations))
	if err != nil {
		return err
	}
	if itersStr != "" {
		n, err := strconv.Atoi(itersStr)
		if err != nil {
			return fmt.Errorf("iterations must be a number: %w", err)
		}
		cfg.Defaults.Iterations = n
	}
	return nil
}

func promptModels(in *bufio.Reader, out io.Writer, cfg *config.V2Config) error {
	section(out, "Default models per harness")
	fields := []struct {
		label   string
		current *string
	}{
		{"claude_model", &cfg.ClaudeModel},
		{"codex_model", &cfg.CodexModel},
		{"gemini_model", &cfg.GeminiModel},
		{"opencode_model", &cfg.OpenCodeModel},
	}
	for _, f := range fields {
		val, err := readLine(in, out, f.label, *f.current)
		if err != nil {
			return err
		}
		*f.current = val
	}
	// Mirror onto Defaults so SaveV2 emits canonical values.
	cfg.Defaults.ClaudeModel = cfg.ClaudeModel
	cfg.Defaults.CodexModel = cfg.CodexModel
	cfg.Defaults.GeminiModel = cfg.GeminiModel
	cfg.Defaults.OpenCodeModel = cfg.OpenCodeModel
	return nil
}

func promptRoutes(in *bufio.Reader, out io.Writer, cfg *config.V2Config) error {
	section(out, "Role routing (fallbacks)")
	fmt.Fprintln(out, "Each role maps to an ordered list of harnesses/aliases. The first available")
	fmt.Fprintln(out, "harness in the list handles a lap with that assignee. Comma-separate entries.")
	fmt.Fprintln(out, "Common roles: default, junior, senior, ui, verify.")
	fmt.Fprintln(out)

	roles := []string{"default", "junior", "senior", "ui", "verify"}
	seen := map[string]bool{}
	for _, r := range roles {
		seen[r] = true
	}
	// Include any already-configured custom roles after the defaults.
	var custom []string
	for k := range cfg.Routes {
		if !seen[k] {
			custom = append(custom, k)
		}
	}
	sort.Strings(custom)
	roles = append(roles, custom...)

	for _, role := range roles {
		current := strings.Join(cfg.Routes[role], ", ")
		val, err := readLine(in, out, role, current)
		if err != nil {
			return err
		}
		if val == "" {
			// User cleared it; remove the role.
			delete(cfg.Routes, role)
			continue
		}
		entries := splitAndTrim(val, ',')
		cfg.Routes[role] = entries
	}

	// Allow adding a brand-new role.
	for {
		name, err := readLine(in, out, "Add another role (blank to finish)", "")
		if err != nil {
			return err
		}
		if name == "" {
			break
		}
		val, err := readLine(in, out, fmt.Sprintf("Route for %s", name), "")
		if err != nil {
			return err
		}
		if val == "" {
			continue
		}
		cfg.Routes[name] = splitAndTrim(val, ',')
	}
	return nil
}

func promptReliability(in *bufio.Reader, out io.Writer, cfg *config.V2Config) error {
	section(out, "Reliability")

	freeze, err := readLine(in, out, "freeze_threshold_secs (0 = use built-in default)", strconv.Itoa(cfg.Reliability.FreezeThresholdSecs))
	if err != nil {
		return err
	}
	if freeze != "" {
		n, err := strconv.Atoi(freeze)
		if err != nil {
			return fmt.Errorf("freeze_threshold_secs must be a number: %w", err)
		}
		cfg.Reliability.FreezeThresholdSecs = n
	}

	retry, err := readLine(in, out, "retry_budget", strconv.Itoa(cfg.Reliability.RetryBudget))
	if err != nil {
		return err
	}
	if retry != "" {
		n, err := strconv.Atoi(retry)
		if err != nil {
			return fmt.Errorf("retry_budget must be a number: %w", err)
		}
		cfg.Reliability.RetryBudget = n
	}

	probeDefault := "n"
	if cfg.Reliability.LivenessProbe {
		probeDefault = "y"
	}
	probe, err := readLine(in, out, "liveness_probe (y/n)", probeDefault)
	if err != nil {
		return err
	}
	cfg.Reliability.LivenessProbe = isYes(probe)
	return nil
}

func promptHarnessAliases(in *bufio.Reader, out io.Writer, cfg *config.V2Config) error {
	section(out, "Custom harness/model aliases")
	fmt.Fprintln(out, "Define short aliases for harness-and-model combos under [harness.<name>.models].")
	fmt.Fprintln(out, "Example: harness=op, alias=z, model=opencode-go/kimi-k2.6 lets you write `op:z`.")
	fmt.Fprintln(out)

	skip, err := readLine(in, out, "Edit harness aliases?", "n")
	if err != nil {
		return err
	}
	if !isYes(skip) {
		return nil
	}

	for {
		hname, err := readLine(in, out, "Harness name (blank to finish)", "")
		if err != nil {
			return err
		}
		if hname == "" {
			return nil
		}
		hc := cfg.Harnesses[hname]
		if hc == nil {
			hc = &config.HarnessConfig{Models: map[string]string{}}
		}
		if hc.Models == nil {
			hc.Models = map[string]string{}
		}
		for {
			alias, err := readLine(in, out, fmt.Sprintf("  Alias for %s (blank to stop adding aliases)", hname), "")
			if err != nil {
				return err
			}
			if alias == "" {
				break
			}
			model, err := readLine(in, out, fmt.Sprintf("  Model for %s:%s", hname, alias), hc.Models[alias])
			if err != nil {
				return err
			}
			if model == "" {
				delete(hc.Models, alias)
				continue
			}
			hc.Models[alias] = model
		}
		cfg.Harnesses[hname] = hc
	}
}

func section(out io.Writer, title string) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "── %s ──\n", title)
}

func readLine(in *bufio.Reader, out io.Writer, label, current string) (string, error) {
	if current != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, current)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil {
		if err == io.EOF && line == "" {
			return current, nil
		}
		if err != io.EOF {
			return "", err
		}
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return current, nil
	}
	return line, nil
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "true", "1":
		return true
	}
	return false
}

func splitAndTrim(s string, sep rune) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == sep })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
