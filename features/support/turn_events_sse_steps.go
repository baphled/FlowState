//go:build e2e

// Package support — step glue for the per-turn SSE endpoint scenarios
// in features/http/turn_events_sse.feature. The steps drive a real
// turn.Registry through an httptest-backed API server, mirroring the
// handler-level tests in internal/api/turn_events_test.go but through
// the BDD surface and the public route table.
package support

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/turn"
)

var (
	errNoTurnContentChunks = errors.New("expected the turn SSE stream to emit content chunk events")
	errNoTurnDoneSentinel  = errors.New("expected the turn SSE stream to terminate with the [DONE] sentinel")
	errTurnStreamBodyRead  = errors.New("failed to read the turn SSE stream body")
)

// turnEventsDispatcher adapts a real turn.Registry into the api
// package's DispatcherService so the events route resolves the same
// registry the steps mutate.
type turnEventsDispatcher struct {
	reg     *turn.Registry
	entries sync.Map
}

// ID returns the dispatcher identifier.
func (d *turnEventsDispatcher) ID() string { return "e2e-turn-events" }

// Start records a dispatched turn's start timestamp.
func (d *turnEventsDispatcher) Start(id string) { d.entries.Store(id, time.Now()) }

// CancelQueuedPrompt satisfies DispatcherService; unused by the
// events route.
func (d *turnEventsDispatcher) CancelQueuedPrompt(_, _ string) bool { return false }

// CloseSessionQueue satisfies DispatcherService; unused by the
// events route.
func (d *turnEventsDispatcher) CloseSessionQueue(string) {}

// DispatchEphemeral satisfies DispatcherService; unused by the
// events route.
func (d *turnEventsDispatcher) DispatchEphemeral(context.Context, dispatch.DispatchRequest, streaming.StreamConsumer) (dispatch.EphemeralHandle, error) {
	return dispatch.EphemeralHandle{}, errors.New("not implemented")
}

// DispatchSessioned satisfies DispatcherService; unused by the
// events route.
func (d *turnEventsDispatcher) DispatchSessioned(context.Context, dispatch.DispatchRequest, streaming.StreamConsumer) (dispatch.SessionedHandle, error) {
	return dispatch.SessionedHandle{}, errors.New("not implemented")
}

// TurnRegistry exposes the underlying turn registry.
func (d *turnEventsDispatcher) TurnRegistry() *turn.Registry { return d.reg }

// turnEventsSSESteps holds per-scenario state for the per-turn SSE
// scenarios.
type turnEventsSSESteps struct {
	server   *httptest.Server
	registry *turn.Registry
	turnID   string
	body     string
	status   int
	header   http.Header
}

// RegisterTurnEventsSSESteps wires the per-turn SSE steps.
func RegisterTurnEventsSSESteps(ctx *godog.ScenarioContext) {
	steps := &turnEventsSSESteps{}
	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		steps.reset()
		return bctx, nil
	})
	ctx.After(func(bctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		steps.teardown()
		return bctx, nil
	})

	ctx.Step(`^I start a turn and connect to its events SSE endpoint$`, steps.startTurnAndConnect)
	ctx.Step(`^I connect to the events SSE endpoint for unknown turn "([^"]*)"$`, steps.connectUnknownTurn)
	ctx.Step(`^the SSE response has content type "([^"]*)"$`, steps.contentTypeIs)
	ctx.Step(`^the stream emits content chunk events$`, steps.emitsContentChunks)
	ctx.Step(`^the stream terminates with the \[DONE\] sentinel$`, steps.terminatesWithDone)
	ctx.Step(`^the response status code is (\d+)$`, steps.statusCodeIs)
}

func (s *turnEventsSSESteps) reset() {
	s.server = nil
	s.registry = nil
	s.turnID = ""
	s.body = ""
	s.status = 0
	s.header = nil
}

func (s *turnEventsSSESteps) teardown() {
	if s.server != nil {
		s.server.CloseClientConnections()
		s.server.Close()
		s.server = nil
	}
}

func (s *turnEventsSSESteps) boot() error {
	s.registry = turn.NewRegistry()
	turnID, err := s.registry.Start("sess-e2e")
	if err != nil {
		return err
	}
	s.turnID = turnID
	dispatcher := &turnEventsDispatcher{reg: s.registry}
	srv := api.NewServer(nil, nil, nil, nil, api.WithDispatcher(dispatcher))
	s.server = httptest.NewServer(srv.Handler())
	return nil
}

func (s *turnEventsSSESteps) startTurnAndConnect() error {
	if err := s.boot(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.turnEventsURL(s.turnID), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errTurnStreamBodyRead
	}
	defer resp.Body.Close()
	s.header = resp.Header.Clone()
	s.status = resp.StatusCode

	go func() {
		if aerr := s.registry.Append(s.turnID, session.Message{ID: "e2e-1", Role: "assistant", Content: "hello "}); aerr != nil {
			return
		}
		if aerr := s.registry.Append(s.turnID, session.Message{ID: "e2e-2", Role: "assistant", Content: "world"}); aerr != nil {
			return
		}
		s.registry.SetProviderModel(s.turnID, "anthropic", "claude-opus-4.5")
		_ = s.registry.Complete(s.turnID, turn.ModelInfo{})
	}()

	scanner := bufio.NewScanner(resp.Body)
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		b.WriteString(line)
		b.WriteByte('\n')
		if line == "data: [DONE]" {
			break
		}
	}
	s.body = b.String()
	return nil
}

func (s *turnEventsSSESteps) connectUnknownTurn(turnID string) error {
	if err := s.boot(); err != nil {
		return err
	}
	resp, err := http.Get(s.turnEventsURL(turnID))
	if err != nil {
		return errTurnStreamBodyRead
	}
	defer resp.Body.Close()
	s.status = resp.StatusCode
	s.header = resp.Header.Clone()
	return nil
}

func (s *turnEventsSSESteps) turnEventsURL(turnID string) string {
	return s.server.URL + "/api/v1/sessions/sess-e2e/turns/" + turnID + "/events"
}

func (s *turnEventsSSESteps) contentTypeIs(want string) error {
	if got := s.header.Get("Content-Type"); got != want {
		return errors.New("content type mismatch: got " + got)
	}
	return nil
}

func (s *turnEventsSSESteps) emitsContentChunks() error {
	chunks := 0
	for _, frame := range s.dataFrames() {
		var payload map[string]any
		if json.Unmarshal([]byte(frame), &payload) != nil {
			continue
		}
		if _, ok := payload["content"]; ok {
			chunks++
		}
	}
	if chunks < 1 {
		return errNoTurnContentChunks
	}
	return nil
}

func (s *turnEventsSSESteps) terminatesWithDone() error {
	for _, frame := range s.dataFrames() {
		if frame == "[DONE]" {
			return nil
		}
	}
	return errNoTurnDoneSentinel
}

func (s *turnEventsSSESteps) statusCodeIs(code int) error {
	if s.status != code {
		return errors.New("status mismatch")
	}
	return nil
}

func (s *turnEventsSSESteps) dataFrames() []string {
	var frames []string
	for _, line := range strings.Split(s.body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	return frames
}
