package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// fakeZaiServer serves an SSE stream of the given chunks.
func fakeZaiServer(chunks ...string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			fl.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	}))
	DeferCleanup(srv.Close)
	return srv
}

// withFakeZai swaps the CLI provider resolver for an httptest-backed
// zai adapter and restores it afterwards.
func withFakeZai(srv *httptest.Server) {
	old := providerResolver
	providerResolver = func(name string) (provider.Provider, error) {
		return zai.New("test-key", zai.WithBaseURL(srv.URL)), nil
	}
	DeferCleanup(func() { providerResolver = old })
}

// runCLIRun runs `session run` with the given id args against the
// per-spec temp DB.
func runCLIRun(ids ...string) result {
	return runArgv(append([]string{"session", "run", "--db", dbFor()}, ids...))
}

// nonEmpty drops empty split fragments.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

var _ = Describe("Session run", func() {
	It("streams events and completes for a new session", func() {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"y"}}]}`,
		)
		withFakeZai(srv)

		created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
		Expect(created.exit).To(Equal(0))
		rec := decodeNew(created.stdout)

		run := runCLIRun(rec.ID)
		Expect(run.exit).To(Equal(0))
		got := nonEmpty(strings.Split(run.stdout, "\n"))
		Expect(got).To(HaveLen(6))
		var last struct {
			Type    string          `json:"type"`
			Actor   string          `json:"actor"`
			Payload json.RawMessage `json:"payload"`
		}
		Expect(json.Unmarshal([]byte(got[len(got)-1]), &last)).To(Succeed())
		Expect(last.Type).To(Equal("turn.end"))
		Expect(last.Actor).To(Equal("engine"))
		Expect(string(last.Payload)).To(ContainSubstring(`"status":"completed"`))

		evts := runCLIEvents(rec.ID)
		Expect(evts.exit).To(Equal(0))
		wantOrder := "turn.start,user.message,assistant.delta,assistant.delta,assistant.message,turn.end"
		gotTypes := []string{}
		for _, l := range nonEmpty(strings.Split(evts.stdout, "\n")) {
			var e struct {
				Type string `json:"type"`
			}
			Expect(json.Unmarshal([]byte(l), &e)).To(Succeed())
			gotTypes = append(gotTypes, e.Type)
		}
		Expect(strings.Join(gotTypes, ",")).To(Equal(wantOrder))
	})

	It("exits 3 and emits a provider.error event on provider failure", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
		}))
		DeferCleanup(srv.Close)
		withFakeZai(srv)

		created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
		Expect(created.exit).To(Equal(0))
		var rec struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec)).To(Succeed())

		run := runCLIRun(rec.ID)
		Expect(run.exit).To(Equal(3))
		got := nonEmpty(strings.Split(run.stdout, "\n"))
		lastType := ""
		for _, l := range got {
			var e struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(l), &e); err != nil || e.Type == "" {
				continue
			}
			lastType = e.Type
		}
		Expect(lastType).To(Equal("provider.error"))
	})

	It("exits 2 with usage when the session id is missing", func() {
		Expect(runCLIRun().exit).To(Equal(2))
	})

	It("exits 1 for an unknown session", func() {
		Expect(runCLIRun("nope").exit).To(Equal(1))
	})

	It("exits 6 when running an already-terminal session", func() {
		srv := fakeZaiServer(`{"choices":[{"delta":{"content":"x"}}]}`)
		withFakeZai(srv)
		created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
		var rec struct {
			ID string `json:"id"`
		}
		Expect(json.Unmarshal([]byte(strings.SplitN(created.stdout, "\n", 2)[0]), &rec)).To(Succeed())
		first := runCLIRun(rec.ID)
		Expect(first.exit).To(Equal(0))
		Expect(runCLIRun(rec.ID).exit).To(Equal(6))
	})
})
