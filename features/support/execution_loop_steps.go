//go:build e2e

package support

import (
	"context"
	"errors"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/execution"
	"github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/provider"
)

// executionLoopState holds state for execution loop BDD scenarios.
type executionLoopState struct {
	loop       *execution.Loop
	result     *harness.EvaluationResult
	chunks     []provider.StreamChunk
	evalErr    error
	maxRetries int
	validator  harness.Validator
}

// executionAlwaysPassValidator is a validator that always returns valid for testing.
type executionAlwaysPassValidator struct{}

// Validate always returns valid for testing.
//
// Returns: always returns ValidationResult with Valid=true and Score=1.0
// Expected: no external calls
// Side effects: none.
func (v *executionAlwaysPassValidator) Validate(_ string) (*harness.ValidationResult, error) {
	return &harness.ValidationResult{Valid: true, Score: 1.0}, nil
}

// executionAlwaysFailValidator is a validator that always returns invalid for testing.
type executionAlwaysFailValidator struct{}

// Validate always returns invalid for testing.
//
// Returns: always returns ValidationResult with Valid=false and Score=0.0
// Expected: no external calls
// Side effects: none.
func (v *executionAlwaysFailValidator) Validate(_ string) (*harness.ValidationResult, error) {
	return &harness.ValidationResult{Valid: false, Score: 0.0}, nil
}

// executionFakeStreamer is a test streamer that returns pre-configured responses.
type executionFakeStreamer struct {
	response string
}

// Stream returns a pre-configured response for testing.
//
// Returns: a channel containing the pre-configured response and a done chunk
// Expected: the channel is closed after sending the response
// Side effects: closes the returned channel.
func (f *executionFakeStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: f.response}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

// RegisterExecutionLoopSteps registers BDD step definitions for the execution loop feature.
//
// Expected: all step definitions are registered with the scenario context
// Side effects: modifies the scenario context by adding step definitions.
func RegisterExecutionLoopSteps(ctx *godog.ScenarioContext) {
	s := &executionLoopState{maxRetries: 3}

	ctx.Step(`^the execution loop is configured with max retries (\d+)$`, func(n int) {
		s.maxRetries = n
	})

	ctx.Step(`^no validator is configured$`, func() {
		s.validator = nil
	})

	ctx.Step(`^a validator that accepts all output$`, func() {
		s.validator = &executionAlwaysPassValidator{}
	})

	ctx.Step(`^a validator that rejects all output$`, func() {
		s.validator = &executionAlwaysFailValidator{}
	})

	ctx.Step(`^the execution loop evaluates agent "([^"]*)" with message "([^"]*)"$`, func(agentID, msg string) {
		opts := []execution.Option{execution.WithMaxRetries(s.maxRetries)}
		if s.validator != nil {
			opts = append(opts, execution.WithValidator(s.validator))
		}
		s.loop = execution.NewLoop(opts...)
		streamer := &executionFakeStreamer{response: "result text"}
		s.result, s.evalErr = s.loop.Evaluate(context.Background(), streamer, agentID, msg)
	})

	ctx.Step(`^the evaluation result has output "([^"]*)"$`, func(expected string) error {
		if s.result == nil {
			return errors.New("result is nil")
		}
		if s.result.Output != expected {
			return errors.New("output mismatch: got " + s.result.Output)
		}
		return nil
	})

	ctx.Step(`^the evaluation attempt count is (\d+)$`, func(n int) error {
		if s.result == nil {
			return errors.New("result is nil")
		}
		if s.result.AttemptCount != n {
			return errors.New("attempt count mismatch")
		}
		return nil
	})

	ctx.Step(`^the final score is 1\.0$`, func() error {
		if s.result == nil {
			return errors.New("result is nil")
		}
		if s.result.FinalScore != 1.0 {
			return errors.New("score is not 1.0")
		}
		return nil
	})

	ctx.Step(`^the evaluation succeeds$`, func() error {
		return s.evalErr
	})

	ctx.Step(`^the evaluation completes without error$`, func() error {
		return s.evalErr
	})

	ctx.Step(`^the execution loop stream-evaluates agent "([^"]*)" with message "([^"]*)"$`, func(agentID, msg string) {
		opts := []execution.Option{execution.WithMaxRetries(s.maxRetries)}
		if s.validator != nil {
			opts = append(opts, execution.WithValidator(s.validator))
		}
		s.loop = execution.NewLoop(opts...)
		streamer := &executionFakeStreamer{response: "stream result"}
		ch, err := s.loop.StreamEvaluate(context.Background(), streamer, agentID, msg)
		s.evalErr = err
		if ch != nil {
			for chunk := range ch {
				s.chunks = append(s.chunks, chunk)
			}
		}
	})

	ctx.Step(`^the stream contains a content chunk "([^"]*)"$`, func(expected string) error {
		for i := range s.chunks {
			if s.chunks[i].Content == expected {
				return nil
			}
		}
		return errors.New("chunk not found: " + expected)
	})

	ctx.Step(`^the stream ends with a done chunk$`, func() error {
		if len(s.chunks) == 0 {
			return errors.New("no chunks")
		}
		last := s.chunks[len(s.chunks)-1]
		if !last.Done {
			return errors.New("last chunk is not done")
		}
		return nil
	})

	ctx.Step(`^the execution loop evaluates with a cancelled context$`, func() {
		ctx2, cancel := context.WithCancel(context.Background())
		cancel()
		s.loop = execution.NewLoop(execution.WithMaxRetries(s.maxRetries))
		streamer := &executionFakeStreamer{response: "output"}
		s.result, s.evalErr = s.loop.Evaluate(ctx2, streamer, "agent", "msg")
	})
}
