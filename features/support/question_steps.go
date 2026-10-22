//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/questionrequest"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	questiontool "github.com/baphled/flowstate/internal/tool/question"
)

// questionSteps holds per-scenario state for the blocking question
// tool feature. State is rebuilt in the Background step so every
// scenario starts from a fresh registry + tool.
type questionSteps struct {
	registry  *questionrequest.Registry
	tool      tool.Tool
	sessionID string
	result    tool.Result
	execErr   error
	done      chan struct{}
}

// RegisterQuestionSteps wires the question_tool feature steps onto
// the godog scenario context.
func RegisterQuestionSteps(ctx *godog.ScenarioContext) {
	s := &questionSteps{}
	ctx.Step(`^a question registry is available$`, s.questionRegistryAvailable)
	ctx.Step(`^a question registry is available with a short timeout$`, s.questionRegistryAvailableShortTimeout)
	ctx.Step(`^a question request registry is wired to a question tool$`, s.questionToolWired)
	ctx.Step(`^the agent asks "([^"]*)" with options "([^"]*)" and "([^"]*)"$`, s.agentAsksWithOptions)
	ctx.Step(`^the agent asks a question without a question argument$`, s.agentAsksWithoutQuestion)
	ctx.Step(`^a pending question request should be registered for the session$`, s.pendingQuestionRegistered)
	ctx.Step(`^the question tool should still be waiting$`, s.questionToolStillWaiting)
	ctx.Step(`^the user answers the pending question with "([^"]*)"$`, s.userAnswersPendingQuestion)
	ctx.Step(`^the question tool should return the answer "([^"]*)"$`, s.questionToolReturnsAnswer)
	ctx.Step(`^the question timeout elapses without an answer$`, s.questionTimeoutElapses)
	ctx.Step(`^the question tool should return a timeout error$`, s.questionToolReturnsTimeout)
	ctx.Step(`^the session should list one pending question$`, s.sessionListsOnePendingQuestion)
	ctx.Step(`^the session should list no pending questions$`, s.sessionListsNoPendingQuestions)
	ctx.Step(`^the question tool should return an argument error without registering a request$`, s.questionToolArgumentError)
}

func (s *questionSteps) questionRegistryAvailable() error {
	s.registry = questionrequest.NewRegistry()
	s.tool = questiontool.New(s.registry, questiontool.DefaultTimeout)
	s.sessionID = "sess-question"
	s.result, s.execErr, s.done = tool.Result{}, nil, nil
	return nil
}

func (s *questionSteps) questionRegistryAvailableShortTimeout() error {
	if err := s.questionRegistryAvailable(); err != nil {
		return err
	}
	s.tool = questiontool.New(s.registry, 50*time.Millisecond)
	return nil
}

func (s *questionSteps) questionToolWired() error {
	if s.registry == nil {
		return errors.New("question registry not initialised — Background step missing")
	}
	return nil
}

func (s *questionSteps) agentAsksWithOptions(question, optionA, optionB string) error {
	s.done = make(chan struct{})
	// Stamp the session ID the same way the engine dispatcher does so
	// the registry attributes the pending question to this session.
	ctx := context.WithValue(context.WithoutCancel(context.Background()), session.IDKey{}, s.sessionID)
	go func() {
		defer close(s.done)
		s.result, s.execErr = s.tool.Execute(ctx, tool.Input{
			Name: "question",
			Arguments: map[string]interface{}{
				"question":       question,
				"options":        []interface{}{optionA, optionB},
				"allow_multiple": false,
			},
		})
	}()
	// Give the Execute goroutine a moment to register + block.
	time.Sleep(50 * time.Millisecond)
	return nil
}

func (s *questionSteps) agentAsksWithoutQuestion() error {
	ctx := context.WithValue(context.WithoutCancel(context.Background()), session.IDKey{}, s.sessionID)
	s.result, s.execErr = s.tool.Execute(ctx, tool.Input{
		Name:      "question",
		Arguments: map[string]interface{}{"options": []interface{}{"a"}},
	})
	return nil
}

func (s *questionSteps) pendingQuestionRegistered() error {
	if got := len(s.registry.PendingForSession(s.sessionID)); got != 1 {
		return errors.New("expected exactly one pending question for the session")
	}
	return nil
}

func (s *questionSteps) questionToolStillWaiting() error {
	select {
	case <-s.done:
		return errors.New("question tool returned before the answer arrived")
	default:
		return nil
	}
}

func (s *questionSteps) userAnswersPendingQuestion(answer string) error {
	ids := s.registry.PendingForSession(s.sessionID)
	if len(ids) == 0 {
		return errors.New("no pending question to answer")
	}
	return s.registry.Resolve(ids[0], questionrequest.QuestionAnswer{
		Answers: []string{answer},
	})
}

func (s *questionSteps) questionToolReturnsAnswer(answer string) error {
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		return errors.New("question tool did not return after the answer")
	}
	if s.execErr != nil {
		return s.execErr
	}
	if !strings.Contains(s.result.Output, answer) {
		return errors.New("tool output does not carry the answer: " + s.result.Output)
	}
	return nil
}

func (s *questionSteps) questionTimeoutElapses() error {
	select {
	case <-s.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("question tool did not return on timeout")
	}
}

func (s *questionSteps) questionToolReturnsTimeout() error {
	if s.execErr != nil {
		return s.execErr
	}
	meta, _ := s.result.Metadata["timed_out"].(bool)
	if !meta {
		return errors.New("expected a timeout (no-answer) result")
	}
	return nil
}

func (s *questionSteps) sessionListsOnePendingQuestion() error {
	if got := len(s.registry.PendingForSession(s.sessionID)); got != 1 {
		return errors.New("expected one pending question on the session list")
	}
	return nil
}

func (s *questionSteps) sessionListsNoPendingQuestions() error {
	if got := len(s.registry.PendingForSession(s.sessionID)); got != 0 {
		return errors.New("expected no pending questions on the session list")
	}
	return nil
}

func (s *questionSteps) questionToolArgumentError() error {
	if s.execErr == nil {
		return errors.New("expected an argument error")
	}
	if s.registry.PendingCount() != 0 {
		return errors.New("expected no request registered on argument error")
	}
	return nil
}
