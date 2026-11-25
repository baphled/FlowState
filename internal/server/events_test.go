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

	It("answers 200 with the whole log for after=-1, since the log starts at seq 1", func() {
		s, _ := streamServer()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events?after=-1", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
			`[` + streamEvent1JSON + `,` + streamEvent2JSON + `]`}))
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

	It("streams the whole log for after=-1, since the log starts at seq 1", func() {
		s, f := streamServer()
		f.setStatus("completed")
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream?after=-1", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
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
		s, f := streamServer()
		inLoop := make(chan struct{})
		f.onEvents = func() { // runs under the fake's lock after the first poll read
			f.onEvents = nil
			close(inLoop)
		}
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/sessions/"+streamID+"/events/stream", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.Handler().ServeHTTP(rec, req)
		}()
		select { // setup guard: the handler has completed its first poll
		case <-inLoop:
		case <-time.After(2 * time.Second):
			panic("stream handler never reached its poll loop")
		}
		cancel()
		Eventually(done).WithTimeout(2 * time.Second).WithPolling(20 * time.Millisecond).Should(BeClosed())
	})
})

// openIdleStream starts the follow handler over a real listener with a log
// that never produces a frame, so a returned response proves the headers
// were flushed before any frame; the stream is closed on cleanup.
func openIdleStream() *http.Response {
	shrinkPoll()
	s, f := streamServer()
	f.streamEvents = nil
	handler := s.Handler()
	returned := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		handler.ServeHTTP(w, r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	DeferCleanup(func() {
		cancel()
		// Close waits for the handler, so a handler that ignores the cancel
		// would hang the suite: require it returned, and only then close (a
		// failure skips Close, leaking the handler but not hanging).
		select {
		case <-returned:
			ts.Close()
		case <-time.After(2 * time.Second):
			panic("stream handler ignored the request cancellation")
		}
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sessions/"+streamID+"/events/stream", nil)
	if err != nil {
		panic(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = resp.Body.Close() })
	return resp
}

var _ = Describe("Events SSE headers", func() {
	It("sets the event-stream content type before any frame", func() {
		Expect(openIdleStream().Header.Get("Content-Type")).To(Equal("text/event-stream"))
	})

	It("disables caching before any frame", func() {
		Expect(openIdleStream().Header.Get("Cache-Control")).To(Equal("no-cache"))
	})
})

// The session is completed, so a stream that wrongly accepted the cursor would
// end and fail the assertion at once instead of polling until the suite timeout.
var _ = DescribeTable("Events after cursor",
	func(path, message string) {
		s, f := streamServer()
		f.setStatus("completed")
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+path, "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
			`{"code":"usage","message":"` + message + `"}`}))
	},
	Entry("events unparseable cursor", "/events?after=%zz", "invalid query string"),
	Entry("events unparseable other key", "/events?after=1&x=%zz", "invalid query string"),
	Entry("events empty value", "/events?after=", "after must be an integer"),
	Entry("events non-integer", "/events?after=abc", "after must be an integer"),
	Entry("events repeated cursor", "/events?after=1&after=2", "after must be given once"),
	Entry("stream unparseable cursor", "/events/stream?after=%zz", "invalid query string"),
	Entry("stream empty value", "/events/stream?after=", "after must be an integer"),
	Entry("stream repeated cursor", "/events/stream?after=1&after=2", "after must be given once"),
)

var _ = Describe("Events carrying a context.compacted event", func() {
	const compactedJSON = `{"session_id":"` + streamID + `","seq":3,"created_at_utc":"2026-11-20T12:00:00Z","actor":"engine",` +
		`"type":"context.compacted","payload":{"first_kept_seq":2,"model":"m","reason":"auto","summary":"SUM",` +
		`"tokens_after":300,"tokens_before":900},"payload_sha256":"5d3e"}`
	seed := func() (*Server, *fakePorts) {
		s, f := streamServer()
		f.streamEvents = append(f.streamEvents, Event{SessionID: streamID, Seq: 3,
			CreatedAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Actor: "engine", Type: "context.compacted",
			Payload: json.RawMessage(`{"first_kept_seq":2,"model":"m","reason":"auto","summary":"SUM",` +
				`"tokens_after":300,"tokens_before":900}`), PayloadSHA: "5d3e"})
		return s, f
	}

	It("lists it in the snapshot like any other type", func() {
		s, _ := seed()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events?after=2", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, `[` + compactedJSON + `]`}))
	})

	It("validates the snapshot against the session-events schema", func() {
		s, _ := seed()
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events", "")
		Expect(schemaError("session-events", rec.Body.String())).To(Succeed())
	})

	It("carries it in the follow stream as a data frame", func() {
		s, f := seed()
		f.setStatus("completed")
		rec := doRequest(s, http.MethodGet, "/sessions/"+streamID+"/events/stream?after=2", "")
		Expect(rec.Body.String()).To(Equal("data: " + compactedJSON + "\n\n" + streamTerminalJSON))
	})
})
