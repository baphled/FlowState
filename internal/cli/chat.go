package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

type ChatOptions struct {
	Agent   string
	Message string
	Model   string
	Session string
}

func newChatCmd(application *app.App) *cobra.Command {
	opts := &ChatOptions{}

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive chat session",
		Long:  "Start an interactive chat session from the CLI.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd, application, opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Agent, "agent", "", "Agent to use for the chat session")
	flags.StringVar(&opts.Message, "message", "", "Initial message to send")
	flags.StringVar(&opts.Model, "model", "", "Model to use for the chat session")
	flags.StringVar(&opts.Session, "session", "", "Session ID to resume")

	return cmd
}

func runChat(cmd *cobra.Command, application *app.App, opts *ChatOptions) error {
	if opts.Message != "" {
		agentName := opts.Agent
		if agentName == "" {
			agentName = "default"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", agentName, opts.Message)
		if err != nil {
			return err
		}

		if application.Engine == nil {
			return errors.New("engine not configured")
		}

		ctx := context.Background()
		chunks, err := application.Engine.Stream(ctx, agentName, opts.Message)
		if err != nil {
			return fmt.Errorf("streaming response: %w", err)
		}

		var response strings.Builder
		for chunk := range chunks {
			if chunk.Error != nil {
				return fmt.Errorf("stream error: %w", chunk.Error)
			}
			response.WriteString(chunk.Content)
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", response.String())
		return err
	}

	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Starting interactive chat... (TUI not wired yet)")
	return err
}
