package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/spf13/cobra"
)

// RunOptions configures non-interactive prompt execution.
type RunOptions struct {
	Prompt  string
	Agent   string
	JSON    bool
	Session string
}

type runResponse struct {
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt"`
	Response string `json:"response"`
	Session  string `json:"session,omitempty"`
}

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

func runPrompt(cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	if err := validateRunOptions(opts); err != nil {
		return err
	}

	if application.Engine == nil {
		return errors.New("engine not configured")
	}

	agentName := resolveAgentName(opts.Agent)
	sessionID := resolveSessionID(opts.Session)
	loadExistingSession(application, opts.Session)

	response, err := streamResponse(cmd, application, agentName, opts)
	if err != nil {
		return err
	}

	saveSession(cmd, application, sessionID)
	return writeRunOutput(cmd, opts, agentName, sessionID, response)
}

func validateRunOptions(opts *RunOptions) error {
	if strings.TrimSpace(opts.Prompt) == "" {
		return errors.New("prompt is required")
	}
	return nil
}

func resolveAgentName(agent string) string {
	name := strings.TrimSpace(agent)
	if name == "" {
		return "worker"
	}
	return name
}

func resolveSessionID(session string) string {
	if session == "" {
		return generateSessionID()
	}
	return session
}

func loadExistingSession(application *app.App, session string) {
	if session == "" || application.Sessions == nil {
		return
	}
	store, err := application.Sessions.Load(session)
	if err == nil {
		application.Engine.SetContextStore(store)
	}
}

func streamResponse(cmd *cobra.Command, application *app.App, agentName string, opts *RunOptions) (string, error) {
	chunks, err := application.Engine.Stream(context.Background(), agentName, opts.Prompt)
	if err != nil {
		return "", fmt.Errorf("streaming response: %w", err)
	}

	var response strings.Builder
	for chunk := range chunks {
		if chunk.Error != nil {
			return "", fmt.Errorf("stream error: %w", chunk.Error)
		}
		if !opts.JSON {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), chunk.Content)
		}
		response.WriteString(chunk.Content)
	}
	return response.String(), nil
}

func saveSession(cmd *cobra.Command, application *app.App, sessionID string) {
	if application.Sessions == nil {
		return
	}
	store := application.Engine.ContextStore()
	if store == nil {
		return
	}
	if err := application.Sessions.Save(sessionID, store); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to save session: %v\n", err)
	}
}

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
