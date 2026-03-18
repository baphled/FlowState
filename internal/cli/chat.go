package cli

import "github.com/spf13/cobra"

// ChatOptions stores chat flag values.
type ChatOptions struct {
	Agent   string
	Message string
	Model   string
	Session string
}

func newChatCmd() *cobra.Command {
	opts := &ChatOptions{}

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive chat session",
		Long:  "Start an interactive chat session from the CLI. This stub reports the selected chat options.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Agent, "agent", "", "Agent to use for the chat session")
	flags.StringVar(&opts.Message, "message", "", "Initial message to send")
	flags.StringVar(&opts.Model, "model", "", "Model to use for the chat session")
	flags.StringVar(&opts.Session, "session", "", "Session ID to resume")

	return cmd
}

func runChat(cmd *cobra.Command, opts *ChatOptions) error {
	return writePlaceholder(
		cmd,
		"chat stub: agent=%q message=%q model=%q session=%q\n",
		opts.Agent,
		opts.Message,
		opts.Model,
		opts.Session,
	)
}
