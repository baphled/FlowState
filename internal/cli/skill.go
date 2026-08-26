package cli

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/spf13/cobra"
)

// newSkillCmd creates the skill command for inspecting available skills.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with skill subcommands.
//
// Side effects:
//   - Registers the skill list and add subcommands.
func newSkillCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Inspect available skills",
		Long:  "Inspect skills available to FlowState and its agents.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSkillListCmd(getApp), newSkillAddCmd(getApp))
	return cmd
}

// newSkillListCmd creates the skill list subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing skills.
//
// Side effects:
//   - None.
func newSkillListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available skills",
		Long:  "List the skills available to FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillList(cmd, getApp())
		},
	}
}

// runSkillList lists all available skills.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a populated skills list.
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes skills list to stdout.
func runSkillList(cmd *cobra.Command, application *app.App) error {
	skills := application.Skills
	if len(skills) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No skills found.")
		return err
	}

	for i := range skills {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [%s]: %s\n", skills[i].Name, skills[i].Tier, skills[i].Description)
		if err != nil {
			return err
		}
	}
	return nil
}

// newSkillAddCmd creates the skill add subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for adding skills.
//
// Side effects:
//   - None.
func newSkillAddCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "add OWNER/REPO",
		Short: "Import a skill from GitHub",
		Long:  "Import a skill from a GitHub repository.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillAdd(cmd, getApp(), args[0])
		},
	}
}

// runSkillAdd imports a skill from a GitHub repository.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured skills directory.
//   - ownerRepo is a non-empty string in the format "owner/repo".
//
// Returns:
//   - nil on success, or an error if import fails.
//
// Side effects:
//   - Imports skill from GitHub, writes confirmation to stdout.
func runSkillAdd(cmd *cobra.Command, application *app.App, ownerRepo string) error {
	importer := skill.NewImporter(application.SkillsDir())
	imported, err := importer.Add(context.Background(), ownerRepo)
	if err != nil {
		return fmt.Errorf("importing skill: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Imported skill: %s\n", imported.Name)
	return err
}
