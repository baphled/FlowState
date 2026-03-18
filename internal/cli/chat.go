package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/spf13/cobra"
)

// ChatOptions holds configuration for the chat command.
type ChatOptions struct {
	Agent   string
	Message string
	Model   string
	Session string
}

func newChatCmd(getApp func() *app.App) *cobra.Command {
	opts := &ChatOptions{}

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive chat session",
		Long:  "Start an interactive chat session from the CLI.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChat(cmd, getApp(), opts)
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
		return runSingleMessageChat(cmd, application, opts)
	}
	return runInteractiveChat(application, opts)
}

func runSingleMessageChat(cmd *cobra.Command, application *app.App, opts *ChatOptions) error {
	agentName := resolveChatAgentName(opts.Agent)

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", agentName, opts.Message)
	if err != nil {
		return err
	}

	if application.Engine == nil {
		return errors.New("engine not configured")
	}

	sessionID := resolveChatSessionID(opts.Session)
	loadSessionIfRequested(application, opts.Session)

	response, err := streamChatResponse(application, agentName, opts.Message)
	if err != nil {
		return err
	}

	saveSessionIfAvailable(cmd, application, sessionID)

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", response)
	return err
}

func runInteractiveChat(application *app.App, opts *ChatOptions) error {
	if application.Engine == nil {
		return errors.New("engine not configured")
	}

	agentName := resolveChatAgentName(opts.Agent)
	sessionID := resolveChatSessionID(opts.Session)

	return tui.Run(application.Engine, agentName, sessionID)
}

func resolveChatAgentName(agent string) string {
	if agent == "" {
		return "default"
	}
	return agent
}

func resolveChatSessionID(session string) string {
	if session == "" {
		return generateSessionID()
	}
	return session
}

func loadSessionIfRequested(application *app.App, session string) {
	if session != "" && application.Sessions != nil {
		store, loadErr := application.Sessions.Load(session)
		if loadErr == nil {
			application.Engine.SetContextStore(store)
		}
	}
}

func streamChatResponse(application *app.App, agentName string, message string) (string, error) {
	ctx := context.Background()
	chunks, err := application.Engine.Stream(ctx, agentName, message)
	if err != nil {
		return "", fmt.Errorf("streaming response: %w", err)
	}

	var response strings.Builder
	for chunk := range chunks {
		if chunk.Error != nil {
			return "", fmt.Errorf("stream error: %w", chunk.Error)
		}
		response.WriteString(chunk.Content)
	}
	return response.String(), nil
}

func saveSessionIfAvailable(cmd *cobra.Command, application *app.App, sessionID string) {
	if application.Sessions != nil {
		store := application.Engine.ContextStore()
		if store != nil {
			if saveErr := application.Sessions.Save(sessionID, store); saveErr != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to save session: %v\n", saveErr)
			}
		}
	}
}

func generateSessionID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}
