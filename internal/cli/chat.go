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

// newChatCmd creates the chat command for interactive chat sessions.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with chat options.
//
// Side effects:
//   - Registers chat command flags.
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

// runChat routes to single-message or interactive chat based on options.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//   - opts is a non-nil ChatOptions.
//
// Returns:
//   - nil on success, or an error if chat execution fails.
//
// Side effects:
//   - Launches interactive chat or sends a single message.
func runChat(cmd *cobra.Command, application *app.App, opts *ChatOptions) error {
	if opts.Model != "" {
		if err := application.SetModel(opts.Model); err != nil {
			return fmt.Errorf("setting model: %w", err)
		}
	}

	if opts.Message != "" {
		return runSingleMessageChat(cmd, application, opts)
	}
	return runInteractiveChat(application, opts)
}

// runSingleMessageChat sends a single message and displays the response.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured engine.
//   - opts is a non-nil ChatOptions with a non-empty message.
//
// Returns:
//   - nil on success, or an error if streaming or output fails.
//
// Side effects:
//   - Writes message and response to stdout, saves session if available.
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

// runInteractiveChat launches the interactive TUI chat session.
//
// Expected:
//   - application is a non-nil App instance with a configured engine.
//   - opts is a non-nil ChatOptions.
//
// Returns:
//   - nil on success, or an error if TUI execution fails.
//
// Side effects:
//   - Launches the interactive TUI application.
func runInteractiveChat(application *app.App, opts *ChatOptions) error {
	if application.Engine == nil {
		return errors.New("engine not configured")
	}

	agentName := resolveChatAgentName(opts.Agent)
	sessionID := resolveChatSessionID(opts.Session)

	return tui.Run(application, agentName, sessionID)
}

// resolveChatAgentName returns the agent name, defaulting to "default" if empty.
//
// Expected:
//   - agent is a string (may be empty).
//
// Returns:
//   - The agent name, or "default" if agent is empty.
//
// Side effects:
//   - None.
func resolveChatAgentName(agent string) string {
	if agent == "" {
		return "default"
	}
	return agent
}

// resolveChatSessionID returns the session ID, generating a new one if empty.
//
// Expected:
//   - session is a string (may be empty).
//
// Returns:
//   - The session ID, or a newly generated one if session is empty.
//
// Side effects:
//   - None.
func resolveChatSessionID(session string) string {
	if session == "" {
		return generateSessionID()
	}
	return session
}

// loadSessionIfRequested loads a session into the engine if a session ID is provided.
//
// Expected:
//   - application is a non-nil App instance.
//   - session is a string (may be empty).
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Loads session into the engine if session is non-empty and sessions store is available.
func loadSessionIfRequested(application *app.App, session string) {
	if session != "" && application.Sessions != nil {
		store, loadErr := application.Sessions.Load(session)
		if loadErr == nil {
			application.Engine.SetContextStore(store)
		}
	}
}

// streamChatResponse streams a response from the engine and returns the complete message.
//
// Expected:
//   - application is a non-nil App instance with a configured engine.
//   - agentName is a non-empty string.
//   - message is a non-empty string.
//
// Returns:
//   - The complete response string and nil on success, or empty string and error on failure.
//
// Side effects:
//   - Streams response chunks from the engine.
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

// saveSessionIfAvailable saves the current session if the session store is available.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance.
//   - sessionID is a non-empty string.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Saves session to the store if available, writes warning to stderr on failure.
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

// generateSessionID creates a unique session ID based on the current timestamp.
//
// Expected:
//   - None.
//
// Returns:
//   - A unique session ID string.
//
// Side effects:
//   - None.
func generateSessionID() string {
	return fmt.Sprintf("session-%d", time.Now().UnixNano())
}
