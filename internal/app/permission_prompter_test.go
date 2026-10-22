package app

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/permissionrequest"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tracer"
)

// Permission Mode ModeAskUser Extension plan (May 2026) Slice 2.
//
// These tests pin the app-level prompter wiring: the prompter
// publishes EventPermissionRequired, blocks on the registry, and the
// permission_pending gauge subscriber wakes on resolution. Memory
// alignment:
//
//   - feedback_eventlogger_catalog_subscriber_is_dead_comment — the
//     catalog claims a permission_pending-gauge subscriber, this
//     test pins it.
//   - feedback_published_unsubscribed_events_dead_surface — verify
//     the bus event reaches a real subscriber.

// recordingRecorder is a tracer.Recorder fake that captures the
// inc/dec counts on the permission_pending gauge so the test can
// assert the wire-up without a real Prometheus registry.
type permissionTestRecorder struct {
	tracer.NoopRecorder
	inc int
	dec int
}

func (r *permissionTestRecorder) IncPermissionPending() { r.inc++ }
func (r *permissionTestRecorder) DecPermissionPending() { r.dec++ }

func TestPermissionPrompterPublishesRequiredEventAndWaitsForGrant(t *testing.T) {
	t.Parallel()

	bus := eventbus.NewEventBus()
	registry := permissionrequest.NewRegistry()
	recorder := &permissionTestRecorder{}
	subscribePermissionGaugeHook(bus, recorder)

	// Capture the published required event so we can assert the
	// payload reached subscribers.
	receivedReq := make(chan *events.PermissionRequiredEvent, 1)
	bus.Subscribe(events.EventPermissionRequired, func(msg any) {
		if evt, ok := msg.(*events.PermissionRequiredEvent); ok {
			receivedReq <- evt
		}
	})

	prompter := newPermissionPrompter(registry, bus, recorder, 500*time.Millisecond)

	// Drive a pathguard-side RequestPermission concurrently — the
	// resolution path needs the Wait goroutine parked first.
	type result struct {
		grant pathguard.PermissionGrant
	}
	out := make(chan result, 1)
	go func() {
		grant := prompter.RequestPermission(context.Background(), pathguard.PermissionRequest{
			ToolName:     "read",
			Resource:     "/vault/secret.md",
			AgentName:    "coordinator",
			DenialReason: "access denied",
			SessionID:    "sess-1",
			Mode:         "ask",
		})
		out <- result{grant: grant}
	}()

	// Wait for the required event so we know the registry has the
	// pending entry. The event includes the registry's minted
	// request_id which we'll use to Resolve.
	var evt *events.PermissionRequiredEvent
	select {
	case evt = <-receivedReq:
	case <-time.After(2 * time.Second):
		t.Fatal("EventPermissionRequired never fired — the prompter is not publishing the bus event before blocking on Wait")
	}

	if evt.Data.RequestID == "" {
		t.Fatal("EventPermissionRequired payload missing request_id — Slice 3 grant handler cannot correlate without it")
	}
	if evt.Data.ToolName != "read" {
		t.Fatalf("EventPermissionRequired.ToolName = %q, want %q", evt.Data.ToolName, "read")
	}
	if evt.Data.Mode != "ask" {
		t.Fatalf("EventPermissionRequired.Mode = %q, want %q — payload must stamp the active mode", evt.Data.Mode, "ask")
	}
	if recorder.inc != 1 {
		t.Fatalf("permission_pending gauge MUST be incremented exactly once on Register; got %d", recorder.inc)
	}

	// Operator clicks Allow Session — Resolve via the registry +
	// publish the granted event so the gauge subscriber decrements.
	if err := registry.Resolve(evt.Data.RequestID, permissionrequest.PermissionGrant{
		Scope: permissionrequest.ScopeSession,
	}); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	bus.Publish(events.EventPermissionGranted, events.NewPermissionGrantedEvent(events.PermissionResolutionEventData{
		RequestID: evt.Data.RequestID,
		SessionID: "sess-1",
		Scope:     "session",
	}))

	// Wait for the prompter to return the projected grant.
	select {
	case got := <-out:
		if got.grant.Scope != pathguard.GrantSession {
			t.Fatalf("prompter returned scope %q, want %q — Resolve(ScopeSession) MUST project to pathguard.GrantSession", got.grant.Scope, pathguard.GrantSession)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompter never returned — Wait did not wake on Resolve")
	}

	// The gauge subscriber must wake on the granted event.
	deadline := time.Now().Add(1 * time.Second)
	for recorder.dec < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if recorder.dec != 1 {
		t.Fatalf("permission_pending gauge MUST decrement exactly once on EventPermissionGranted; got %d", recorder.dec)
	}
}

func TestPermissionPrompterTimeoutPublishesTimeoutEvent(t *testing.T) {
	t.Parallel()

	bus := eventbus.NewEventBus()
	registry := permissionrequest.NewRegistry()
	recorder := &permissionTestRecorder{}
	subscribePermissionGaugeHook(bus, recorder)

	timeoutFired := make(chan *events.PermissionTimeoutEvent, 1)
	bus.Subscribe(events.EventPermissionTimeout, func(msg any) {
		if evt, ok := msg.(*events.PermissionTimeoutEvent); ok {
			timeoutFired <- evt
		}
	})

	// 30ms timeout — fires before any Resolve.
	prompter := newPermissionPrompter(registry, bus, recorder, 30*time.Millisecond)

	grant := prompter.RequestToolPermission(context.Background(), engine.EnginePermissionRequest{
		ToolName:  "bash",
		AgentName: "coordinator",
		Resource:  "bash",
		SessionID: "sess-timeout",
		Mode:      "ask",
	})
	if grant.Allowed {
		t.Fatal("timeout MUST resolve to GrantDeny (Allowed=false) — the suspended call cannot proceed without operator consent")
	}

	select {
	case <-timeoutFired:
	case <-time.After(2 * time.Second):
		t.Fatal("EventPermissionTimeout never fired — the prompter must publish on the timeout path for observability")
	}

	deadline := time.Now().Add(1 * time.Second)
	for recorder.dec < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if recorder.dec != 1 {
		t.Fatalf("permission_pending gauge MUST decrement on EventPermissionTimeout (R4 acceptance — no dead Publish); got dec=%d", recorder.dec)
	}
}

// pin: project_flowstate_streamer_request_lifetime_coupling. The
// prompter calls Wait with context.WithoutCancel-detached ctx so a
// tab-close (parent ctx cancel) does NOT cancel the suspended
// goroutine.
func TestPermissionPrompterSurvivesParentContextCancel(t *testing.T) {
	t.Parallel()

	bus := eventbus.NewEventBus()
	registry := permissionrequest.NewRegistry()
	recorder := &permissionTestRecorder{}
	subscribePermissionGaugeHook(bus, recorder)

	receivedReq := make(chan *events.PermissionRequiredEvent, 1)
	bus.Subscribe(events.EventPermissionRequired, func(msg any) {
		if evt, ok := msg.(*events.PermissionRequiredEvent); ok {
			receivedReq <- evt
		}
	})

	prompter := newPermissionPrompter(registry, bus, recorder, 2*time.Second)

	parent, cancelParent := context.WithCancel(context.Background())
	out := make(chan pathguard.GrantScope, 1)
	go func() {
		grant := prompter.RequestPermission(parent, pathguard.PermissionRequest{
			ToolName:  "read",
			Resource:  "/x.md",
			SessionID: "sess-detach",
			Mode:      "ask",
		})
		out <- grant.Scope
	}()

	var evt *events.PermissionRequiredEvent
	select {
	case evt = <-receivedReq:
	case <-time.After(1 * time.Second):
		t.Fatal("PermissionRequired never fired")
	}

	// Cancel the parent — simulates a tab-close. The Wait inside
	// the prompter MUST NOT observe this cancellation.
	cancelParent()

	// Brief settle. If the WithoutCancel barrier leaks, `out`
	// receives a GrantDeny here.
	select {
	case s := <-out:
		t.Fatalf("prompter returned %q after parent cancel — the WithoutCancel boundary leaked. memory:project_flowstate_streamer_request_lifetime_coupling pins this contract", s)
	case <-time.After(50 * time.Millisecond):
		// Expected — still parked.
	}

	// Now genuinely grant the request. Prompter wakes and returns.
	_ = registry.Resolve(evt.Data.RequestID, permissionrequest.PermissionGrant{
		Scope: permissionrequest.ScopeOnce,
	})
	bus.Publish(events.EventPermissionGranted, events.NewPermissionGrantedEvent(events.PermissionResolutionEventData{
		RequestID: evt.Data.RequestID,
	}))

	select {
	case s := <-out:
		if s != pathguard.GrantOnce {
			t.Fatalf("prompter returned scope %q, want GrantOnce", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompter never woke on Resolve — the WithoutCancel barrier blocks the grant signal too?")
	}
}
