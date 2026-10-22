package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// newTaskCmd creates the task command group for managing background delegation tasks.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with task subcommands.
//
// Side effects:
//   - Registers the task list, output, and cancel subcommands.
func newTaskCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage background delegation tasks",
		Long:  "List, inspect, and cancel background delegation tasks.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newTaskListCmd(getApp),
		newTaskOutputCmd(getApp),
		newTaskCancelCmd(getApp),
	)

	return cmd
}

// newTaskListCmd creates the task list subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for listing background tasks.
//
// Side effects:
//   - None.
func newTaskListCmd(getApp func() *app.App) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List background tasks",
		Long:  "List all active and completed background delegation tasks.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a := getApp()
			bgMgr := a.BackgroundManager()
			if bgMgr == nil {
				return errors.New("no background manager configured")
			}

			tasks := bgMgr.List()
			if len(tasks) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No tasks")
				return err
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(tasks)
			}

			return printTaskList(cmd.OutOrStdout(), tasks)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// newTaskOutputCmd creates the task output subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for retrieving task output.
//
// Side effects:
//   - None.
func newTaskOutputCmd(getApp func() *app.App) *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "output ID",
		Short: "Get background task output",
		Long:  "Retrieve the output and status of a specific background task.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := getApp()
			bgMgr := a.BackgroundManager()
			if bgMgr == nil {
				return errors.New("no background manager configured")
			}

			task, ok := bgMgr.Get(args[0])
			if !ok {
				return fmt.Errorf("task not found: %s", args[0])
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(task)
			}

			return printTask(cmd.OutOrStdout(), task)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// newTaskCancelCmd creates the task cancel subcommand.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command for cancelling tasks.
//
// Side effects:
//   - None.
func newTaskCancelCmd(getApp func() *app.App) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "cancel [ID]",
		Short: "Cancel a background task",
		Long:  "Cancel a specific background task, or all tasks if --all is specified.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a := getApp()
			bgMgr := a.BackgroundManager()
			if bgMgr == nil {
				return errors.New("no background manager configured")
			}

			if all {
				cancelled := bgMgr.CancelAll()
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Cancelled %d task(s)\n", len(cancelled))
				return err
			}

			if len(args) == 0 {
				return errors.New("must provide task ID or use --all")
			}

			if err := bgMgr.Cancel(args[0]); err != nil {
				return fmt.Errorf("cancelling task: %w", err)
			}

			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Cancelled task: %s\n", args[0])
			return err
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Cancel all tasks")
	return cmd
}

// printTaskList outputs a formatted list of tasks.
//
// Expected:
//   - w is a non-nil io.Writer.
//   - tasks is a non-nil slice of background tasks.
//
// Returns:
//   - An error if writing fails.
//
// Side effects:
//   - Writes task list to the provided writer.
func printTaskList(w io.Writer, tasks interface{}) error {
	taskData, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling tasks: %w", err)
	}
	_, err = fmt.Fprintln(w, string(taskData))
	return err
}

// printTask outputs a single task.
//
// Expected:
//   - w is a non-nil io.Writer.
//   - task is the background task to display.
//
// Returns:
//   - An error if writing fails.
//
// Side effects:
//   - Writes task details to the provided writer.
func printTask(w io.Writer, task interface{}) error {
	taskData, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling task: %w", err)
	}
	_, err = fmt.Fprintln(w, string(taskData))
	return err
}
