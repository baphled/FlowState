package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/baphled/flowstate/internal/app"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui"
	"github.com/spf13/cobra"
)

// ChatOptions holds configuration for the chat command.
type ChatOptions struct {
	Agent   string
	Message string
	Model   string
	Session string
	Output  string
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
	flags.StringVar(&opts.Output, "output", "text", "Output format: text or json")

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
	agentName := resolveChatAgentName(opts.Agent, application.Config.DefaultAgent)

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", agentName, opts.Message)
	if err != nil {
		return err
	}

	if application.Streamer == nil {
		return errors.New("engine not configured")
	}

	sessionID := resolveChatSessionID(opts.Session)
	loadSessionIfRequested(application, opts.Session)

	response, err := streamChatResponse(application, agentName, opts.Message, opts.Output, io.Discard)
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

	agentName := resolveChatAgentName(opts.Agent, application.Config.DefaultAgent)
	sessionID := resolveChatSessionID(opts.Session)

	return tui.Run(application, agentName, sessionID)
}

// resolveChatAgentName returns the agent name, defaulting to the provided defaultAgent if empty.
//
// Expected:
//   - agent is a string (may be empty).
//   - defaultAgent is a non-empty string.
//
// Returns:
//   - The agent name, or defaultAgent if agent is empty.
//
// Side effects:
//   - None.
func resolveChatAgentName(agent, defaultAgent string) string {
	if agent == "" {
		return defaultAgent
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

// streamChatResponse streams a response from the streamer and returns the complete message.
//
// Expected:
//   - application is a non-nil App instance with a configured streamer.
//   - agentName is a non-empty string.
//   - message is a non-empty string.
//   - outputFormat is either "text" or "json".
//   - writer is a non-nil io.Writer for output.
//
// Returns:
//   - The complete response string and nil on success, or empty string and error on failure.
//
// Side effects:
//   - Streams response chunks from the streamer.
func streamChatResponse(application *app.App, agentName string, message string, outputFormat string, writer io.Writer) (string, error) {
	var consumer streaming.StreamConsumer
	if outputFormat == "json" {
		consumer = NewJSONConsumer(writer)
	} else {
		consumer = NewWriterConsumer(writer, true)
	}
	if err := streaming.Run(context.Background(), application.Streamer, consumer, agentName, message); err != nil {
		return "", fmt.Errorf("streaming response: %w", err)
	}
	if err := getConsumerError(consumer); err != nil {
		return "", fmt.Errorf("stream error: %w", err)
	}
	return getConsumerResponse(consumer), nil
}

// getConsumerError retrieves the error from a consumer using type assertion.
//
// Expected:
//   - consumer is a StreamConsumer implementation.
//
// Returns:
//   - The error from the consumer, or nil if the consumer doesn't support errors.
//
// Side effects:
//   - None.
func getConsumerError(consumer streaming.StreamConsumer) error {
	type errorGetter interface {
		Err() error
	}
	if eg, ok := consumer.(errorGetter); ok {
		return eg.Err()
	}
	return nil
}

// getConsumerResponse retrieves the response from a consumer using type assertion.
//
// Expected:
//   - consumer is a StreamConsumer implementation.
//
// Returns:
//   - The response string from the consumer, or empty string if not supported.
//
// Side effects:
//   - None.
func getConsumerResponse(consumer streaming.StreamConsumer) string {
	type responseGetter interface {
		Response() string
	}
	if rg, ok := consumer.(responseGetter); ok {
		return rg.Response()
	}
	return ""
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
			metadata := ctxstore.SessionMetadata{
				AgentID:      application.Engine.Manifest().ID,
				SystemPrompt: application.Engine.BuildSystemPrompt(),
				LoadedSkills: application.Engine.LoadedSkills(),
			}
			if saveErr := application.Sessions.Save(sessionID, store, metadata); saveErr != nil {
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
