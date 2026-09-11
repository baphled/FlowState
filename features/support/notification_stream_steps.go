//go:build e2e

// Package support — notification stream step glue for the
// features/notifications/notification_stream.feature scenarios.
package support

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

var (
	errNoNotificationPublished   = errors.New("no notification event was published")
	errUnknownNotificationType   = errors.New("unknown notification type")
	errNotificationTypeMismatch  = errors.New("notification type mismatch")
	errNotificationSeverityMisat = errors.New("notification severity mismatch")
	errMessageMissing            = errors.New("notification message missing expected substring")
	errStreamNoPayload           = errors.New("expected the SSE stream to emit a payload")
	errStreamUnexpectedPayload   = errors.New("expected the SSE stream to emit no notification payloads")
	errStreamMissingKey          = errors.New("SSE payload missing a web-contract key")
	errStreamNotConnected        = errors.New("SSE endpoint unavailable")
)

// NotificationStreamSteps holds state for the notification SSE scenarios.
type NotificationStreamSteps struct {
	bus         *eventbus.EventBus
	server      *httptest.Server
	published   []*events.NotificationEvent
	lastPayload map[string]any
	sseURL      string
	// republish re-emits the Given-step's event after the SSE client
	// has connected, so the stream observes it live (SSE handlers
	// subscribe on connect; pre-connect publishes are not replayed).
	republish func()
}

// RegisterNotificationStreamSteps wires the notification stream steps.
func RegisterNotificationStreamSteps(ctx *godog.ScenarioContext) {
	steps := &NotificationStreamSteps{}
	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		steps.reset()
		return bctx, nil
	})
	ctx.After(func(bctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		steps.teardown()
		return bctx, nil
	})

	ctx.Step(`^the event bus is running$`, steps.busRunning)
	ctx.Step(`^the notification SSE endpoint is available at "/api/v1/notifications/events"$`, steps.endpointAvailable)
	ctx.Step(`^a delegated turn completes successfully$`, steps.turnCompletes)
	ctx.Step(`^a session task fails with reason "([^"]*)"$`, steps.taskFails)
	ctx.Step(`^a provider enters a cooldown$`, steps.providerCooldown)
	ctx.Step(`^the active provider fails over to another provider$`, steps.providerFailover)
	ctx.Step(`^a notification event of type "([^"]*)" is published$`, steps.notificationOfType)
	ctx.Step(`^a tool execute event is published$`, steps.toolExecutePublished)
	ctx.Step(`^a client subscribes to the notification SSE endpoint$`, steps.clientSubscribes)
	ctx.Step(`^the notification severity is "([^"]*)"$`, steps.severityIs)
	ctx.Step(`^the notification message contains "([^"]*)"$`, steps.messageContains)
	ctx.Step(`^the SSE stream emits a JSON payload with keys (.+)$`, steps.streamEmitsKeys)
	ctx.Step(`^the SSE stream emits no notification payloads$`, steps.streamEmitsNothing)
}

func (s *NotificationStreamSteps) reset() {
	s.published = nil
	s.lastPayload = nil
	s.republish = nil
}

func (s *NotificationStreamSteps) teardown() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *NotificationStreamSteps) busRunning() {
	s.bus = eventbus.NewEventBus()
	s.bus.Subscribe(events.EventNotification, func(msg any) {
		if evt, ok := msg.(*events.NotificationEvent); ok {
			s.published = append(s.published, evt)
		}
	})
}

func (s *NotificationStreamSteps) endpointAvailable() error {
	if s.bus == nil {
		s.busRunning()
	}
	srv := api.NewServer(nil, nil, nil, nil, api.WithEventBus(s.bus))
	s.server = httptest.NewServer(srv.Handler())
	return nil
}

func (s *NotificationStreamSteps) requireLast() (*events.NotificationEvent, error) {
	if len(s.published) == 0 {
		return nil, errNoNotificationPublished
	}
	return s.published[len(s.published)-1], nil
}

func (s *NotificationStreamSteps) turnCompletes() error {
	s.bus.Publish(events.EventNotification, events.NewNotificationEvent(events.NotificationEventData{
		ID:       "chain-turn",
		Type:     events.NotificationTypeTurnComplete,
		Severity: events.NotificationSeverityInfo,
		Message:  "Delegated turn completed",
	}))
	return nil
}

func (s *NotificationStreamSteps) taskFails(reason string) error {
	s.bus.Publish(events.EventNotification, events.NewNotificationEvent(events.NotificationEventData{
		ID:       "chain-fail",
		Type:     events.NotificationTypeTaskFailed,
		Severity: events.NotificationSeverityError,
		Message:  "Task failed: " + reason,
	}))
	return nil
}

func (s *NotificationStreamSteps) providerCooldown() error {
	s.bus.Publish(events.EventNotification, events.NewNotificationEvent(events.NotificationEventData{
		ID:       "cooldown:openai:gpt-4o",
		Type:     events.NotificationTypeCooldown,
		Severity: events.NotificationSeverityWarning,
		Message:  "Provider openai entered a cooldown",
		Provider: "openai",
		Model:    "gpt-4o",
	}))
	return nil
}

func (s *NotificationStreamSteps) providerFailover() error {
	s.bus.Publish(events.EventNotification, events.NewNotificationEvent(events.NotificationEventData{
		ID:       "failover:openai:gpt-4o",
		Type:     events.NotificationTypeFailover,
		Severity: events.NotificationSeverityWarning,
		Message:  "Failing over from openai/gpt-4o",
		Provider: "openai",
		Model:    "gpt-4o",
	}))
	return nil
}

func (s *NotificationStreamSteps) notificationOfType(typ string) error {
	switch typ {
	case events.NotificationTypeTurnComplete:
		s.republish = func() { s.turnCompletes() }
		return s.turnCompletes()
	case events.NotificationTypeTaskFailed:
		s.republish = func() { s.taskFails("provider unavailable") }
		return s.taskFails("provider unavailable")
	case events.NotificationTypeCooldown:
		s.republish = func() { _ = s.providerCooldown() }
		return s.providerCooldown()
	case events.NotificationTypeFailover:
		s.republish = func() { _ = s.providerFailover() }
		return s.providerFailover()
	}
	return errUnknownNotificationType
}

func (s *NotificationStreamSteps) toolExecutePublished() error {
	s.bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{}))
	return nil
}

func (s *NotificationStreamSteps) readFrames(ctx context.Context, resp *http.Response) ([]string, error) {
	reader := bufio.NewReader(resp.Body)
	var frames []string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return frames, nil
		}
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
		select {
		case <-ctx.Done():
			return frames, nil
		default:
		}
	}
	return frames, nil
}

// clientSubscribes connects to the SSE endpoint, waits for the
// connected handshake frame (which guarantees the handler has
// subscribed to the bus), then re-publishes the Given-step event so
// the stream receives it live. This avoids the publish-before-
// subscribe race inherent to the scenario ordering.
func (s *NotificationStreamSteps) clientSubscribes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.server.URL+"/api/v1/notifications/events", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errStreamNotConnected
	}
	defer resp.Body.Close()
	_, err = bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		return errStreamNoPayload
	}
	// No republish registered (e.g. the non-notification Given step)
	// — the stream is expected to stay silent; drain until deadline.
	if s.republish == nil {
		_, _ = s.readFrames(ctx, resp)
		return nil
	}
	s.republish()
	frames, err := s.readFrames(ctx, resp)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		var payload map[string]any
		if json.Unmarshal([]byte(frame), &payload) == nil {
			if _, isNotification := payload["Type"]; isNotification {
				s.lastPayload = payload
				return nil
			}
		}
	}
	return errStreamNoPayload
}

func (s *NotificationStreamSteps) severityIs(severity string) error {
	evt, err := s.requireLast()
	if err != nil {
		return err
	}
	if evt.Data.Severity != severity {
		return errNotificationSeverityMisat
	}
	return nil
}

func (s *NotificationStreamSteps) messageContains(substr string) error {
	evt, err := s.requireLast()
	if err != nil {
		return err
	}
	if !strings.Contains(evt.Data.Message, substr) {
		return errMessageMissing
	}
	return nil
}

func (s *NotificationStreamSteps) streamEmitsKeys(keys string) error {
	if s.lastPayload == nil {
		return errStreamNoPayload
	}
	for _, key := range strings.Split(keys, ",") {
		key = strings.Trim(strings.TrimSpace(key), `"`)
		if key == "" {
			continue
		}
		if _, ok := s.lastPayload[key]; !ok {
			return errStreamMissingKey
		}
	}
	return nil
}

func (s *NotificationStreamSteps) streamEmitsNothing() error {
	if s.lastPayload != nil {
		return errStreamUnexpectedPayload
	}
	return nil
}
