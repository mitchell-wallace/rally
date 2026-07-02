package cli

import (
	"fmt"
	"os"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/spf13/cobra"
)

var resolveWorkspaceDir = defaultResolveWorkspaceDir
var loadConfig = config.LoadV2

func NewRoutesCmd() *cobra.Command {
	routesCmd := &cobra.Command{
		Use:   "routes",
		Short: "Inspect route configuration",
	}

	checkCmd := &cobra.Command{
		Use:          "check",
		Short:        "Validate [routes] configuration",
		SilenceUsage: true,
		RunE:         runRoutesCheck,
	}

	routesCmd.AddCommand(checkCmd)
	return routesCmd
}

func runRoutesCheck(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}

	cfg, err := loadConfig(workspaceDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	result, err := CheckRoutes(workspaceDir, cfg)
	renderRouteCheckResult(cmd.OutOrStdout(), result)
	return err
}

func defaultResolveWorkspaceDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok, _ := gitx.GitRepoRoot(wd); ok {
		return root, nil
	}
	return wd, nil
}
