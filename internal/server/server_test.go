package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var errFakeUsage = errors.New("usage: unknown status")
var errFakeMissing = errors.New("unknown session 01a1f000-0000-4000-8000-000000000002")
var errFakeTerminal = errors.New("session 01a1f000-0000-4000-8000-000000000001 is cancelled")
var errFakeUnmapped = errors.New("boom: disk detached")
var fakeCodes = map[error]string{
	errFakeUsage: "usage", errFakeMissing: "session_not_found", errFakeTerminal: "session_terminal",
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type fakePorts struct {
	sessions  []Session
	events    []Event
	err       error
	writeErr  error
	closed    Session
	diagnosis Diagnosis
}

func (f *fakePorts) ports() Ports {
	return Ports{
		ListSessions: func(_ context.Context, _ string) ([]Session, error) {
			if f.err != nil {
				return nil, f.err
			}
			return f.sessions, nil
		},
		InspectSession: func(_ context.Context, _ string) (Session, []Event, error) {
			if f.err != nil {
				return Session{}, nil, f.err
			}
			return f.sessions[0], f.events, nil
		},
		CloseSession: func(_ context.Context, _ string) (Session, error) {
			if f.writeErr != nil {
				return Session{}, f.writeErr
			}
			return f.closed, nil
		},
		CancelSession: func(_ context.Context, _ string) (Session, error) {
			if f.writeErr != nil {
				return Session{}, f.writeErr
			}
			return f.closed, nil
		},
		DiagnoseSession: func(_ context.Context, _ string) (Diagnosis, error) {
			if f.err != nil {
				return Diagnosis{}, f.err
			}
			return f.diagnosis, nil
		},
		ErrCode: func(err error) string { return fakeCodes[err] },
	}
}
func sampleServer() (*Server, *fakePorts) {
	stamp := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := &fakePorts{
		sessions: []Session{{ID: "01a1f000-0000-4000-8000-000000000001", CreatedAt: stamp, UpdatedAt: stamp,
			Status: "active", Provider: "z.ai", ModelID: "glm-5.3", Harness: "pi", ConfigSHA: "3f7a"}},
		events: []Event{{SessionID: "01a1f000-0000-4000-8000-000000000001", Seq: 1, CreatedAt: stamp,
			Actor: "user", Type: "turn.start", Payload: json.RawMessage(`{"task":"demo"}`), PayloadSHA: "9c2a"}},
	}
	closed := f.sessions[0]
	closed.Status = "cancelled"
	f.closed = closed
	pool, available := int64(120000), int64(90000)
	f.diagnosis = Diagnosis{SessionID: closed.ID, Status: "active", Crashed: true, RunActivity: true,
		PendingMessages: 2, Reserved: 10000, Pool: &pool, Available: &available}
	return New(f.ports()), f
}

type reply struct {
	status int
	body   string
}

func doRequest(s *Server, method, target, bearer string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func schemaError(key, data string) error {
	raw, err := os.ReadFile("../../schemas/cli.json")
	must(err)
	var combined map[string]any
	must(json.Unmarshal(raw, &combined))
	compiler := jsonschema.NewCompiler()
	must(compiler.AddResource(key, combined[key]))
	compiled, err := compiler.Compile(key)
	must(err)
	var decoded any
	must(json.Unmarshal([]byte(data), &decoded))
	return compiled.Validate(decoded)
}

var inspectJSON = `{"id":"01a1f000-0000-4000-8000-000000000001","created_at_utc":"2026-11-20T12:00:00Z","updated_at_utc":"2026-11-20T12:00:00Z",` +
	`"status":"active","provider":"z.ai","model_id":"glm-5.3","harness":"pi","config_sha256":"3f7a","parent_id":"","events":[` +
	`{"session_id":"01a1f000-0000-4000-8000-000000000001","seq":1,"created_at_utc":"2026-11-20T12:00:00Z","actor":"user",` +
	`"type":"turn.start","payload":{"task":"demo"},"payload_sha256":"9c2a"}]}`

var closedJSON = `{"id":"01a1f000-0000-4000-8000-000000000001","created_at_utc":"2026-11-20T12:00:00Z","updated_at_utc":"2026-11-20T12:00:00Z",` +
	`"status":"cancelled","provider":"z.ai","model_id":"glm-5.3","harness":"pi","config_sha256":"3f7a","parent_id":""}`

var _ = Describe("Serve S1 transport", func() {
	It("maps the closed code table onto HTTP statuses", func() {
		want := map[string]int{"usage": 400, "session_not_found": 404, "session_terminal": 409, "session_cancelled": 409, "budget_exceeded": 409,
			"receipt_invalid": 422, "provider_error": 502, "replay_mismatch": 500, "config_invalid": 500, "internal": 500}
		got := map[string]int{}
		for code := range want {
			got[code] = statusFor(code)
		}
		Expect(got).To(Equal(want))
	})

	DescribeTable("answers request envelopes",
		func(method, target, bearer, token string, portErr error, wantStatus int, wantBody string) {
			must(os.Setenv(TokenEnv, token))
			DeferCleanup(os.Unsetenv, TokenEnv)
			s, f := sampleServer()
			f.err = portErr
			rec := doRequest(s, method, target, bearer)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{wantStatus, wantBody}))
		},
		Entry("invalid status filter is 400 usage", http.MethodGet, "/sessions?status=bogus", "", "", errFakeUsage, http.StatusBadRequest, `{"code":"usage","message":"usage: unknown status"}`),
		Entry("POST /sessions is 405 usage", http.MethodPost, "/sessions", "", "", nil, http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`),
		Entry("unknown session is 404 session_not_found", http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000002", "", "", errFakeMissing, http.StatusNotFound, `{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`),
		Entry("unknown route is 404 usage", http.MethodGet, "/nope", "", "", nil, http.StatusNotFound, `{"code":"usage","message":"unknown route"}`),
		Entry("wrong bearer is 401", http.MethodGet, "/health", "wrong", "s3cret", nil, http.StatusUnauthorized, `{"code":"usage","message":"unauthorized"}`),
		Entry("correct bearer with token set is 200", http.MethodGet, "/health", "s3cret", "s3cret", nil, http.StatusOK, `{"status":"ok"}`),
		Entry("health is 200 without a token", http.MethodGet, "/health", "", "", nil, http.StatusOK, `{"status":"ok"}`),
		Entry("inspect returns the session with its events", http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001", "", "", nil, http.StatusOK, inspectJSON),
	)

	It("returns the session-list shape validated against schemas/cli.json", func() {
		s, _ := sampleServer()
		Expect(schemaError("session-list", doRequest(s, http.MethodGet, "/sessions", "").Body.String())).To(Succeed())
	})

	Describe("session writes", func() {
		It("closes an active session with 200 and the closed session body", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/close", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, closedJSON}))
		})

		It("maps a terminal close onto 409 session_terminal", func() {
			s, f := sampleServer()
			f.writeErr = errFakeTerminal
			rec := doRequest(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/close", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusConflict,
				`{"code":"session_terminal","message":"session 01a1f000-0000-4000-8000-000000000001 is cancelled"}`}))
		})

		It("cancels an active session with 200 and the cancelled session body", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/cancel", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, closedJSON}))
		})

		It("maps a terminal cancel onto 409 session_terminal", func() {
			s, f := sampleServer()
			f.writeErr = errFakeTerminal
			rec := doRequest(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/cancel", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusConflict,
				`{"code":"session_terminal","message":"session 01a1f000-0000-4000-8000-000000000001 is cancelled"}`}))
		})

		It("rejects a GET on the close route with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/close", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
		})
	})

	Describe("session doctor", func() {
		It("returns the diagnosis shape validated against schemas/cli.json session-doctor", func() {
			s, _ := sampleServer()
			Expect(schemaError("session-doctor", doRequest(s, http.MethodGet,
				"/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").Body.String())).To(Succeed())
		})

		It("maps an unknown session onto 404 session_not_found", func() {
			s, f := sampleServer()
			f.err = errFakeMissing
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000002/doctor", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusNotFound,
				`{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`}))
		})
	})

	It("maps an empty ErrCode onto 500 internal", func() {
		s, f := sampleServer()
		f.err = errFakeUnmapped
		rec := doRequest(s, http.MethodGet, "/sessions", "")
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusInternalServerError,
			`{"code":"internal","message":"boom: disk detached"}`}))
	})

	It("configures a 30s write timeout on the http server construction", func() {
		s, _ := sampleServer()
		Expect(s.httpServer().WriteTimeout).To(Equal(30 * time.Second))
	})

	It("shuts down gracefully on context cancellation", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		must(err)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- New((&fakePorts{}).ports()).Serve(ctx, l) }()
		cancel()
		Eventually(done).WithTimeout(2 * time.Second).WithPolling(10 * time.Millisecond).
			To(Receive(Succeed()))
	})
})
