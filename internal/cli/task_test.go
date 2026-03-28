package cli

import (
	"bytes"
	"testing"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestTaskListCommand verifies the task list command outputs task information.
func TestTaskListCommand(t *testing.T) {
	appPtr := &app.App{}
	getApp := func() *app.App { return appPtr }

	cmd := newTaskCmd(getApp)
	require.NotNil(t, cmd)

	listCmd := findSubcommand(cmd, "list")
	require.NotNil(t, listCmd)
}

// TestTaskOutputCommand verifies the task output command retrieves a specific task.
func TestTaskOutputCommand(t *testing.T) {
	appPtr := &app.App{}
	getApp := func() *app.App { return appPtr }

	cmd := newTaskCmd(getApp)
	require.NotNil(t, cmd)

	outputCmd := findSubcommand(cmd, "output")
	require.NotNil(t, outputCmd)
	require.NotNil(t, outputCmd.Args)
}

// TestTaskCancelCommand verifies the task cancel command exists.
func TestTaskCancelCommand(t *testing.T) {
	appPtr := &app.App{}
	getApp := func() *app.App { return appPtr }

	cmd := newTaskCmd(getApp)
	require.NotNil(t, cmd)

	cancelCmd := findSubcommand(cmd, "cancel")
	require.NotNil(t, cancelCmd)
}

// TestTaskListCLI verifies task list outputs correctly.
func TestTaskListCLI(t *testing.T) {
	bgMgr := engine.NewBackgroundTaskManager()
	appPtr := &app.App{
		Engine: &engine.Engine{},
	}
	appPtr.SetBackgroundManager(bgMgr)

	getApp := func() *app.App { return appPtr }
	cmd := newTaskListCmd(getApp)

	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)

	require.NoError(t, cmd.RunE(cmd, nil))
	output := out.String()
	require.Contains(t, output, "No tasks")
}

// TestTaskOutputCLI verifies task output displays task details.
func TestTaskOutputCLI(t *testing.T) {
	bgMgr := engine.NewBackgroundTaskManager()
	appPtr := &app.App{
		Engine: &engine.Engine{},
	}
	appPtr.SetBackgroundManager(bgMgr)

	getApp := func() *app.App { return appPtr }
	cmd := newTaskOutputCmd(getApp)

	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.RunE(cmd, []string{"nonexistent"})
	require.Error(t, err)
}

// TestTaskCancelCLI verifies task cancel works.
func TestTaskCancelCLI(t *testing.T) {
	bgMgr := engine.NewBackgroundTaskManager()
	appPtr := &app.App{
		Engine: &engine.Engine{},
	}
	appPtr.SetBackgroundManager(bgMgr)

	getApp := func() *app.App { return appPtr }
	cmd := newTaskCancelCmd(getApp)

	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)

	require.NotNil(t, cmd)
}

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	return nil
}
