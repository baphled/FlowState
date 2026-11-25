package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var errFakeUsage = errors.New("usage: unknown status")
var errFakeMissing = errors.New("unknown session 01a1f000-0000-4000-8000-000000000002")
var errFakeTerminal = errors.New("session 01a1f000-0000-4000-8000-000000000001 is cancelled")
var errFakeUnmapped = errors.New("boom: disk detached")
var errFakeScope = errors.New("usage: steering sender is outside the delegation chain")
var errFakeProvider = errors.New("provider: upstream refused")
var errFakeBudget = errors.New("budget of 5 exceeded after 6 tokens; session paused")
var errFakeManifest = errors.New("usage: manifest name is required")
var errFakeBudgetStop = errors.New("budget exceeded: grant 200 exceeds available 100")
var errFakeGate = errors.New("app: run swarm demo.swarm not accepted: failed gates [no], admitted 1 of 1 packets")
var errFakeReceipt = errors.New(`receipt_invalid: "receipt.json": gate_outcome inconsistent with review verdict`)
var fakeCodes = map[error]string{
	errFakeUsage: "usage", errFakeMissing: "session_not_found", errFakeTerminal: "session_terminal",
	errFakeScope: "usage", errFakeManifest: "usage", errFakeBudgetStop: "budget_exceeded",
	errFakeGate: "receipt_invalid", errFakeReceipt: "receipt_invalid",
	errFakeProvider: "provider_error", errFakeBudget: "budget_exceeded",
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type fakePorts struct {
	sessions        []Session
	events          []Event
	err             error
	writeErr        error
	closed          Session
	diagnosis       Diagnosis
	budget          Budget
	anchored        Budget
	message         Message
	swarmValidation SwarmValidation
	swarmRun        SwarmRun
	swarmErr        error
	receipt         ReceiptVerdict
	receiptErr      error
	mu              sync.Mutex
	streamEvents    []Event
	streamStatus    string
	onEvents        func() // one-shot commit landing right after an events read
	streamErr       error
	deferEvents     int  // polls that answer empty before the log arrives
	logLanded       bool // true once a poll answered the real (non-deferred) log
	runEvents       []Event
	runErr          error
	runOpts         RunOptions
	created         Session
	createErr       error
	createdReq      CreateRequest
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
		BudgetOverview: func(_ context.Context, _ string) (Budget, error) {
			if f.err != nil {
				return Budget{}, f.err
			}
			return f.budget, nil
		},
		SetBudgetPool: func(_ context.Context, _ string, _ int64) (Budget, error) {
			if f.writeErr != nil {
				return Budget{}, f.writeErr
			}
			return f.anchored, nil
		},
		SendSteeringMessage: func(_ context.Context, _, _, _ string) (Message, error) {
			if f.writeErr != nil {
				return Message{}, f.writeErr
			}
			return f.message, nil
		},
		ValidateSwarm: func(_ context.Context, _ string) (SwarmValidation, error) {
			if f.swarmErr != nil {
				return SwarmValidation{}, f.swarmErr
			}
			return f.swarmValidation, nil
		},
		RunSwarm: func(_ context.Context, _, _ string, _ int64) (SwarmRun, error) {
			if f.swarmErr != nil {
				return SwarmRun{}, f.swarmErr
			}
			return f.swarmRun, nil
		},
		ValidateReceipt: func(_ context.Context, _ string) (ReceiptVerdict, error) {
			if f.receiptErr != nil {
				return ReceiptVerdict{}, f.receiptErr
			}
			return f.receipt, nil
		},
		EventsAfter: func(_ context.Context, _ string, afterSeq int64) ([]Event, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.streamErr != nil {
				return nil, f.streamErr
			}
			if f.deferEvents > 0 {
				f.deferEvents--
				return nil, nil
			}
			f.logLanded = true
			if f.onEvents != nil {
				defer f.onEvents() // runs before the unlock above
			}
			out := make([]Event, 0, len(f.streamEvents))
			for _, e := range f.streamEvents {
				if e.Seq > afterSeq {
					out = append(out, e)
				}
			}
			return out, nil
		},
		SessionStatus: func(_ context.Context, _ string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			// While the deferred log has not landed the session stays
			// active, so the stream polls on instead of ending early.
			if !f.logLanded {
				return "active", nil
			}
			return f.streamStatus, nil
		},
		RunSession: func(_ context.Context, _ string, opts RunOptions) ([]Event, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.runOpts = opts
			return f.runEvents, f.runErr
		},
		CreateSession: func(_ context.Context, req CreateRequest) (Session, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.createdReq = req
			return f.created, f.createErr
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
	f.budget = Budget{SessionID: closed.ID, Reserved: 10000}
	anchoredPool, anchoredAvailable := int64(120000), int64(110000)
	f.anchored = Budget{SessionID: closed.ID, Reserved: 10000,
		Pool: &anchoredPool, Available: &anchoredAvailable}
	f.message = Message{ID: "01a1f000-0000-4000-8000-00000000000a", SessionID: closed.ID,
		Sender: "01a1f000-0000-4000-8000-000000000002"}
	f.swarmValidation = SwarmValidation{Name: "demo.swarm", Valid: true}
	f.swarmRun = SwarmRun{Manifest: "demo.swarm", Admitted: []string{"p1"}, Accepted: true,
		Children: []SwarmChild{{Packet: "p1", Session: "01a1f000-0000-4000-8000-000000000003",
			Status: "active", ParentID: closed.ID}}}
	f.receipt = ReceiptVerdict{Path: "receipt.json", Verdict: "valid"}
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

// doBody issues a request carrying a JSON string body.
func doBody(s *Server, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
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
			"max_turns": 409, "receipt_invalid": 422, "provider_error": 502, "replay_mismatch": 500, "config_invalid": 500, "internal": 500, "reservation_unsettled": 500}
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
		Entry("PUT /sessions is 405 usage", http.MethodPut, "/sessions", "", "", nil, http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`),
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

		It("renders the totals of a reported usage and validates against schemas/cli.json", func() {
			s, f := sampleServer()
			f.diagnosis.Usage = DiagnosisUsage{InputTokens: 7, OutputTokens: 3, TurnsReported: 1, TurnsTotal: 2,
				LastContextTokens: 10}
			body := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").Body.String()
			Expect(schemaError("session-doctor", body)).To(Succeed())
		})

		DescribeTable("carries the usage in the body",
			func(usage DiagnosisUsage, want string) {
				s, f := sampleServer()
				f.diagnosis.Usage = usage
				Expect(doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").
					Body.String()).To(ContainSubstring(want))
			},
			Entry("the totals when reported",
				DiagnosisUsage{InputTokens: 7, TurnsReported: 1, TurnsTotal: 2}, `"input_tokens":7`),
			Entry("a null total when no turn reported",
				DiagnosisUsage{TurnsTotal: 2}, `"input_tokens":null`),
			Entry("a null last context when no turn reported",
				DiagnosisUsage{TurnsTotal: 2}, `"last_context_tokens":null`),
			Entry("the turns total", DiagnosisUsage{TurnsTotal: 2}, `"turns_total":2`),
		)

		DescribeTable("carries a truncated diagnosis in the body",
			func(want string) {
				s, f := sampleServer()
				f.diagnosis.FinishReason, f.diagnosis.Truncated = "length", true
				Expect(doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").
					Body.String()).To(ContainSubstring(want))
			},
			Entry("the finish reason", `"finish_reason":"length"`),
			Entry("the truncated flag", `"truncated":true`),
		)

		It("validates a truncated diagnosis against schemas/cli.json session-doctor", func() {
			s, f := sampleServer()
			f.diagnosis.FinishReason, f.diagnosis.Truncated = "length", true
			Expect(schemaError("session-doctor", doRequest(s, http.MethodGet,
				"/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").Body.String())).To(Succeed())
		})

		It("omits finish_reason and truncated when none was recorded", func() {
			s, _ := sampleServer()
			body := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/doctor", "").Body.String()
			Expect(body).NotTo(Or(ContainSubstring("finish_reason"), ContainSubstring("truncated")))
		})

		It("maps an unknown session onto 404 session_not_found", func() {
			s, f := sampleServer()
			f.err = errFakeMissing
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000002/doctor", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusNotFound,
				`{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`}))
		})
	})

	Describe("session budget", func() {
		It("answers GET with the overview body including null pool", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
				`{"session_id":"01a1f000-0000-4000-8000-000000000001","reserved":10000,"budget_pool":null,"available":null}`}))
		})

		It("returns the GET overview shape validated against schemas/cli.json session-budget", func() {
			s, _ := sampleServer()
			Expect(schemaError("session-budget", doRequest(s, http.MethodGet,
				"/sessions/01a1f000-0000-4000-8000-000000000001/budget", "").Body.String())).To(Succeed())
		})

		It("answers POST with the anchored overview body", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", `{"pool":120000}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
				`{"session_id":"01a1f000-0000-4000-8000-000000000001","reserved":10000,"budget_pool":120000,"available":110000}`}))
		})

		It("returns the POST overview shape validated against schemas/cli.json session-budget", func() {
			s, _ := sampleServer()
			Expect(schemaError("session-budget", doBody(s, http.MethodPost,
				"/sessions/01a1f000-0000-4000-8000-000000000001/budget", `{"pool":120000}`).Body.String())).To(Succeed())
		})

		It("rejects a malformed budget body with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", `not json`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"invalid budget body"}`}))
		})

		It("rejects a missing pool with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", `{}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"pool is required"}`}))
		})

		It("rejects a negative pool with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", `{"pool":-5}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"pool must be positive"}`}))
		})

		It("maps an unknown session onto 404 session_not_found", func() {
			s, f := sampleServer()
			f.err = errFakeMissing
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000002/budget", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusNotFound,
				`{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`}))
		})

		It("rejects an unsupported budget method with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodPut, "/sessions/01a1f000-0000-4000-8000-000000000001/budget", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
		})
	})

	Describe("steering message", func() {
		It("answers POST with the recorded message body", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/message",
				`{"from":"01a1f000-0000-4000-8000-000000000002","body":"steer left"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK,
				`{"id":"01a1f000-0000-4000-8000-00000000000a","session_id":"01a1f000-0000-4000-8000-000000000001","sender":"01a1f000-0000-4000-8000-000000000002"}`}))
		})

		It("returns the message shape validated against schemas/cli.json session-message", func() {
			s, _ := sampleServer()
			Expect(schemaError("session-message", doBody(s, http.MethodPost,
				"/sessions/01a1f000-0000-4000-8000-000000000001/message",
				`{"from":"01a1f000-0000-4000-8000-000000000002","body":"steer left"}`).Body.String())).To(Succeed())
		})

		It("rejects a missing field with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/message", `{"from":"root"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"from and body are required"}`}))
		})

		// Real behaviour: app.SendSteeringMessage wraps unknown-session
		// and delegation-scope failures into ErrUsage, so the shared code
		// table answers usage/400 — a 404 is unreachable here.
		It("maps a delegation-scope rejection onto 400 usage, not 404", func() {
			s, f := sampleServer()
			f.writeErr = errFakeScope
			rec := doBody(s, http.MethodPost, "/sessions/01a1f000-0000-4000-8000-000000000001/message",
				`{"from":"outsider","body":"steer left"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"usage: steering sender is outside the delegation chain"}`}))
		})

		It("rejects a GET on the message route with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/sessions/01a1f000-0000-4000-8000-000000000001/message", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
		})
	})

	Describe("swarm validate", func() {
		It("answers POST with the manifest verdict body", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/validate", `{"path":"swarm.yaml"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, `{"name":"demo.swarm","valid":true}`}))
		})

		It("returns the verdict shape validated against schemas/cli.json swarm-validate", func() {
			s, _ := sampleServer()
			Expect(schemaError("swarm-validate", doBody(s, http.MethodPost, "/swarm/validate",
				`{"path":"swarm.yaml"}`).Body.String())).To(Succeed())
		})

		It("rejects a malformed body with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/validate", `not json`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"invalid swarm validate body"}`}))
		})

		It("rejects a missing path with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/validate", `{}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"path is required"}`}))
		})

		It("maps a manifest validation failure onto 400 usage with the selector message", func() {
			s, f := sampleServer()
			f.swarmErr = errFakeManifest
			rec := doBody(s, http.MethodPost, "/swarm/validate", `{"path":"swarm.yaml"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"usage: manifest name is required"}`}))
		})

		It("rejects a GET on the route with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/swarm/validate", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
		})
	})

	Describe("swarm run", func() {
		var runOK, runBody string
		BeforeEach(func() {
			runOK = `{"parent":"01a1f000-0000-4000-8000-000000000001","path":"swarm.yaml","budget":100}`
			runBody = `{"manifest":"demo.swarm","admitted":["p1"],"accepted":true,` +
				`"children":[{"packet":"p1","session":"01a1f000-0000-4000-8000-000000000003",` +
				`"status":"active","parent_id":"01a1f000-0000-4000-8000-000000000001"}]}`
		})

		It("answers POST with the run result body", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run", runOK)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, runBody}))
		})

		It("returns the run shape validated against schemas/cli.json swarm-run", func() {
			s, _ := sampleServer()
			Expect(schemaError("swarm-run", doBody(s, http.MethodPost, "/swarm/run", runOK).Body.String())).To(Succeed())
		})

		It("rejects a malformed body with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run", `not json`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"invalid swarm run body"}`}))
		})

		It("rejects a missing parent with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run", `{"path":"swarm.yaml","budget":100}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"parent is required"}`}))
		})

		It("rejects a missing path with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run", `{"parent":"01a1f000-0000-4000-8000-000000000001","budget":100}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"path is required"}`}))
		})

		It("rejects a missing budget with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run", `{"parent":"01a1f000-0000-4000-8000-000000000001","path":"swarm.yaml"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"budget is required"}`}))
		})

		It("rejects a non-positive budget with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/swarm/run",
				`{"parent":"01a1f000-0000-4000-8000-000000000001","path":"swarm.yaml","budget":0}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"budget must be positive"}`}))
		})

		It("maps an unknown parent onto 404 session_not_found", func() {
			s, f := sampleServer()
			f.swarmErr = errFakeMissing
			rec := doBody(s, http.MethodPost, "/swarm/run", runOK)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusNotFound,
				`{"code":"session_not_found","message":"unknown session 01a1f000-0000-4000-8000-000000000002"}`}))
		})

		It("maps a budget stop onto 409 budget_exceeded", func() {
			s, f := sampleServer()
			f.swarmErr = errFakeBudgetStop
			rec := doBody(s, http.MethodPost, "/swarm/run", runOK)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusConflict,
				`{"code":"budget_exceeded","message":"budget exceeded: grant 200 exceeds available 100"}`}))
		})

		// The app's not-accepted error maps onto receipt_invalid; the
		// shared table renders 422 — the CLI exits 8 for the same class.
		// Only the {code,message} error body is written; no run result.
		It("maps a gate-not-accepted run onto 422 receipt_invalid", func() {
			s, f := sampleServer()
			f.swarmErr = errFakeGate
			rec := doBody(s, http.MethodPost, "/swarm/run", runOK)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusUnprocessableEntity,
				`{"code":"receipt_invalid","message":"app: run swarm demo.swarm not accepted: failed gates [no], admitted 1 of 1 packets"}`}))
		})

		It("rejects a GET on the route with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/swarm/run", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
		})
	})

	Describe("receipt validate", func() {
		It("answers POST with the valid verdict body", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/receipt/validate", `{"path":"receipt.json"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, `{"path":"receipt.json","verdict":"valid"}`}))
		})

		It("answers POST with the invalid verdict body", func() {
			s, f := sampleServer()
			f.receipt.Verdict = "invalid"
			rec := doBody(s, http.MethodPost, "/receipt/validate", `{"path":"receipt.json"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusOK, `{"path":"receipt.json","verdict":"invalid"}`}))
		})

		// The receipt-validate key covers the CLI's array of per-file
		// verdict lines; this single-path endpoint returns one item, so
		// the spec validates the wrapped one-element array.
		It("returns the verdict shape validated against schemas/cli.json receipt-validate", func() {
			s, _ := sampleServer()
			Expect(schemaError("receipt-validate", "["+doBody(s, http.MethodPost, "/receipt/validate",
				`{"path":"receipt.json"}`).Body.String()+"]")).To(Succeed())
		})

		It("rejects a malformed body with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/receipt/validate", `not json`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"invalid receipt body"}`}))
		})

		It("rejects a missing path with 400 usage", func() {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, "/receipt/validate", `{}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"path is required"}`}))
		})

		It("maps a receipt_invalid port error onto 422", func() {
			s, f := sampleServer()
			f.receiptErr = errFakeReceipt
			rec := doBody(s, http.MethodPost, "/receipt/validate", `{"path":"receipt.json"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusUnprocessableEntity,
				`{"code":"receipt_invalid","message":"receipt_invalid: \"receipt.json\": gate_outcome inconsistent with review verdict"}`}))
		})

		It("rejects a GET on the route with 405 usage", func() {
			s, _ := sampleServer()
			rec := doRequest(s, http.MethodGet, "/receipt/validate", "")
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusMethodNotAllowed, `{"code":"usage","message":"method not allowed"}`}))
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

	It("cancels an in-flight read on shutdown instead of waiting it out", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		must(err)
		entered := make(chan struct{})
		p := (&fakePorts{}).ports()
		p.ListSessions = func(ctx context.Context, _ string) ([]Session, error) {
			close(entered)
			<-ctx.Done() // a read that only its request context can end
			return nil, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		// The client keeps its own context: were it tied to the serve
		// context, the disconnect alone would end the handler.
		clientCtx, hangUp := context.WithCancel(context.Background())
		DeferCleanup(hangUp)
		req, err := http.NewRequestWithContext(clientCtx, http.MethodGet,
			"http://"+l.Addr().String()+"/sessions", http.NoBody)
		must(err)
		done := make(chan error, 1)
		go func() { done <- New(p).Serve(ctx, l) }()
		go func() {
			if res, derr := http.DefaultClient.Do(req); derr == nil {
				_ = res.Body.Close()
			}
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			panic("the request never reached its handler")
		}
		cancel()
		Eventually(done).WithTimeout(2 * time.Second).WithPolling(10 * time.Millisecond).
			To(Receive(Succeed()))
	})

	It("lets a read observe its request's cancellation", func() {
		var seen error
		p := (&fakePorts{}).ports()
		p.ListSessions = func(ctx context.Context, _ string) ([]Session, error) {
			seen = ctx.Err()
			return nil, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		New(p).Handler().ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/sessions", nil).WithContext(ctx))
		Expect(seen).To(MatchError(context.Canceled))
	})

	// A read whose client has gone has nobody to answer: the error writer
	// stays silent instead of reporting the cancellation as 500 internal.
	DescribeTable("writes nothing for a read cancelled by a client disconnect",
		func(method, target, body string) {
			s, f := sampleServer()
			f.err, f.streamErr = context.Canceled, context.Canceled
			f.swarmErr, f.receiptErr = context.Canceled, context.Canceled
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec,
				httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(ctx))
			Expect(rec.Body.String()).To(BeEmpty())
		},
		Entry("session list", http.MethodGet, "/sessions", ""),
		Entry("session get", http.MethodGet, "/sessions/s1", ""),
		Entry("doctor", http.MethodGet, "/sessions/s1/doctor", ""),
		Entry("budget", http.MethodGet, "/sessions/s1/budget", ""),
		Entry("events", http.MethodGet, "/sessions/s1/events", ""),
		Entry("swarm validate", http.MethodPost, "/swarm/validate", `{"path":"m.yaml"}`),
		Entry("receipt validate", http.MethodPost, "/receipt/validate", `{"path":"r.json"}`),
	)

	It("still renders a read's own failure after the client has gone", func() {
		s, f := sampleServer()
		f.err = errFakeMissing
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/s1", nil).WithContext(ctx))
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("keeps answering 500 for a cancellation error on a live request", func() {
		s, f := sampleServer()
		f.err = context.Canceled
		Expect(doRequest(s, http.MethodGet, "/sessions", "").Code).To(Equal(http.StatusInternalServerError))
	})

	It("answers 503 unavailable to a read cancelled by shutdown", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		must(err)
		entered := make(chan struct{})
		p := (&fakePorts{}).ports()
		p.ListSessions = func(ctx context.Context, _ string) ([]Session, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- New(p).Serve(ctx, l) }()
		got := make(chan reply, 1)
		go func() {
			req, rerr := http.NewRequestWithContext(context.Background(), http.MethodGet,
				"http://"+l.Addr().String()+"/sessions", http.NoBody)
			must(rerr)
			res, derr := http.DefaultClient.Do(req)
			if derr != nil {
				got <- reply{}
				return
			}
			defer func() { _ = res.Body.Close() }()
			raw, _ := io.ReadAll(res.Body)
			got <- reply{res.StatusCode, string(raw)}
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			panic("the request never reached its handler")
		}
		cancel()
		Eventually(got).WithTimeout(3 * time.Second).WithPolling(10 * time.Millisecond).
			Should(Receive(Equal(reply{http.StatusServiceUnavailable,
				`{"code":"unavailable","message":"server is shutting down"}`})))
	})

	// A write is detached from cancellation, so its own failure keeps its
	// class even when the request context is done.
	It("keeps a cancelled write's error as 500 internal", func() {
		s, f := sampleServer()
		f.writeErr = context.Canceled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec,
			httptest.NewRequest(http.MethodPost, "/sessions/s1/close", nil).WithContext(ctx))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})

	// A write must finish once started: its port never sees the request's
	// cancellation, whether that comes from shutdown or a client disconnect.
	DescribeTable("detaches writes from request cancellation",
		func(target, body string) {
			var seen []error
			note := func(ctx context.Context) { seen = append(seen, ctx.Err()) }
			p := (&fakePorts{}).ports()
			p.CloseSession = func(ctx context.Context, _ string) (Session, error) {
				note(ctx)
				return Session{}, nil
			}
			p.CancelSession = func(ctx context.Context, _ string) (Session, error) {
				note(ctx)
				return Session{}, nil
			}
			p.SetBudgetPool = func(ctx context.Context, _ string, _ int64) (Budget, error) {
				note(ctx)
				return Budget{}, nil
			}
			p.SendSteeringMessage = func(ctx context.Context, _, _, _ string) (Message, error) {
				note(ctx)
				return Message{}, nil
			}
			p.RunSwarm = func(ctx context.Context, _, _ string, _ int64) (SwarmRun, error) {
				note(ctx)
				return SwarmRun{}, nil
			}
			p.CreateSession = func(ctx context.Context, _ CreateRequest) (Session, error) {
				note(ctx)
				return Session{}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			New(p).Handler().ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)).WithContext(ctx))
			Expect(seen).To(Equal([]error{nil}))
		},
		Entry("close", "/sessions/s1/close", ""),
		Entry("cancel", "/sessions/s1/cancel", ""),
		Entry("budget anchor", "/sessions/s1/budget", `{"pool":100}`),
		Entry("steering message", "/sessions/s1/message", `{"from":"s2","body":"go"}`),
		Entry("create", "/sessions", `{"task":"greet"}`),
		Entry("swarm run", "/swarm/run", `{"parent":"s1","path":"m.yaml","budget":100}`),
	)
	DescribeTable("rejects an over-limit body as usage naming the limit",
		func(target string, prefix string) {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, target, prefix+strings.Repeat("a", 2<<20)+`"}`)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"request body exceeds the 1048576 byte limit"}`}))
		},
		Entry("create", "/sessions", `{"task":"`),
		Entry("run", "/sessions/s1/run", `{"budget":1,"x":"`),
		Entry("budget anchor", "/sessions/s1/budget", `{"x":"`),
		Entry("steering message", "/sessions/s1/message", `{"from":"s2","body":"`),
		Entry("swarm validate", "/swarm/validate", `{"path":"`),
		Entry("swarm run", "/swarm/run", `{"parent":"s1","path":"`),
		Entry("receipt validate", "/receipt/validate", `{"path":"`),
	)

	// The limit is inclusive: a body of exactly the limit reaches its port.
	DescribeTable("applies the body limit at exactly one mebibyte",
		func(size, want int) {
			s, _ := sampleServer()
			body := `{"path":"` + strings.Repeat("a", size-len(`{"path":""}`)) + `"}`
			Expect(doBody(s, http.MethodPost, "/swarm/validate", body).Code).To(Equal(want))
		},
		Entry("a body of exactly the limit is accepted", 1<<20, http.StatusOK),
		Entry("one byte more is refused", 1<<20+1, http.StatusBadRequest),
	)

	It("limits a body sent without a content length", func() {
		s, _ := sampleServer()
		rec := httptest.NewRecorder()
		chunked := io.MultiReader(strings.NewReader(`{"path":"` + strings.Repeat("a", 2<<20) + `"}`))
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/swarm/validate", chunked))
		Expect(rec.Body.String()).To(ContainSubstring("exceeds the 1048576 byte limit"))
	})

	It("answers an unauthenticated over-limit body as unauthorized", func() {
		s, _ := sampleServer()
		guarded := *New(s.ports)
		guarded.token = "s3cret"
		rec := doBody(&guarded, http.MethodPost, "/swarm/validate", `{"path":"`+strings.Repeat("a", 2<<20)+`"}`)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("treats a request built without a body as an empty run body", func() {
		s, _ := sampleServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sessions/s1/run", nil)
		req.Body = nil
		s.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})

	It("answers a create request built without a body as an invalid body", func() {
		s, _ := sampleServer()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sessions", nil)
		req.Body = nil
		s.Handler().ServeHTTP(rec, req)
		Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
			`{"code":"usage","message":"invalid create body"}`}))
	})

	DescribeTable("rejects unknown fields and trailing data as usage",
		func(target, body, message string) {
			s, _ := sampleServer()
			rec := doBody(s, http.MethodPost, target, body)
			Expect(reply{rec.Code, rec.Body.String()}).To(Equal(reply{http.StatusBadRequest,
				`{"code":"usage","message":"` + message + `"}`}))
		},
		Entry("create unknown field", "/sessions", `{"task":"t","tsak":"x"}`, "invalid create body"),
		Entry("create trailing data", "/sessions", `{"task":"t"} {}`, "invalid create body"),
		// A secret-looking key before trailing data must not reach the secret
		// scan: only a strict whole-body parse reports the trailing data first.
		Entry("create trailing data after a secret-looking key", "/sessions", `{"task":"t","api_key":"x"} {}`, "invalid create body"),
		Entry("run unknown field", "/sessions/s1/run", `{"budgt":100}`, "invalid run body"),
		Entry("run trailing data", "/sessions/s1/run", `{"budget":1} trailing`, "invalid run body"),
		Entry("budget unknown field", "/sessions/s1/budget", `{"pool":5,"pol":1}`, "invalid budget body"),
		Entry("budget trailing data", "/sessions/s1/budget", `{"pool":5}{"pool":6}`, "invalid budget body"),
		Entry("message unknown field", "/sessions/s1/message", `{"from":"a","body":"b","bdy":"c"}`, "invalid message body"),
		Entry("message trailing data", "/sessions/s1/message", `{"from":"a","body":"b"} x`, "invalid message body"),
		Entry("swarm validate unknown field", "/swarm/validate", `{"path":"m","pth":"x"}`, "invalid swarm validate body"),
		Entry("swarm validate trailing data", "/swarm/validate", `{"path":"m"} []`, "invalid swarm validate body"),
		Entry("swarm run unknown field", "/swarm/run", `{"parent":"p","path":"m","budget":1,"bugdet":2}`, "invalid swarm run body"),
		Entry("swarm run trailing data", "/swarm/run", `{"parent":"p","path":"m","budget":1} 1`, "invalid swarm run body"),
		Entry("receipt unknown field", "/receipt/validate", `{"path":"r","pth":"x"}`, "invalid receipt body"),
		Entry("receipt trailing data", "/receipt/validate", `{"path":"r"} {`, "invalid receipt body"),
	)
})
