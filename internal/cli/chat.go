package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type ChatOptions struct {
	Agent   string
	Message string
	Model   string
	Session string
}

func newChatCmd(rootOpts *RootOptions) *cobra.Command {
	opts := &ChatOptions{}

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive chat session",
		Long:  "Start an interactive chat session from the CLI.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd, rootOpts, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Agent, "agent", "", "Agent to use for the chat session")
	flags.StringVar(&opts.Message, "message", "", "Initial message to send")
	flags.StringVar(&opts.Model, "model", "", "Model to use for the chat session")
	flags.StringVar(&opts.Session, "session", "", "Session ID to resume")

	return cmd
}

func runChat(cmd *cobra.Command, _ *RootOptions, opts *ChatOptions) error {
	if opts.Message != "" {
		agentName := opts.Agent
		if agentName == "" {
			agentName = "default"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", agentName, opts.Message)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Response: (placeholder - engine not wired)")
		return err
	}

	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Starting interactive chat... (TUI not wired yet)")
	return err
}
