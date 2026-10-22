package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("CLI task subcommands", func() {
	// findSubcommand returns the named subcommand or nil. Pinned in this
	// file (rather than a shared helper) so the spec body keeps its
	// single-test boundary.
	findSubcommand := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	getEmptyApp := func() *app.App { return &app.App{} }

	It("registers the list subcommand on the task root", func() {
		cmd := cli.NewTaskCmdForTest(getEmptyApp)
		Expect(cmd).NotTo(BeNil())
		Expect(findSubcommand(cmd, "list")).NotTo(BeNil())
	})

	It("registers the output subcommand with positional Args", func() {
		cmd := cli.NewTaskCmdForTest(getEmptyApp)
		Expect(cmd).NotTo(BeNil())

		outputCmd := findSubcommand(cmd, "output")
		Expect(outputCmd).NotTo(BeNil())
		Expect(outputCmd.Args).NotTo(BeNil())
	})

	It("registers the cancel subcommand", func() {
		cmd := cli.NewTaskCmdForTest(getEmptyApp)
		Expect(cmd).NotTo(BeNil())
		Expect(findSubcommand(cmd, "cancel")).NotTo(BeNil())
	})

	It("list prints 'No tasks' when the background manager is empty", func() {
		bgMgr := engine.NewBackgroundTaskManager()
		appPtr := &app.App{Engine: &engine.Engine{}}
		appPtr.SetBackgroundManager(bgMgr)

		cmd := cli.NewTaskListCmdForTest(func() *app.App { return appPtr })

		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		Expect(cmd.RunE(cmd, nil)).To(Succeed())
		Expect(out.String()).To(ContainSubstring("No tasks"))
	})

	It("output errors when the requested task does not exist", func() {
		bgMgr := engine.NewBackgroundTaskManager()
		appPtr := &app.App{Engine: &engine.Engine{}}
		appPtr.SetBackgroundManager(bgMgr)

		cmd := cli.NewTaskOutputCmdForTest(func() *app.App { return appPtr })

		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)
		cmd.SetArgs([]string{"nonexistent"})

		Expect(cmd.RunE(cmd, []string{"nonexistent"})).To(HaveOccurred())
	})

	It("cancel constructs a non-nil command", func() {
		bgMgr := engine.NewBackgroundTaskManager()
		appPtr := &app.App{Engine: &engine.Engine{}}
		appPtr.SetBackgroundManager(bgMgr)

		cmd := cli.NewTaskCancelCmdForTest(func() *app.App { return appPtr })
		Expect(cmd).NotTo(BeNil())
	})
})
