package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/spf13/cobra"
)

type RunOptions struct {
	Prompt string
	Agent  string
	JSON   bool
}

type runResponse struct {
	Success  bool   `json:"success"`
	Response string `json:"response"`
	Agent    string `json:"agent"`
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrompt(cmd, getApp(), opts)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.Prompt, "prompt", "", "Prompt to execute")
	flags.StringVar(&opts.Agent, "agent", opts.Agent, "Agent to use for the prompt")
	flags.BoolVar(&opts.JSON, "json", false, "Output the result as JSON")

	return cmd
}

func runPrompt(cmd *cobra.Command, application *app.App, opts *RunOptions) error {
	if strings.TrimSpace(opts.Prompt) == "" {
		return errors.New("prompt is required")
	}

	if application.Engine == nil {
		return errors.New("engine not configured")
	}

	agentName := strings.TrimSpace(opts.Agent)
	if agentName == "" {
		agentName = "worker"
	}

	chunks, err := application.Engine.Stream(context.Background(), agentName, opts.Prompt)
	if err != nil {
		return fmt.Errorf("streaming response: %w", err)
	}

	response, err := collectRunResponse(chunks)
	if err != nil {
		return fmt.Errorf("stream error: %w", err)
	}

	if opts.JSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(runResponse{
			Success:  true,
			Response: response,
			Agent:    agentName,
		})
	}

	_, err = fmt.Fprintln(cmd.OutOrStdout(), response)
	return err
}

func collectRunResponse(chunks <-chan provider.StreamChunk) (string, error) {
	var response strings.Builder

	for chunk := range chunks {
		if chunk.Error != nil {
			return "", chunk.Error
		}
		response.WriteString(chunk.Content)
	}

	return response.String(), nil
}
