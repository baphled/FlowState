package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const streamID = "01a1f000-0000-4000-8000-000000000001"

// streamServer seeds the follow fake: a two-event log and a polled status.
func streamServer() (*Server, *fakePorts) {
	stamp := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := &fakePorts{
		streamEvents: []Event{
			{SessionID: streamID, Seq: 1, CreatedAt: stamp, Actor: "user", Type: "turn.start",
				Payload: json.RawMessage(`{"task":"demo"}`), PayloadSHA: "9c2a"},
			{SessionID: streamID, Seq: 2, CreatedAt: stamp, Actor: "engine", Type: "turn.end",
				Payload: json.RawMessage(`{"status":"completed"}`), PayloadSHA: "8d2b"},
		},
		streamStatus: "active",
	}
	return New(f.ports()), f
}

// setStatus mutates the polled stream status under the fake's lock.
func (f *fakePorts) setStatus(status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamStatus = status
}

// shrinkPoll drops the SSE poll cadence for one spec and restores it after.
func shrinkPoll() {
	old := streamPollInterval
	streamPollInterval = time.Millisecond
	DeferCleanup(func() { streamPollInterval = old })
}

// streamEvent1JSON/streamEvent2JSON pin the seeded events' wire bodies.
var (
	streamEvent1JSON   = `{"session_id":"01a1f000-0000-4000-8000-000000000001","seq":1,"created_at_utc":"2026-11-20T12:00:00Z","actor":"user","type":"turn.start","payload":{"task":"demo"},"payload_sha256":"9c2a"}`
	streamEvent2JSON   = `{"session_id":"01a1f000-0000-4000-8000-000000000001","seq":2,"created_at_utc":"2026-11-20T12:00:00Z","actor":"engine","type":"turn.end","payload":{"status":"completed"},"payload_sha256":"8d2b"}`
	streamTerminalJSON = `data: {"session_id":"` + streamID + `","status":"completed"}` + "\n\n"
)

var _ = Describe("Events snapshot", func() {
	It("answers the ordered events past the cursor as the session-events array", func() {
		s, _ := streamServer()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events", "")
		must(schemaError("session-events", rec.Body.String()))
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
			`[` + streamEvent1JSON + `,` + streamEvent2JSON + `]`}))
	})

	It("returns only events after the after cursor", func() {
		s, _ := streamServer()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events?after=1", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, `[` + streamEvent2JSON + `]`}))
	})

	It("answers 404 session_not_found for an unknown session", func() {
		s, f := streamServer()
		f.streamErr = errFakeMissing
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusNotFound,
			`{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`}))
	})

	It("rejects a malformed after cursor with usage", func() {
		s, _ := streamServer()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events?after=soon", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
			`{"code":"usage","message":"after must be an integer"}`}))
	})
})

var _ = Describe("Events SSE stream", func() {
	It("emits the seeded events in order as SSE frames", func() {
		s, f := streamServer()
		f.setStatus("completed")
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream", "")
		Expect([]string{rec.Header().Get("Content-Type"), rec.Body.String()}).To(Equal([]string{"text/event-stream",
			"data: " + streamEvent1JSON + "\n\ndata: " + streamEvent2JSON + "\n\n" + streamTerminalJSON}))
	})

	It("ends the stream with a terminal status frame", func() {
		s, f := streamServer()
		f.setStatus("completed")
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream?after=2", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, streamTerminalJSON}))
	})

	It("streams events that arrive across polls before terminal", func() {
		shrinkPoll()
		s, f := streamServer()
		f.mu.Lock()
		f.deferEvents = 1 // first poll answers empty: the event lands across polls
		f.streamEvents = []Event{{SessionID: streamID, Seq: 1, CreatedAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Actor: "user",
			Type: "turn.start", Payload: json.RawMessage(`{"task":"demo"}`), PayloadSHA: "9c2a"}}
		f.streamStatus = "completed"
		f.mu.Unlock()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream", "")
		Expect(rec.Body.String()).To(SatisfyAll(
			ContainSubstring(`"seq":1`),
			MatchRegexp(`(?s)"seq":1.*"status":"completed"`)))
	})

	It("frames events committed with the terminal status between two reads", func() {
		shrinkPoll()
		s, f := streamServer()
		f.onEvents = func() {
			f.onEvents = nil
			f.streamEvents = append(f.streamEvents, Event{SessionID: streamID, Seq: 3, Actor: "engine",
				Type: "session.cancelled", Payload: json.RawMessage(`{}`), PayloadSHA: "7e1c"})
			f.streamStatus = "cancelled"
		}
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream", "")
		Expect(rec.Body.String()).To(MatchRegexp(`(?s)"seq":3.*"status":"cancelled"`))
	})

	It("applies the token middleware", func() {
		s, _ := streamServer()
		unauth := *New(s.ports)
		unauth.token = "s3cret"
		rec := doRequest(&unauth, http.MethodGet, "/sessions/"+streamID+"/events/stream", "wrong")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("stops when the request context is cancelled mid-stream", func() {
		shrinkPoll()
		s, _ := streamServer()
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/sessions/"+streamID+"/events/stream", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.Handler().ServeHTTP(rec, req)
		}()
		cancel()
		Eventually(done).WithTimeout(2 * time.Second).WithPolling(20 * time.Millisecond).Should(BeClosed())
	})
})
