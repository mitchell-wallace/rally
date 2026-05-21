package cli

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mitchell-wallace/rally/internal/config"
)

// NewConfigCmd returns the `rally config` command group.
func NewConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Configure rally interactively",
		Long: `Interactively edit .rally/config.toml.

Walks through defaults (mix, iterations), default models per harness,
paths (data dir, instructions files), role routing fallbacks, reliability
tuning, and any custom harness aliases. Existing values are pre-filled in
each form; leave them as-is to keep, or edit in place.`,
		RunE: runConfig,
	}
}

func runConfig(cmd *cobra.Command, _ []string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("rally config is interactive and requires a terminal; edit .rally/config.toml directly for non-interactive use")
	}
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

	// Build editable copies for huh fields; mutate cfg only after the form
	// completes so an aborted form leaves the on-disk config untouched.
	mix := cfg.Defaults.Mix
	iterations := strconv.Itoa(cfg.Defaults.Iterations)
	claudeModel := cfg.ClaudeModel
	codexModel := cfg.CodexModel
	geminiModel := cfg.GeminiModel
	openCodeModel := cfg.OpenCodeModel
	antigravityModel := cfg.AntigravityModel

	dataDir := cfg.DataDir
	lapsInstructions := cfg.Laps.InstructionsFile
	fallbackInstructions := cfg.Fallback.InstructionsFile
	runHooksOnAutoCommit := cfg.RunHooksOnAutoCommit

	freezeStr := strconv.Itoa(cfg.Reliability.FreezeThresholdSecs)
	retryStr := strconv.Itoa(cfg.Reliability.RetryBudget)
	livenessProbe := cfg.Reliability.LivenessProbe

	// Parallel slices so each huh field can bind directly to &routeValues[i]
	// and we can read the user's edits back after the form runs.
	roleNames := []string{"default", "junior", "senior", "ui", "verify"}
	roleSet := map[string]bool{}
	for _, r := range roleNames {
		roleSet[r] = true
	}
	for name := range cfg.Routes {
		if !roleSet[name] {
			roleNames = append(roleNames, name)
			roleSet[name] = true
		}
	}
	sort.Strings(roleNames[5:])
	routeValues := make([]string, len(roleNames))
	for i, r := range roleNames {
		routeValues[i] = strings.Join(cfg.Routes[r], ", ")
	}

	editHarnesses := false
	editCustomRoles := false
	saveChanges := true

	defaultsGroup := huh.NewGroup(
		huh.NewNote().Title("rally config").Description("Press Tab to move between fields. Submit each form to advance."),
		huh.NewInput().Title("Default agent mix").Description("Used when --agent isn't passed. e.g. \"cc cx\" or \"cc:2,cx:1\".").Value(&mix),
		huh.NewInput().Title("Default iterations").Description("0 lets rally pick a sensible default (50 free-form, queue-bounded with laps).").Value(&iterations).Validate(validateOptionalInt),
	)

	modelsGroup := huh.NewGroup(
		huh.NewNote().Title("Default models").Description("Leave blank to inherit the harness's own default."),
		huh.NewInput().Title("claude_model").Value(&claudeModel),
		huh.NewInput().Title("codex_model").Value(&codexModel),
		huh.NewInput().Title("gemini_model").Value(&geminiModel),
		huh.NewInput().Title("opencode_model").Value(&openCodeModel),
		huh.NewInput().Title("antigravity_model").Value(&antigravityModel),
	)

	pathsGroup := huh.NewGroup(
		huh.NewNote().Title("Paths & toggles").Description("Optional paths and behaviour flags."),
		huh.NewInput().Title("data_dir").Description("Where rally writes try logs and relay records. Blank = ~/.local/share/rally.").Value(&dataDir),
		huh.NewInput().Title("laps.instructions_file").Description("Extra instructions injected when a lap is assigned.").Value(&lapsInstructions),
		huh.NewInput().Title("fallback.instructions_file").Description("Instructions used when no laps queue exists.").Value(&fallbackInstructions),
		huh.NewConfirm().Title("run_hooks_on_autocommit").Description("Run git hooks (e.g. pre-commit) on rally's automatic commits.").Affirmative("Yes").Negative("No").Value(&runHooksOnAutoCommit),
	)

	routeFields := []huh.Field{
		huh.NewNote().Title("Role routing").Description("Each role maps to an ordered list of harnesses/aliases. Comma-separate entries. Clear a value to remove the role."),
	}
	for i, role := range roleNames {
		routeFields = append(routeFields, huh.NewInput().Title(role).Value(&routeValues[i]))
	}
	routeFields = append(routeFields, huh.NewConfirm().Title("Add a custom role afterwards?").Affirmative("Yes").Negative("No").Value(&editCustomRoles))
	routesGroup := huh.NewGroup(routeFields...)

	reliabilityGroup := huh.NewGroup(
		huh.NewNote().Title("Reliability").Description("Freeze detection and retry behaviour."),
		huh.NewInput().Title("freeze_threshold_secs").Description("Log silence (seconds) before treating an agent as frozen.").Value(&freezeStr).Validate(validateOptionalInt),
		huh.NewInput().Title("retry_budget").Description("Per-run retry budget before a try is marked failed.").Value(&retryStr).Validate(validateOptionalInt),
		huh.NewConfirm().Title("liveness_probe").Description("Send a periodic check to detect connection drops.").Affirmative("On").Negative("Off").Value(&livenessProbe),
	)

	harnessGroup := huh.NewGroup(
		huh.NewConfirm().Title("Edit custom harness aliases?").Description("Add or edit [harness.<name>] entries — short labels, custom commands, model maps.").Affirmative("Yes").Negative("No").Value(&editHarnesses),
	)

	saveGroup := huh.NewGroup(
		huh.NewConfirm().Title("Save changes to .rally/config.toml?").Affirmative("Save").Negative("Discard").Value(&saveChanges),
	)

	form := huh.NewForm(defaultsGroup, modelsGroup, pathsGroup, routesGroup, reliabilityGroup, harnessGroup, saveGroup).
		WithShowHelp(false).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.OutOrStderr())
	if err := form.Run(); err != nil {
		return err
	}

	if !saveChanges {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted; nothing written.")
		return nil
	}

	// Apply edits back onto cfg now that the user has confirmed.
	cfg.Defaults.Mix = strings.TrimSpace(mix)
	if n, ok := parseIntDefault(iterations, cfg.Defaults.Iterations); ok {
		cfg.Defaults.Iterations = n
	}
	cfg.ClaudeModel = strings.TrimSpace(claudeModel)
	cfg.CodexModel = strings.TrimSpace(codexModel)
	cfg.GeminiModel = strings.TrimSpace(geminiModel)
	cfg.OpenCodeModel = strings.TrimSpace(openCodeModel)
	cfg.AntigravityModel = strings.TrimSpace(antigravityModel)
	cfg.Defaults.ClaudeModel = cfg.ClaudeModel
	cfg.Defaults.CodexModel = cfg.CodexModel
	cfg.Defaults.GeminiModel = cfg.GeminiModel
	cfg.Defaults.OpenCodeModel = cfg.OpenCodeModel
	cfg.Defaults.AntigravityModel = cfg.AntigravityModel

	cfg.DataDir = strings.TrimSpace(dataDir)
	cfg.Laps.InstructionsFile = strings.TrimSpace(lapsInstructions)
	cfg.Fallback.InstructionsFile = strings.TrimSpace(fallbackInstructions)
	cfg.RunHooksOnAutoCommit = runHooksOnAutoCommit

	if n, ok := parseIntDefault(freezeStr, cfg.Reliability.FreezeThresholdSecs); ok {
		cfg.Reliability.FreezeThresholdSecs = n
	}
	if n, ok := parseIntDefault(retryStr, cfg.Reliability.RetryBudget); ok {
		cfg.Reliability.RetryBudget = n
	}
	cfg.Reliability.LivenessProbe = livenessProbe

	for i, role := range roleNames {
		v := strings.TrimSpace(routeValues[i])
		if v == "" {
			delete(cfg.Routes, role)
			continue
		}
		cfg.Routes[role] = splitCSV(v)
	}

	if editCustomRoles {
		if err := promptCustomRoles(cmd, cfg.Routes); err != nil {
			return err
		}
	}
	if editHarnesses {
		if err := promptHarnesses(cmd, cfg); err != nil {
			return err
		}
	}

	if err := config.SaveV2(workspaceDir, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Wrote .rally/config.toml.")
	return nil
}

// promptCustomRoles loops until the user stops adding new role entries.
func promptCustomRoles(cmd *cobra.Command, routes map[string][]string) error {
	for {
		var name, list string
		var addAnother bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("New role name").Description("e.g. planner, reviewer. Blank to stop.").Value(&name),
				huh.NewInput().Title("Route").Description("Ordered harnesses/aliases, comma-separated.").Value(&list),
				huh.NewConfirm().Title("Add another after this one?").Affirmative("Yes").Negative("No").Value(&addAnother),
			),
		).WithShowHelp(false).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStderr())
		if err := form.Run(); err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		entries := splitCSV(list)
		if len(entries) > 0 {
			routes[name] = entries
		}
		if !addAnother {
			return nil
		}
	}
}

// promptHarnesses lets the user add/edit [harness.<name>] entries, including
// the full custom-harness fields (command, model_flag, output_*) and the
// per-harness model alias map.
func promptHarnesses(cmd *cobra.Command, cfg config.V2Config) error {
	for {
		// Step 1: pick the harness name (or stop).
		var name string
		nameForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("Harness name").Description("Built-in: antigravity/claude/codex/gemini/opencode. Or invent a new one. Blank to stop.").Value(&name),
			),
		).WithShowHelp(false).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStderr())
		if err := nameForm.Run(); err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		hc := cfg.Harnesses[name]
		if hc == nil {
			hc = &config.HarnessConfig{Models: map[string]string{}}
		}
		if hc.Models == nil {
			hc.Models = map[string]string{}
		}

		// Step 2: command + output settings (only meaningful for non-built-in).
		commandStr := strings.Join(hc.Command, " ")
		modelFlag := ""
		if hc.ModelFlag != nil {
			modelFlag = *hc.ModelFlag
		}
		outputStrategy := hc.OutputStrategy
		outputLines := strconv.Itoa(hc.OutputLines)
		tailStream := hc.TailStream

		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewNote().Title(name).Description("Leave blank to skip; only set when you need a fully custom harness CLI."),
				huh.NewInput().Title("command").Description(`Space-separated; "$PROMPT" expands to the task. e.g. opencode run "$PROMPT" --format json`).Value(&commandStr),
				huh.NewInput().Title("model_flag").Description("Flag passed before the resolved model, e.g. --model.").Value(&modelFlag),
				huh.NewSelect[string]().Title("output_strategy").Description("How rally summarises agent output.").Options(
					huh.NewOption("(keep)", outputStrategy),
					huh.NewOption("tail", "tail"),
					huh.NewOption("stream", "stream"),
					huh.NewOption("none", "none"),
				).Value(&outputStrategy),
				huh.NewInput().Title("output_lines").Description("Lines to keep when output_strategy=tail.").Value(&outputLines).Validate(validateOptionalInt),
				huh.NewSelect[string]().Title("tail_stream").Description("Which stream to tail.").Options(
					huh.NewOption("(keep)", tailStream),
					huh.NewOption("stdout", "stdout"),
					huh.NewOption("stderr", "stderr"),
					huh.NewOption("combined", "combined"),
				).Value(&tailStream),
			),
		).WithShowHelp(false).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStderr()).Run(); err != nil {
			return err
		}

		if commandStr = strings.TrimSpace(commandStr); commandStr != "" {
			hc.Command = strings.Fields(commandStr)
		}
		if modelFlag = strings.TrimSpace(modelFlag); modelFlag != "" {
			f := modelFlag
			hc.ModelFlag = &f
		}
		hc.OutputStrategy = strings.TrimSpace(outputStrategy)
		if n, err := strconv.Atoi(strings.TrimSpace(outputLines)); err == nil {
			hc.OutputLines = n
		}
		hc.TailStream = strings.TrimSpace(tailStream)

		// Step 3: model alias loop.
		for {
			var alias, model string
			var another bool
			aliasForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().Title(fmt.Sprintf("Alias under %s", name)).Description("Short label you'll write as harness:alias. Blank to stop.").Value(&alias),
					huh.NewInput().Title("Model").Description("Full provider/model slug to map to this alias.").Value(&model),
					huh.NewConfirm().Title("Another alias for this harness?").Affirmative("Yes").Negative("No").Value(&another),
				),
			).WithShowHelp(false).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStderr())
			if err := aliasForm.Run(); err != nil {
				return err
			}
			alias = strings.TrimSpace(alias)
			if alias == "" {
				break
			}
			if model = strings.TrimSpace(model); model == "" {
				delete(hc.Models, alias)
			} else {
				hc.Models[alias] = model
			}
			if !another {
				break
			}
		}

		cfg.Harnesses[name] = hc

		var addAnotherHarness bool
		if err := huh.NewForm(
			huh.NewGroup(huh.NewConfirm().Title("Configure another harness?").Affirmative("Yes").Negative("No").Value(&addAnotherHarness)),
		).WithShowHelp(false).WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStderr()).Run(); err != nil {
			return err
		}
		if !addAnotherHarness {
			return nil
		}
	}
}

func splitCSV(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseIntDefault(s string, def int) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def, false
	}
	return n, true
}

func validateOptionalInt(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := strconv.Atoi(s); err != nil {
		return fmt.Errorf("must be a number")
	}
	return nil
}
