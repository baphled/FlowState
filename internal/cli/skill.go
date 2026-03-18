package cli

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/spf13/cobra"
)

func newSkillCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Inspect available skills",
		Long:  "Inspect skills available to FlowState and its agents.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSkillListCmd(getApp), newSkillAddCmd(getApp))
	return cmd
}

func newSkillListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available skills",
		Long:  "List the skills available to FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillList(cmd, getApp())
		},
	}
}

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

func runSkillAdd(cmd *cobra.Command, application *app.App, ownerRepo string) error {
	importer := skill.NewImporter(application.SkillsDir())
	imported, err := importer.Add(context.Background(), ownerRepo)
	if err != nil {
		return fmt.Errorf("importing skill: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Imported skill: %s\n", imported.Name)
	return err
}
