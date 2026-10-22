// Package question implements the blocking clarifying-question tool.
// Execute registers the question in a questionrequest.Registry and
// blocks until the operator answers via the API, the timeout fires,
// or the parent context is cancelled.
package question

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/questionrequest"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// DefaultTimeout is the default suspension window after which an
// unanswered question resolves with a no-answer result. Configurable
// via New's timeout parameter; zero falls back to this constant.
const DefaultTimeout = 5 * time.Minute

// Waiter is the subset of questionrequest.Registry the question tool
// depends on. Declared locally so tests can substitute a fake without
// importing the concrete registry type.
type Waiter interface {
	Register(req questionrequest.QuestionRequest) error
	Wait(ctx context.Context, requestID string) (questionrequest.QuestionAnswer, error)
}

// Tool implements a blocking clarifying question prompt. Each Execute
// call registers a pending QuestionRequest then blocks until the
// operator answers, the timeout elapses, or the parent context is
// cancelled. The operator's answer is returned as the tool result so
// the agent proceeds with information the user actually chose.
type Tool struct {
	registry Waiter
	bus      *eventbus.EventBus
	timeout  time.Duration
}

// New creates a new blocking question tool bound to the supplied
// question registry. A nil registry is a wiring bug and yields a tool
// whose Execute errors on every call — callers must wire the shared registry
// from app construction. timeout <= 0 falls back to DefaultTimeout.
//
// Expected:
//   - registry is the shared questionrequest.Registry.
//   - timeout is the per-question suspension window; 0 means default.
//
// Returns:
//   - A Tool configured to block on operator answers.
//
// Side effects:
//   - None until Execute is called.
func New(registry Waiter, timeout time.Duration) *Tool {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Tool{registry: registry, timeout: timeout}
}

// NewWithBus creates a blocking question tool that additionally
// publishes EventQuestionRequired / EventQuestionAnswered /
// EventQuestionTimeout onto the supplied event bus so the API server's
// turn-registry subscribers surface the request on the long-poll
// diff. A nil bus disables publishing — the blocking behaviour is
// unchanged.
//
// Expected:
//   - registry is the shared questionrequest.Registry.
//   - bus may be nil.
//   - timeout is the per-question suspension window; 0 means default.
//
// Returns:
//   - A Tool configured to block and publish lifecycle events.
//
// Side effects:
//   - None until Execute is called.
func NewWithBus(registry Waiter, bus *eventbus.EventBus, timeout time.Duration) *Tool {
	t := New(registry, timeout)
	t.bus = bus
	return t
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "question".
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Name() string { return "question" }

// Description returns a human-readable description of the question tool.
//
// Returns:
//   - A short summary of the tool's purpose.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Description() string {
	return "Ask the user a clarifying question and wait for their answer"
}

// Timeout implements tool.TimeoutOverrider so the engine grants the
// suspension window as the per-tool execution budget instead of the
// default shell-style cap.
//
// Returns:
//   - The configured suspension window.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Timeout() time.Duration { return t.timeout }

// Schema returns the input schema for the question tool.
//
// Returns:
//   - A schema describing question, options, and allow_multiple.
//
// Expected:
//   - None.
//
// Side effects:
//   - None.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"question": {Type: "string", Description: "The question to ask the user"},
			"options": {
				Type:        "array",
				Description: "Optional answer options",
				Items:       map[string]interface{}{"type": "string"},
			},
			"allow_multiple": {Type: "boolean", Description: "Whether multiple options may be selected"},
		},
		Required: []string{"question"},
	}
}

// Execute registers the question then blocks for the operator's
// answer.
//
// Expected:
//   - input contains a non-empty question argument.
//
// Returns:
//   - A tool.Result carrying the operator's answer, or a no-answer
//     result when the timeout fires.
//
// Side effects:
//   - Registers a pending QuestionRequest and publishes
//     EventQuestionRequired; on resolution publishes
//     EventQuestionAnswered or EventQuestionTimeout.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	if t.registry == nil {
		return tool.Result{}, errors.New("question registry is not configured")
	}
	args, err := parseArguments(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}

	requestID := uuid.NewString()
	req := questionrequest.QuestionRequest{
		RequestID:     requestID,
		ToolName:      t.Name(),
		Question:      args.question,
		Options:       args.options,
		AllowMultiple: args.allowMultiple,
		SessionID:     sessionIDFromContext(ctx),
		CreatedAt:     time.Now(),
	}
	if err := t.registry.Register(req); err != nil {
		return tool.Result{}, fmt.Errorf("failed to register question request: %w", err)
	}
	t.publishRequired(req)

	waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.timeout)
	defer cancel()
	answer, err := t.registry.Wait(waitCtx, requestID)
	if err != nil {
		t.publishTimeout(req)
		return noAnswerResult(req, t.timeout), nil
	}
	t.publishAnswered(req, answer)
	return answerResult(req, answer), nil
}

// parseArguments validates the question tool arguments and logs the
// parse outcome so malformed invocations are diagnosable from logs.
//
// Expected:
//   - input.Arguments carries a non-empty "question" string, an
//     optional "options" []any of strings, and an optional
//     "allow_multiple" bool.
//
// Returns:
//   - The parsed arguments, or an error describing the first failure.
//
// Side effects:
//   - Emits structured warn/debug log lines.
func parseArguments(ctx context.Context, input tool.Input) (questionArguments, error) {
	argKeys := make([]string, 0, len(input.Arguments))
	for k := range input.Arguments {
		argKeys = append(argKeys, k)
	}
	question, ok := input.Arguments["question"].(string)
	if !ok || strings.TrimSpace(question) == "" {
		slog.Warn("question-path: question tool argument missing or empty",
			"session_id", sessionIDFromContext(ctx),
			"arguments_nil", input.Arguments == nil,
			"argument_keys", argKeys,
			"parsed_question", truncateForLog(question))
		if input.Arguments == nil {
			return questionArguments{}, errors.New("question arguments failed to parse: nil map from provider argument decoding")
		}
		return questionArguments{}, errors.New("question argument is required")
	}
	slog.Debug("question-path: question tool parsed arguments",
		"session_id", sessionIDFromContext(ctx),
		"argument_keys", argKeys,
		"question", truncateForLog(question))
	options, err := parseOptions(input.Arguments["options"])
	if err != nil {
		return questionArguments{}, err
	}
	allowMultiple, _ := input.Arguments["allow_multiple"].(bool)
	return questionArguments{question: question, options: options, allowMultiple: allowMultiple}, nil
}

// questionArguments holds the validated fields of a question tool
// invocation.
type questionArguments struct {
	question      string
	options       []string
	allowMultiple bool
}

// parseOptions extracts and validates the optional options argument.
//
// Expected:
//   - raw is either nil or a []any of strings.
//
// Returns:
//   - The option strings, or an error when a non-string is present.
//
// Side effects:
//   - None.
func parseOptions(raw any) ([]string, error) {
	rawOptions, ok := raw.([]any)
	if !ok || len(rawOptions) == 0 {
		return nil, nil
	}
	options := make([]string, 0, len(rawOptions))
	for _, item := range rawOptions {
		option, ok := item.(string)
		if !ok {
			return nil, errors.New("options must contain strings")
		}
		options = append(options, option)
	}
	return options, nil
}

// truncateForLog shortens a question string for structured logging so
// long prompts don't flood log output.
//
// Expected:
//   - s may be any string, including empty.
//
// Returns:
//   - s unchanged when at most 80 bytes, else the first 80 bytes plus
//     an ellipsis.
//
// Side effects:
//   - None.
func truncateForLog(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "…"
}

// sessionIDFromContext reads the active session ID the engine stamps
// onto the tool-execution context. Empty when absent.
//
// Expected:
//   - ctx may carry a session.IDKey{} value.
//
// Returns:
//   - The session ID string, or "".
//
// Side effects:
//   - None.
func sessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(session.IDKey{}).(string)
	return id
}

// answerResult renders the operator's answer as a tool result.
//
// Expected:
//   - answer carries at least one answer string.
//
// Returns:
//   - A tool.Result whose Output joins the answers.
//
// Side effects:
//   - None.
func answerResult(req questionrequest.QuestionRequest, answer questionrequest.QuestionAnswer) tool.Result {
	joined := strings.Join(answer.Answers, ", ")
	return tool.Result{
		Title:  "Question",
		Output: fmt.Sprintf("User answered %q: %s", req.Question, joined),
		Metadata: map[string]interface{}{
			"request_id": req.RequestID,
			"question":   req.Question,
			"answers":    answer.Answers,
		},
	}
}

// noAnswerResult renders the timeout disposition as a clear tool
// result the agent can act on without erroring the turn.
//
// Expected:
//   - req is the request that timed out.
//   - timeout is the elapsed window.
//
// Returns:
//   - A tool.Result stating no answer arrived.
//
// Side effects:
//   - None.
func noAnswerResult(req questionrequest.QuestionRequest, timeout time.Duration) tool.Result {
	return tool.Result{
		Title:  "Question",
		Output: fmt.Sprintf("No answer received within %s for question %q — proceed with your best judgement or ask again", timeout, req.Question),
		Metadata: map[string]interface{}{
			"request_id": req.RequestID,
			"question":   req.Question,
			"timed_out":  true,
		},
	}
}

// publishRequired fires EventQuestionRequired for the registered
// request. No-op when the bus is unwired.
//
// Expected:
//   - req is the freshly registered request.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes onto the event bus.
func (t *Tool) publishRequired(req questionrequest.QuestionRequest) {
	if t.bus == nil {
		return
	}
	t.bus.Publish(events.EventQuestionRequired, events.NewQuestionRequiredEvent(events.QuestionRequiredEventData{
		RequestID:     req.RequestID,
		ToolName:      req.ToolName,
		Question:      req.Question,
		Options:       req.Options,
		AllowMultiple: req.AllowMultiple,
		SessionID:     req.SessionID,
		ChainID:       req.ChainID,
	}))
}

// publishAnswered fires EventQuestionAnswered with the operator's
// answer. No-op when the bus is unwired.
//
// Expected:
//   - answer is the resolved answer.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes onto the event bus.
func (t *Tool) publishAnswered(req questionrequest.QuestionRequest, answer questionrequest.QuestionAnswer) {
	if t.bus == nil {
		return
	}
	t.bus.Publish(events.EventQuestionAnswered, events.NewQuestionAnsweredEvent(events.QuestionAnsweredEventData{
		RequestID: req.RequestID,
		SessionID: req.SessionID,
		ToolName:  req.ToolName,
		Question:  req.Question,
		Answers:   answer.Answers,
	}))
}

// publishTimeout fires EventQuestionTimeout for the expired request.
// No-op when the bus is unwired.
//
// Expected:
//   - req is the request whose window elapsed.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes onto the event bus.
func (t *Tool) publishTimeout(req questionrequest.QuestionRequest) {
	if t.bus == nil {
		return
	}
	t.bus.Publish(events.EventQuestionTimeout, events.NewQuestionTimeoutEvent(events.QuestionAnsweredEventData{
		RequestID: req.RequestID,
		SessionID: req.SessionID,
		ToolName:  req.ToolName,
		Question:  req.Question,
	}))
}
