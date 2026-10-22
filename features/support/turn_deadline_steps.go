//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/cucumber/godog"

	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/provider/shared"
)

// turnDeadlineSteps holds the streaming server, its stream cancel func and
// the captured terminal chunk for the per-turn stream deadline feature.
type turnDeadlineSteps struct {
	srv        *httptest.Server
	cancel     context.CancelFunc
	lastError  error
	lastDone   bool
	sawErrorCh bool
}

// registerTurnDeadlineSteps binds the steps for the per-turn stream deadline
// feature. The scenarios drive a real SSE httptest server through
// openaicompat.RunStream so the deadline cap is exercised on the genuine SDK
// streaming path, not a synthetic channel.
func registerTurnDeadlineSteps(ctx *godog.ScenarioContext) {
	s := &turnDeadlineSteps{}
	ctx.BeforeScenario(func(*godog.Scenario) {
		s.srv = nil
		s.cancel = nil
		s.lastError = nil
		s.lastDone = false
		s.sawErrorCh = false
		openaicompat.SetTurnStreamTimeout(shared.DefaultTurnStreamTimeout)
	})
	ctx.AfterScenario(func(*godog.Scenario, error) {
		if s.cancel != nil {
			s.cancel()
		}
		if s.srv != nil {
			s.srv.Close()
		}
		openaicompat.SetTurnStreamTimeout(shared.DefaultTurnStreamTimeout)
	})
	ctx.Step(`^the provider trickles chunks forever$`, s.trickleForever)
	ctx.Step(`^the provider streams a complete response with finish reason "([^"]*)"$`, s.completeResponse)
	ctx.Step(`^the per-turn stream deadline is set to (\d+) (milliseconds|seconds)$`, s.deadlineSet)
	ctx.Step(`^the per-turn stream deadline is disabled$`, s.deadlineDisabled)
	ctx.Step(`^the stream is read for (\d+) milliseconds$`, s.readForMs)
	ctx.Step(`^the stream terminates with a terminal error chunk$`, s.terminalErrorChunk)
	ctx.Step(`^the stream terminates with a done chunk and no error$`, s.doneNoError)
	ctx.Step(`^the terminal error carries the turn deadline sentinel$`, s.deadlineSentinel)
	ctx.Step(`^the terminal error is a retriable network error$`, s.retriableNetwork)
	ctx.Step(`^no terminal error chunk has been emitted$`, s.noTerminalError)
}

// startStream spins up the current httptest server and consumes the stream
// to completion (or until the consumer window ends), recording the terminal
// chunk. The stream runs on a cancellable context that is cancelled on
// return — with the deadline disabled nothing else tears the pump down, and
// an uncancelled pump would block forever in shared.SendChunk once the
// channel buffer fills, wedging the AfterScenario server close.
func (s *turnDeadlineSteps) startStream(readWindow time.Duration) {
	var params openaiAPI.ChatCompletionNewParams
	params.Messages = []openaiAPI.ChatCompletionMessageParamUnion{
		openaiAPI.UserMessage("hello"),
	}
	params.Model = "gpt-4o"
	client := openaiAPI.NewClient(option.WithBaseURL(s.srv.URL + "/v1"))
	streamCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	defer cancel()
	ch := openaicompat.RunStream(streamCtx, client, params, "openai")

	deadline := time.After(readWindow)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if chunk.Error != nil {
				s.sawErrorCh = true
				s.lastError = chunk.Error
			}
			if chunk.Done {
				s.lastDone = true
				return
			}
		case <-deadline:
			return
		}
	}
}

// trickleForever serves an SSE stream that emits a content delta every
// 20ms and never sends a finish reason or [DONE]. It exits when the client
// disconnects (r.Context().Done()) so the AfterScenario server close cannot
// wedge on a handler that outlives the stream.
func (s *turnDeadlineSteps) trickleForever() error {
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"tick %d\"}}]}\n\n", i)
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	return nil
}

// completeResponse serves an SSE stream with one content chunk, a finish
// reason, usage chunk, and [DONE].
func (s *turnDeadlineSteps) completeResponse(reason string) error {
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":%q}]}\n\n", reason)
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	s.startStream(5 * time.Second)
	return nil
}

// deadlineSet sets the per-turn cap in the given unit and drains the
// stream (long window so the deadline, not the read window, ends the
// stream).
func (s *turnDeadlineSteps) deadlineSet(amount int, unit string) error {
	d := time.Duration(amount) * time.Millisecond
	if unit == "seconds" {
		d = time.Duration(amount) * time.Second
	}
	openaicompat.SetTurnStreamTimeout(d)
	s.startStream(10 * time.Second)
	return nil
}

// deadlineDisabled zeroes the cap and drains under a bounded read window.
func (s *turnDeadlineSteps) deadlineDisabled() error {
	openaicompat.SetTurnStreamTimeout(0)
	return nil
}

// readForMs drains the stream for the given window without any deadline
// expected to fire.
func (s *turnDeadlineSteps) readForMs(ms int) error {
	s.startStream(time.Duration(ms) * time.Millisecond)
	return nil
}

// terminalErrorChunk asserts a terminal Error+Done chunk arrived.
func (s *turnDeadlineSteps) terminalErrorChunk() error {
	if !s.sawErrorCh || !s.lastDone {
		return fmt.Errorf("expected a terminal error chunk, got error=%v done=%v", s.lastError, s.lastDone)
	}
	return nil
}

// doneNoError asserts a clean Done with no error.
func (s *turnDeadlineSteps) doneNoError() error {
	if !s.lastDone {
		return fmt.Errorf("expected a done chunk, got none")
	}
	if s.sawErrorCh {
		return fmt.Errorf("expected no error chunk, got: %v", s.lastError)
	}
	return nil
}

// deadlineSentinel asserts the terminal error wraps the shared sentinel.
func (s *turnDeadlineSteps) deadlineSentinel() error {
	if !errors.Is(s.lastError, shared.ErrTurnDeadlineExceeded) {
		return fmt.Errorf("terminal error does not wrap the turn deadline sentinel: %v", s.lastError)
	}
	return nil
}

// retriableNetwork asserts the terminal error classifies as a retriable
// network error so the failover layer can advance.
func (s *turnDeadlineSteps) retriableNetwork() error {
	provErr, ok := s.lastError.(*provider.Error)
	if !ok {
		return fmt.Errorf("terminal error is not a *provider.Error: %T", s.lastError)
	}
	if provErr.ErrorType != provider.ErrorTypeNetworkError {
		return fmt.Errorf("expected network_error classification, got %q", provErr.ErrorType)
	}
	if !provErr.IsRetriable {
		return fmt.Errorf("expected the deadline error to be retriable")
	}
	return nil
}

// noTerminalError asserts the stream was still open when the read window
// ended — the disabled cap never tripped.
func (s *turnDeadlineSteps) noTerminalError() error {
	if s.sawErrorCh {
		return fmt.Errorf("unexpected terminal error chunk: %v", s.lastError)
	}
	return nil
}
