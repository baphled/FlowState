package cli

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/skill"
	"github.com/spf13/cobra"
)

func newSkillCmd(opts *RootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Inspect available skills",
		Long:  "Inspect skills available to FlowState and its agents.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSkillListCmd(opts), newSkillAddCmd(opts))
	return cmd
}

func newSkillListCmd(opts *RootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available skills",
		Long:  "List the skills available to FlowState.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillList(cmd, opts)
		},
	}
}

func runSkillList(cmd *cobra.Command, opts *RootOptions) error {
	loader := skill.NewFileSkillLoader(opts.SkillsDir)
	skills, err := loader.LoadAll()
	if err != nil {
		return fmt.Errorf("loading skills: %w", err)
	}

	if len(skills) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No skills found.")
		return err
	}

	for _, s := range skills {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [%s]: %s\n", s.Name, s.Tier, s.Description)
		if err != nil {
			return err
		}
	}
	return nil
}

func newSkillAddCmd(opts *RootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "add OWNER/REPO",
		Short: "Import a skill from GitHub",
		Long:  "Import a skill from a GitHub repository.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillAdd(cmd, opts, args[0])
		},
	}
}

func runSkillAdd(cmd *cobra.Command, opts *RootOptions, ownerRepo string) error {
	importer := skill.NewImporter(opts.SkillsDir)
	imported, err := importer.Add(context.Background(), ownerRepo)
	if err != nil {
		return fmt.Errorf("importing skill: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Imported skill: %s\n", imported.Name)
	return err
}
