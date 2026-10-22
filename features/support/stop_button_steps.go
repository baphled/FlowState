//go:build e2e

// Package support provides BDD step definitions and helpers.
//
// This file implements the flagship stop-button reliability scenario
// (features/dispatch/stop_button.feature). It wires the full server-side
// stack in process — a real engine driving the real bash tool through a
// scripted failover provider, a persistent session manager, and the API
// server's auto-constructed dispatcher — then presses stop through the
// real DELETE /turns/{turn_id} route and asserts every link of the
// cancel chain settles: turn status, session failure_reason, process
// tree teardown, and queued-prompt release.
package support

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	bashtool "github.com/baphled/flowstate/internal/tool/bash"
)

// stopButtonWedgedCommand mirrors the Wave 1 bash-process-kill shape: an
// outer bash blocked on wait while a backgrounded sleep renamed via
// exec -a holds the stdio pipes and is findable by a pgrep marker.
const stopButtonWedgedCommand = `bash -c 'exec -a %s sleep 300' & wait`

const (
	stopButtonFirstPrompt  = "run the wedged command"
	stopButtonSecondPrompt = "queued follow-up prompt"
)

// stopButtonBudgets bound each polling assertion. The turn-settle bound
// must stay comfortably above the bash tool's 5s WaitDelay plus drain.
const (
	stopButtonBlockBudget    = 10 * time.Second
	stopButtonSettleOverhead = 15 * time.Second
	stopButtonMetaBudget     = 10 * time.Second
	stopButtonTeardownBudget = 3 * time.Second
	stopButtonReleaseBudget  = 5 * time.Second
	stopButtonPollInterval   = 100 * time.Millisecond
)

// stopButtonSteps holds the per-scenario state for the stop-button
// reliability scenario: the in-process API server, the persistent
// session manager and its sessions dir, the scripted provider, the
// running turn's identifier, and the cancel timestamp anchoring the
// 15-second settle bound.
type stopButtonSteps struct {
	server      *api.Server
	mgr         *session.Manager
	sessionsDir string
	sessionID   string
	turnID      string
	provider    *stopButtonScriptedProvider
	marker      string
	cancelledAt time.Time
}

// stopButtonScriptedProvider drives the turn lifecycle for the scenario:
// the first prompt's turn emits a bash tool call for the wedged command,
// a post-cancel retry surfaces the cancelled context, and the queued
// second prompt's stream is held open until the scenario releases it so
// the healthy follow-up cannot demote the session before the meta.json
// assertion has run.
type stopButtonScriptedProvider struct {
	name             string
	marker           string
	mu               sync.Mutex
	secondSeenOnce   bool
	secondPromptSeen chan struct{}
	release          chan struct{}
}

// Name identifies the provider to the failover manager.
func (p *stopButtonScriptedProvider) Name() string { return p.name }

// Stream emits the scripted chunk sequence for the request's last user
// message: a wedged bash tool call for the first prompt, the cancelled
// context error for post-cancel retries, and a held stream for the
// queued second prompt.
func (p *stopButtonScriptedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 4)
	if err := ctx.Err(); err != nil {
		go func() {
			defer close(ch)
			ch <- provider.StreamChunk{Error: err, Done: true}
		}()
		return ch, nil
	}
	if stopButtonLastUser(req.Messages) == stopButtonSecondPrompt {
		p.markSecondSeen()
		go func() {
			defer close(ch)
			select {
			case <-ctx.Done():
				ch <- provider.StreamChunk{Error: ctx.Err(), Done: true}
			case <-p.release:
				ch <- provider.StreamChunk{Content: "queued prompt completed", Done: true}
			}
		}()
		return ch, nil
	}
	if stopButtonHasToolResult(req.Messages) {
		go func() {
			defer close(ch)
			ch <- provider.StreamChunk{Content: "turn recovered", Done: true}
		}()
		return ch, nil
	}
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{
			EventType: "tool_call",
			ToolCall: &provider.ToolCall{
				ID:   "call_wedged_bash",
				Name: "bash",
				Arguments: map[string]interface{}{
					"command": fmt.Sprintf(stopButtonWedgedCommand, p.marker),
				},
			},
		}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *stopButtonScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *stopButtonScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

// Models satisfies the provider interface with no models.
func (p *stopButtonScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// markSecondSeen records once that the queued prompt's turn reached the
// provider, proving the dispatcher released it for dispatch.
func (p *stopButtonScriptedProvider) markSecondSeen() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.secondSeenOnce {
		p.secondSeenOnce = true
		close(p.secondPromptSeen)
	}
}

// stopButtonLastUser returns the content of the most recent user
// message in the assembled request.
func stopButtonLastUser(messages []provider.Message) string {
	last := ""
	for _, m := range messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	return last
}

// stopButtonHasToolResult reports whether the request carries a
// tool-result message, distinguishing a post-tool retry from a fresh
// first-prompt turn.
func stopButtonHasToolResult(messages []provider.Message) bool {
	for _, m := range messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// RegisterStopButtonSteps binds every step of
// features/dispatch/stop_button.feature onto the godog scenario
// context.
func RegisterStopButtonSteps(ctx *godog.ScenarioContext, s *stopButtonSteps) {
	ctx.Step(`^a session with a running turn blocked on a wedged bash command$`, s.sessionWithRunningTurnBlocked)
	ctx.Step(`^a second prompt is queued behind the running turn$`, s.secondPromptQueuedBehind)
	ctx.Step(`^the user cancels the turn via DELETE /turns/\{turn_id\}$`, s.userCancelsViaDelete)
	ctx.Step(`^the turn settles as "([^"]*)" within (\d+) seconds$`, s.turnSettlesAsWithinSeconds)
	ctx.Step(`^the session meta\.json contains "([^"]*)" set to "([^"]*)"$`, s.sessionMetaJSONContainsSetTo)
	ctx.Step(`^the wedged command's process tree is dead$`, s.wedgedCommandProcessTreeIsDead)
	ctx.Step(`^the queued prompt is released for dispatch$`, s.queuedPromptReleasedForDispatch)
}

// sessionWithRunningTurnBlocked wires the full server-side stack, posts
// the first prompt, and waits until the wedged command's descendant is
// observable via pgrep so the turn is provably blocked on the bash
// tool.
//
// Returns:
//   - An error if any wiring, dispatch, or spawn-observation step fails.
//
// Side effects:
//   - Creates a temp sessions dir, a session, a running turn, and a
//     wedged sleep process on the host.
func (s *stopButtonSteps) sessionWithRunningTurnBlocked() error {
	dir, err := os.MkdirTemp("", "flowstate-stop-button-*")
	if err != nil {
		return fmt.Errorf("creating sessions dir: %w", err)
	}
	s.sessionsDir = dir
	s.marker = fmt.Sprintf("fsstopbtn%d", os.Getpid())
	s.provider = &stopButtonScriptedProvider{
		name:             "stop-button-provider",
		marker:           s.marker,
		secondPromptSeen: make(chan struct{}),
		release:          make(chan struct{}),
	}

	provRegistry := provider.NewRegistry()
	provRegistry.Register(s.provider)
	failoverManager := failover.NewManager(provRegistry, failover.NewHealthManager(), 5*time.Minute)
	failoverManager.SetBasePreferences([]provider.ModelPreference{
		{Provider: s.provider.name, Model: "stop-button-model"},
	})

	manifest := &agent.Manifest{
		ID:   "stop-button-agent",
		Name: "stop-button-agent",
		Metadata: agent.Metadata{
			Role: "worker",
			Goal: "stop-button BDD harness",
		},
		Capabilities: agent.Capabilities{Tools: []string{"bash"}},
	}
	eng := engine.New(engine.Config{
		Registry:        provRegistry,
		FailoverManager: failoverManager,
		Manifest:        *manifest,
		Tools:           []tool.Tool{bashtool.New()},
		TokenCounter:    ctxstore.NewApproximateCounter(),
	})

	s.mgr = session.NewManager(eng)
	s.mgr.SetSessionsDir(dir)
	sess, err := s.mgr.CreateSession("stop-button-agent")
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	s.sessionID = sess.ID

	s.server = api.NewServer(eng, agent.NewRegistry(), nil, nil, api.WithSessionManager(s.mgr))

	resp, err := s.postMessage(stopButtonFirstPrompt)
	if err != nil {
		return err
	}
	if resp.TurnID == "" || resp.Queued {
		return fmt.Errorf("expected first prompt to start a running turn, got turn_id=%q queued=%t", resp.TurnID, resp.Queued)
	}
	s.turnID = resp.TurnID

	deadline := time.Now().Add(stopButtonBlockBudget)
	for {
		found, err := markerProcessesRunning(s.marker)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wedged command matching %q not running within %s; turn never blocked", s.marker, stopButtonBlockBudget)
		}
		time.Sleep(stopButtonPollInterval)
	}
}

// stopButtonMessageResponse is the decoded POST /messages wire shape
// the scenario asserts on.
type stopButtonMessageResponse struct {
	TurnID        string `json:"turn_id"`
	Queued        bool   `json:"queued"`
	QueuePosition int    `json:"queue_position"`
}

// postMessage sends a session message through the real HTTP route.
//
// Returns:
//   - The decoded response and an error covering transport, status, and
//     decoding failures.
func (s *stopButtonSteps) postMessage(content string) (stopButtonMessageResponse, error) {
	var resp stopButtonMessageResponse
	body := strings.NewReader(`{"content":"` + content + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+s.sessionID+"/messages", body)
	rec := httptest.NewRecorder()
	s.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
		return resp, fmt.Errorf("POST /messages returned %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return resp, fmt.Errorf("decoding POST /messages response: %w", err)
	}
	return resp, nil
}

// secondPromptQueuedBehind posts the follow-up prompt while the first
// turn is still running and asserts the dispatcher queued it.
//
// Returns:
//   - An error when the response does not report a queued prompt.
func (s *stopButtonSteps) secondPromptQueuedBehind() error {
	resp, err := s.postMessage(stopButtonSecondPrompt)
	if err != nil {
		return err
	}
	if !resp.Queued {
		return fmt.Errorf("expected second prompt to be queued, got turn_id=%q queued=%t", resp.TurnID, resp.Queued)
	}
	return nil
}

// userCancelsViaDelete presses stop through the real cancel route and
// anchors the 15-second settle window.
//
// Returns:
//   - An error when the route does not answer 200 OK.
//
// Side effects:
//   - Records s.cancelledAt.
func (s *stopButtonSteps) userCancelsViaDelete() error {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+s.sessionID+"/turns/"+s.turnID, nil)
	rec := httptest.NewRecorder()
	s.cancelledAt = time.Now()
	s.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("DELETE /turns/{turn_id} returned %d: %s", rec.Code, rec.Body.String())
	}
	return nil
}

// turnStatus fetches the turn's wire status through the real GET route.
func (s *stopButtonSteps) turnStatus() (string, error) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+s.sessionID+"/turns/"+s.turnID, nil)
	rec := httptest.NewRecorder()
	s.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return "", fmt.Errorf("GET /turns/{turn_id} returned %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("decoding GET /turns/{turn_id} response: %w", err)
	}
	return resp.Status, nil
}

// turnSettlesAsWithinSeconds polls the turn endpoint until the status
// reaches the expected terminal value inside the budget measured from
// the cancel.
//
// Returns:
//   - nil once settled, or an error naming the last observed status.
func (s *stopButtonSteps) turnSettlesAsWithinSeconds(status string, seconds int) error {
	budget := time.Duration(seconds) * time.Second
	deadline := s.cancelledAt.Add(budget)
	var last string
	for {
		current, err := s.turnStatus()
		if err != nil {
			return err
		}
		last = current
		if current == status {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("turn did not settle as %q within %s; last status %q", status, budget, last)
		}
		time.Sleep(stopButtonPollInterval)
	}
}

// sessionMetaJSONContainsSetTo polls the session sidecar until the
// expected key carries the expected value.
//
// Returns:
//   - nil once the sidecar matches, or an error after the budget. The
//     failure error carries the observed session status and persisted
//     message shapes so a Wave-1 cancel-stamping gap is diagnosable
//     from the scenario output alone.
func (s *stopButtonSteps) sessionMetaJSONContainsSetTo(key, value string) error {
	if key != "failure_reason" {
		return fmt.Errorf("unsupported meta.json key %q", key)
	}
	deadline := time.Now().Add(stopButtonMetaBudget)
	var last string
	for {
		meta, err := session.LoadSessionMetadata(s.sessionsDir, s.sessionID)
		if err == nil && meta != nil {
			last = meta.FailureReason
			if meta.FailureReason == value {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("meta.json failure_reason did not become %q within %s; last observed %q; diagnostics: %s",
				value, stopButtonMetaBudget, last, s.cancelDiagnostics())
		}
		time.Sleep(stopButtonPollInterval)
	}
}

// cancelDiagnostics renders the post-cancel session state for failure
// reporting: status, failure_reason on the live session, and the
// persisted message roles with stop reasons.
func (s *stopButtonSteps) cancelDiagnostics() string {
	sess, err := s.mgr.GetSession(s.sessionID)
	if err != nil {
		return fmt.Sprintf("session load failed: %v", err)
	}
	var shapes []string
	for _, m := range sess.Messages {
		shapes = append(shapes, fmt.Sprintf("%s(stop=%q)", m.Role, m.StopReason))
	}
	return fmt.Sprintf("status=%q failure_reason=%q messages=%v",
		sess.Status, sess.FailureReason, shapes)
}

// wedgedCommandProcessTreeIsDead asserts via pgrep that no process
// carrying the scenario marker survives the cancellation.
//
// Returns:
//   - nil once pgrep finds no marker match, or an error after the
//     budget.
//
// Side effects:
//   - On failure, force-kills the marker processes so the failed
//     scenario does not leak a five-minute sleep onto the host.
func (s *stopButtonSteps) wedgedCommandProcessTreeIsDead() error {
	deadline := time.Now().Add(stopButtonTeardownBudget)
	for {
		found, err := markerProcessesRunning(s.marker)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if time.Now().After(deadline) {
			killMarkerProcesses(s.marker)
			return fmt.Errorf("wedged command processes matching %q still running after %s", s.marker, stopButtonTeardownBudget)
		}
		time.Sleep(stopButtonPollInterval)
	}
}

// queuedPromptReleasedForDispatch asserts the queued prompt's turn
// reached the provider after the cancelled turn settled, then releases
// the held stream so the follow-up turn can complete.
//
// Returns:
//   - nil once the provider observed the second prompt, or a timeout
//     error.
//
// Side effects:
//   - Closes the provider's release channel.
func (s *stopButtonSteps) queuedPromptReleasedForDispatch() error {
	select {
	case <-s.provider.secondPromptSeen:
		close(s.provider.release)
		return nil
	case <-time.After(stopButtonReleaseBudget):
		killMarkerProcesses(s.marker)
		return fmt.Errorf("queued prompt was not released for dispatch within %s", stopButtonReleaseBudget)
	}
}
