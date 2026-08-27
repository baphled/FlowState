//go:build e2e

package support

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/cucumber/godog"

	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
)

// openAICompatClassificationSteps holds the captured *provider.Error from the
// most recent classification step so the assertion steps can inspect it.
type openAICompatClassificationSteps struct {
	lastErr *provider.Error
}

// registerOpenAICompatClassificationSteps binds the steps for the
// OpenAI-compatible provider error classification feature. A dedicated
// httptest server replays the scenario's status code and JSON body so
// ParseProviderError classifies a genuine SDK error rather than a synthetic
// one.
func registerOpenAICompatClassificationSteps(ctx *godog.ScenarioContext) {
	s := &openAICompatClassificationSteps{}
	ctx.Step(`^an OpenAI-compatible provider named "([^"]*)"$`, s.providerNamed)
	ctx.Step(`^the provider returns HTTP (\d+) with code "([^"]*)"$`, s.returnsHTTPWithCode)
	ctx.Step(`^the provider returns HTTP (\d+) with message "([^"]*)"$`, s.returnsHTTPWithMessage)
	ctx.Step(`^the error type is "([^"]*)"$`, s.errorTypeIs)
	ctx.Step(`^the error is not retriable$`, s.errorIsNotRetriable)
}

// providerNamed accepts the provider name for the scenario.
func (s *openAICompatClassificationSteps) providerNamed(name string) error {
	return nil
}

// request failing with the given status and error body, capturing the
// classified provider error.
func (s *openAICompatClassificationSteps) request(status int, body string) error {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	ctx := context.Background()
	client := openaiAPI.NewClient(option.WithBaseURL(srv.URL + "/v1"))
	_, err := client.Chat.Completions.New(ctx, openaiAPI.ChatCompletionNewParams{
		Messages: []openaiAPI.ChatCompletionMessageParamUnion{
			openaiAPI.UserMessage("hello"),
		},
		Model: "gpt-4o",
	})
	if err == nil {
		return fmt.Errorf("expected the request to fail, but it succeeded")
	}
	s.lastErr = openaicompat.ParseProviderError("openai", err)
	if s.lastErr == nil {
		return fmt.Errorf("ParseProviderError returned nil for error: %v", err)
	}
	return nil
}

// returnsHTTPWithCode issues a request whose error body carries the given code.
func (s *openAICompatClassificationSteps) returnsHTTPWithCode(status int, code string) error {
	body := fmt.Sprintf(`{"error":{"code":%q,"message":"request failed"}}`, code)
	return s.request(status, body)
}

// returnsHTTPWithMessage issues a request whose error body carries the given message.
func (s *openAICompatClassificationSteps) returnsHTTPWithMessage(status int, message string) error {
	body := fmt.Sprintf(`{"error":{"code":"invalid_request_error","message":%q}}`, message)
	return s.request(status, body)
}

// errorTypeIs asserts the classified error's type.
func (s *openAICompatClassificationSteps) errorTypeIs(want string) error {
	if got := string(s.lastErr.ErrorType); got != want {
		return fmt.Errorf("expected error type %q, got %q", want, got)
	}
	return nil
}

// errorIsNotRetriable asserts the classified error is not retriable.
func (s *openAICompatClassificationSteps) errorIsNotRetriable() error {
	if s.lastErr.IsRetriable {
		return fmt.Errorf("expected error to not be retriable")
	}
	return nil
}


