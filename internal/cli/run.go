package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/spf13/cobra"
)

// RunOptions configures non-interactive prompt execution.
type RunOptions struct {
	Prompt  string
	Agent   string
	JSON    bool
	Session string
}

// runResponse represents the JSON response from a non-interactive prompt execution.
//
// Expected:
//   - None.
//
// Returns:
//   - N/A (type definition).
//
// Side effects:
//   - None.
type runResponse struct {
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Session  string `json:"session,omitempty"`
}

// newRunCmd creates the run command for non-interactive prompt execution.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with run options.
//
// Side effects:
//   - Registers run command flags.
func newRunCmd(getApp func() *app.App) *cobra.Command {
	opts := &RunOptions{
		Agent: "worker",
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a prompt non-interactively",
		Long:  "Run a prompt to completion for scripting and pipeline use.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPrompt(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&opts.Prompt, "prompt", "p", "", "The prompt to send to the agent (required)")
	flags.StringVar(&opts.Agent, "agent", opts.Agent, "Agent to use (default: worker)")
	flags.BoolVar(&opts.JSON, "json", false, "Output result as JSON")
	flags.StringVar(&opts.Session, "session", "", "Session ID to use/resume")

	return cmd
}

// runPrompt executes a prompt non-interactively and outputs the response.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - application is a non-nil App instance with a configured engine.
//   - opts is a non-nil RunOptions with a non-empty prompt.
//
// Returns:
//   - nil on success, or an error if validation or execution fails.
//
// Side effects:
//   - Streams response to stdout, saves session if available.
func runPrompt(cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	if err := validateRunOptions(opts); err != nil {
		return err
	}

	if application.Streamer == nil {
		return errors.New("engine not configured")
	}

	agentName := resolveAgentName(opts.Agent)
	sessionID := resolveSessionID(opts.Session)
	loadExistingSession(application, opts.Session)

	wrappedStreamer := streaming.NewSessionContextStreamer(
		application.Streamer,
		func() string { return sessionID },
		session.IDKey{},
	)

	response, err := streamResponse(cmd, wrappedStreamer, agentName, opts)
	if err != nil {
		return err
	}

	saveSession(cmd, application, sessionID)
	return writeRunOutput(cmd, opts, agentName, sessionID, response)
}

// validateRunOptions checks that required options are set.
//
// Expected:
//   - opts is a non-nil RunOptions.
//
// Returns:
//   - nil if valid, or an error if the prompt is empty.
//
// Side effects:
//   - None.
func validateRunOptions(opts *RunOptions) error {
	if strings.TrimSpace(opts.Prompt) == "" {
		return errors.New("prompt is required")
	}
	return nil
}

// resolveAgentName returns the agent name, defaulting to "worker" if empty.
//
// Expected:
//   - agent is a string (may be empty or whitespace).
//
// Returns:
//   - The agent name, or "worker" if agent is empty or whitespace.
//
// Side effects:
//   - None.
func resolveAgentName(agent string) string {
	name := strings.TrimSpace(agent)
	if name == "" {
		return "worker"
	}
	return name
}

// resolveSessionID returns the session ID, generating a new one if empty.
//
// Expected:
//   - session is a string (may be empty).
//
// Returns:
//   - The session ID, or a newly generated one if session is empty.
//
// Side effects:
//   - None.
func resolveSessionID(sessionParam string) string {
	if sessionParam == "" {
		return generateSessionID()
	}
	return sessionParam
}

// loadExistingSession loads a session into the engine if a session ID is provided.
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
func loadExistingSession(application *app.App, sessionParam string) {
	if sessionParam == "" || application.Sessions == nil {
		return
	}
	store, err := application.Sessions.Load(sessionParam)
	if err == nil {
		application.Engine.SetContextStore(store, sessionParam)
	}
}

// streamResponse streams a response from the streamer and returns the complete message.
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - streamer is a non-nil streaming.Streamer for response generation.
//   - agentName is a non-empty string.
//   - opts is a non-nil RunOptions with a non-empty prompt.
//
// Returns:
//   - The complete response string and nil on success, or empty string and error on failure.
//
// Side effects:
//   - Streams response chunks to stdout if JSON output is not requested.
func streamResponse(cmd *cobra.Command, streamer streaming.Streamer, agentName string, opts *RunOptions) (string, error) {
	consumer := NewWriterConsumer(cmd.OutOrStdout(), opts.JSON)
	if err := streaming.Run(context.Background(), streamer, consumer, agentName, opts.Prompt); err != nil {
		return "", fmt.Errorf("streaming response: %w", err)
	}
	if consumer.Err() != nil {
		return "", fmt.Errorf("stream error: %w", consumer.Err())
	}
	return consumer.Response(), nil
}

// saveSession saves the current session if the session store is available.
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
func saveSession(cmd *cobra.Command, application *app.App, sessionID string) {
	if application.Sessions == nil {
		return
	}
	store := application.Engine.ContextStore()
	if store == nil {
		return
	}
	loadedSkills := application.Engine.LoadedSkills()
	skillNames := make([]string, 0, len(loadedSkills))
	for i := range loadedSkills {
		skillNames = append(skillNames, loadedSkills[i].Name)
	}
	metadata := ctxstore.SessionMetadata{
		AgentID:      application.Engine.Manifest().ID,
		SystemPrompt: application.Engine.BuildSystemPrompt(),
		LoadedSkills: skillNames,
	}
	if err := application.Sessions.Save(sessionID, store, metadata); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to save session: %v\n", err)
	}
}

// writeRunOutput writes the response in the requested format (JSON or plain text).
//
// Expected:
//   - cmd is a non-nil cobra.Command.
//   - opts is a non-nil RunOptions.
//   - agentName is a non-empty string.
//   - sessionID is a non-empty string.
//   - response is a string (may be empty).
//
// Returns:
//   - nil on success, or an error if output fails.
//
// Side effects:
//   - Writes response to stdout in JSON or plain text format.
func writeRunOutput(cmd *cobra.Command, opts *RunOptions, agentName, sessionID, response string) error {
	if opts.JSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(runResponse{
			Agent:    agentName,
			Prompt:   opts.Prompt,
			Response: response,
			Session:  sessionID,
		})
	}

	if !strings.HasSuffix(response, "\n") {
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
	}
	return nil
}
