package cli

import (
	"fmt"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/spf13/cobra"
)

// NewPlanCommand creates the plan command for managing plans.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with plan subcommands.
//
// Side effects:
//   - Registers the plan list, select, and delete subcommands.
func NewPlanCommand(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Manage plans",
		Long:  "Create, list, select, and delete plans.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newPlanListCmd(getApp), newPlanSelectCmd(getApp), newPlanDeleteCmd(getApp))
	return cmd
}

// newPlanListCmd creates the plan list subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing plans.
//
// Side effects:
//   - None.
func newPlanListCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all plans",
		Long:  "List all available plans.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := getApp()
			planDir := filepath.Join(a.Config.DataDir, "plans")

			store, err := plan.NewPlanStore(planDir)
			if err != nil {
				return fmt.Errorf("creating plan store: %w", err)
			}

			summaries, err := store.List()
			if err != nil {
				return fmt.Errorf("listing plans: %w", err)
			}

			if len(summaries) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No plans yet.")
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "ID\tTitle\tStatus\tCreatedAt")
			if err != nil {
				return err
			}

			for _, s := range summaries {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n",
					s.ID, s.Title, s.Status, s.CreatedAt.Format("2006-01-02 15:04"))
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// newPlanSelectCmd creates the plan select subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for selecting plans.
//
// Side effects:
//   - None.
func newPlanSelectCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "select ID",
		Short: "Select and display a plan",
		Long:  "Select a plan by ID and display its full content.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return displayPlan(cmd, getApp(), args[0])
		},
	}
}

// displayPlan retrieves and prints a plan to stdout.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - a is a non-nil App instance.
//   - planID is a non-empty string.
//
// Returns:
//   - error if plan retrieval or output fails.
//
// Side effects:
//   - Writes plan content to cmd.OutOrStdout().
func displayPlan(cmd *cobra.Command, a *app.App, planID string) error {
	planDir := filepath.Join(a.Config.DataDir, "plans")

	store, err := plan.NewPlanStore(planDir)
	if err != nil {
		return fmt.Errorf("creating plan store: %w", err)
	}

	planFile, err := store.Get(planID)
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	if err := writePlanHeader(cmd, planFile); err != nil {
		return err
	}

	if err := writePlanDescription(cmd, planFile); err != nil {
		return err
	}

	if err := writePlanStatus(cmd, planFile); err != nil {
		return err
	}

	if err := writePlanTasks(cmd, planFile); err != nil {
		return err
	}

	return nil
}

// writePlanHeader writes the plan title to output.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - planFile is a non-nil plan.File.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writePlanHeader(cmd *cobra.Command, planFile *plan.File) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n", planFile.Title)
	return err
}

// writePlanDescription writes the plan description if present.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - planFile is a non-nil plan.File.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writePlanDescription(cmd *cobra.Command, planFile *plan.File) error {
	if planFile.Description == "" {
		return nil
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n", planFile.Description)
	return err
}

// writePlanStatus writes the plan status to output.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - planFile is a non-nil plan.File.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writePlanStatus(cmd *cobra.Command, planFile *plan.File) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "**Status**: %s\n", planFile.Status)
	return err
}

// writePlanTasks writes the plan tasks if any exist.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - planFile is a non-nil plan.File.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writePlanTasks(cmd *cobra.Command, planFile *plan.File) error {
	if len(planFile.Tasks) == 0 {
		return nil
	}

	_, err := fmt.Fprintln(cmd.OutOrStdout(), "\n## Tasks")
	if err != nil {
		return err
	}

	for i := range planFile.Tasks {
		t := &planFile.Tasks[i]
		if err := writeTaskHeader(cmd, *t); err != nil {
			return err
		}
		if err := writeTaskDescription(cmd, *t); err != nil {
			return err
		}
	}

	return nil
}

// writeTaskHeader writes a task title to output.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - t is a plan.Task.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writeTaskHeader(cmd *cobra.Command, t plan.Task) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "### %s\n", t.Title)
	return err
}

// writeTaskDescription writes a task description to output if present.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - t is a plan.Task.
//
// Returns:
//   - error if output fails.
//
// Side effects:
//   - Writes to cmd.OutOrStdout().
func writeTaskDescription(cmd *cobra.Command, t plan.Task) error {
	if t.Description == "" {
		return nil
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n", t.Description)
	return err
}

// newPlanDeleteCmd creates the plan delete subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for deleting plans.
//
// Side effects:
//   - None.
func newPlanDeleteCmd(getApp func() *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete ID",
		Short: "Delete a plan",
		Long:  "Delete a plan by ID.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := getApp()
			planDir := filepath.Join(a.Config.DataDir, "plans")
			planID := args[0]

			store, err := plan.NewPlanStore(planDir)
			if err != nil {
				return fmt.Errorf("creating plan store: %w", err)
			}

			if err := store.Delete(planID); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Plan %q deleted.\n", planID)
			return err
		},
	}
}
