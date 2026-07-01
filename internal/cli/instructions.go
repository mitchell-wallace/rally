package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/spf13/cobra"
)

func newInstructionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instructions",
		Short: "Manage project instructions",
	}
	cmd.AddCommand(newInstructionsEditCmd())
	cmd.AddCommand(newInstructionsShowCmd())
	return cmd
}

func newInstructionsEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Edit project instructions",
		RunE:  runInstructionsEdit,
	}
}

func newInstructionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show project instructions",
		RunE:  runInstructionsShow,
	}
}

func runInstructionsEdit(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	path := store.InstructionsPath(workspaceDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := "# Rally Project Instructions\n\n# Add persistent instructions for rally agents below.\n# These are included in every agent session prompt.\n"
		if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
			return err
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, path)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runInstructionsShow(cmd *cobra.Command, args []string) error {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		return err
	}
	path := store.InstructionsPath(workspaceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("(no project instructions set)")
			return nil
		}
		return err
	}
	fmt.Print(string(data))
	return nil
}
