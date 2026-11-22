package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// runServer seeds the run/create fakes: a two-event turn result and one created row.
func runServer() (*Server, *fakePorts) {
	stamp := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := &fakePorts{
		runEvents: []Event{
			{SessionID: streamID, Seq: 1, CreatedAt: stamp, Actor: "user", Type: "turn.start",
				Payload: json.RawMessage(`{"task":"demo"}`), PayloadSHA: "9c2a"},
			{SessionID: streamID, Seq: 4, CreatedAt: stamp, Actor: "engine", Type: "turn.end",
				Payload: json.RawMessage(`{"status":"completed"}`), PayloadSHA: "8d1b"},
		},
		created: Session{ID: streamID, CreatedAt: stamp, UpdatedAt: stamp, Status: "active",
			Provider: "z.ai", ModelID: "glm-5.3", Harness: "pi", ConfigSHA: "3f7a"},
	}
	return New(f.ports()), f
}

var _ = Describe("Session cancel with an unsettled reservation", func() {
	const cancelPath = "/sessions/" + streamID + "/cancel"
	var rec *httptest.ResponseRecorder

	BeforeEach(func() {
		f := &fakePorts{}
		p := f.ports()
		p.CancelSession = func(_ context.Context, id string) (Session, error) {
			return Session{ID: id, Status: "cancelled"},
				fmt.Errorf("%w: reservation_settle_failed: session %s is cancelled", ErrReservationUnsettled, id)
		}
		rec = doBody(New(p), http.MethodPost, cancelPath, "")
	})

	It("keeps the failing 500 status", func() {
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
	})

	It("answers the stable reservation_unsettled code", func() {
		Expect(rec.Body.String()).To(ContainSubstring(`"code":"reservation_unsettled"`))
	})

	It("states in a structured field that the session is cancelled", func() {
		Expect(rec.Body.String()).To(ContainSubstring(`"session":{"id":"` + streamID + `","status":"cancelled"}`))
	})

	It("does not claim the session is cancelled when the port returned no cancelled row", func() {
		p := (&fakePorts{}).ports()
		p.CancelSession = func(context.Context, string) (Session, error) {
			return Session{}, fmt.Errorf("%w: no row", ErrReservationUnsettled)
		}
		Expect(doBody(New(p), http.MethodPost, cancelPath, "").Body.String()).NotTo(ContainSubstring(`"session"`))
	})

	It("omits the session field from an unrelated cancel failure", func() {
		s, f := sampleServer()
		f.writeErr = errFakeUnmapped
		Expect(doBody(s, http.MethodPost, cancelPath, "").Body.String()).NotTo(ContainSubstring(`"session"`))
	})
})

var _ = Describe("Session run", func() {
	It("returns the projected run events as the session-events array", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", "")
		if rec.Code != http.StatusOK {
			panic("unexpected status " + rec.Body.String())
		}
		Expect(schemaError("session-events", rec.Body.String())).To(Succeed())
	})

	It("maps a provider failure onto 502 provider_error", func() {
		s, f := runServer()
		f.runErr = errFakeProvider
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", "")
		if rec.Code != http.StatusBadGateway {
			panic("provider failure returned " + rec.Body.String())
		}
		Expect(rec.Body.String()).To(ContainSubstring("provider_error"))
	})

	It("maps an unknown session onto 404 session_not_found", func() {
		s, f := runServer()
		f.runErr = errFakeMissing
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", "")
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("maps a budget stop onto 409 budget_exceeded", func() {
		s, f := runServer()
		f.runErr = errFakeBudget
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", `{"budget":5}`)
		if rec.Code != http.StatusConflict {
			panic("over-budget run returned " + rec.Body.String())
		}
		Expect(rec.Body.String()).To(ContainSubstring("budget_exceeded"))
	})

	It("passes the body budget and max_tokens to the run port", func() {
		s, f := runServer()
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", `{"budget":100,"max_tokens":50}`)
		if rec.Code != http.StatusOK {
			panic("run returned " + rec.Body.String())
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		Expect(f.runOpts).To(Equal(RunOptions{Budget: 100, MaxTokens: 50}))
	})

	It("treats an empty body as no run options", func() {
		s, f := runServer()
		f.runOpts = RunOptions{Budget: -1, MaxTokens: -1}
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", "")
		if rec.Code != http.StatusOK {
			panic("run returned " + rec.Body.String())
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		Expect(f.runOpts).To(Equal(RunOptions{}))
	})

	It("rejects a negative budget with usage", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", `{"budget":-3}`)
		if rec.Code != http.StatusBadRequest {
			panic("negative budget returned " + rec.Body.String())
		}
		Expect(rec.Body.String()).To(ContainSubstring(`"usage"`))
	})

	It("rejects a malformed body with usage", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions/"+streamID+"/run", "not json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("Session creation", func() {
	It("creates a session and answers the session-new shape", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions", `{"provider":"z.ai","model":"glm-5.3","task":"greet"}`)
		if rec.Code != http.StatusOK {
			panic("unexpected status " + rec.Body.String())
		}
		Expect(schemaError("session-new", rec.Body.String())).To(Succeed())
	})

	It("passes the body fields to the create port", func() {
		s, f := runServer()
		rec := doBody(s, http.MethodPost, "/sessions", `{"provider":"z.ai","model":"glm-5.3","task":"greet"}`)
		if rec.Code != http.StatusOK {
			panic("create returned " + rec.Body.String())
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		Expect(f.createdReq).To(Equal(CreateRequest{Provider: "z.ai", Model: "glm-5.3", Task: "greet"}))
	})

	It("rejects a missing task with usage", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions", `{"provider":"z.ai","model":"glm-5.3"}`)
		if rec.Code != http.StatusBadRequest {
			panic("missing task returned " + rec.Body.String())
		}
		Expect(rec.Body.String()).To(ContainSubstring(`"usage"`))
	})

	It("rejects a body harness, which the composition always pins", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions", `{"task":"greet","harness":"pi"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("rejects a secret-looking body key with usage", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions",
			`{"provider":"z.ai","model":"glm-5.3","task":"greet","api_key":"hunter2"}`)
		if rec.Code != http.StatusBadRequest {
			panic("secret key returned " + rec.Body.String())
		}
		Expect(rec.Body.String()).To(ContainSubstring("secret"))
	})

	It("rejects a malformed body with usage", func() {
		s, _ := runServer()
		rec := doBody(s, http.MethodPost, "/sessions", "not json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})
})
