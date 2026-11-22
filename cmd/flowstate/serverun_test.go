package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

// serveBoot boots `flowstate serve` on a fresh temp database seeded by
// seed (may be nil) and returns the base URL and the stop func.
func serveBoot(seed func(st *app.Store), extra ...string) (string, func() int) {
	return serveBootAt(filepath.Join(GinkgoT().TempDir(), "serve.db"), seed, extra...)
}

// serveBootAt is serveBoot on a caller-chosen database path.
func serveBootAt(db string, seed func(st *app.Store), extra ...string) (string, func() int) {
	st, err := app.OpenStore(db)
	must(err)
	if seed != nil {
		seed(st)
	}
	must(st.Close())
	argv := append([]string{"serve", "--addr", "127.0.0.1:0", "--db", db}, extra...)
	outR, outW, err := os.Pipe()
	must(err)
	exit := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { exit <- dispatchContexted(ctx, argv, outW, outW) }()
	line, err := bufio.NewReader(outR).ReadString('\n')
	must(err)
	listen := map[string]string{}
	must(json.Unmarshal([]byte(line), &listen))
	return "http://" + listen["listen"], func() int {
		cancel()
		_ = outW.Close()
		return <-exit
	}
}

// mustExit guards a booted serve's exit code; a panic is a setup failure.
func mustExit(code int) {
	if code != 0 {
		panic(fmt.Sprintf("serve exited %d", code))
	}
}

// serveReq issues one request against a booted serve; "" posts no body.
func serveReq(method, url, body string) *http.Response {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	must(err)
	res, err := http.DefaultClient.Do(req)
	must(err)
	DeferCleanup(func() { _ = res.Body.Close() })
	return res
}

// serveJSON decodes one response body into v.
func serveJSON(res *http.Response, v any) {
	data, err := io.ReadAll(res.Body)
	must(err)
	must(json.Unmarshal(data, v))
}

// bootSeededSession boots serve with one active z.ai session.
func bootSeededSession() (string, string, func() int) {
	base, id, _, stop := bootSeededProviderSession("z.ai")
	return base, id, stop
}

// bootSeededProviderSession boots serve with one active session on the
// named provider and also returns the database path.
func bootSeededProviderSession(providerName string) (string, string, string, func() int) {
	var id string
	db := filepath.Join(GinkgoT().TempDir(), "serve.db")
	base, stop := serveBootAt(db, func(st *app.Store) {
		sess, err := app.CreateSession(context.Background(), st, providerName, "glm-5.3",
			app.DefaultHarness, "3f7a", "greet")
		must(err)
		id = sess.ID
	})
	return base, id, db, stop
}

var _ = Describe("Serve events and stream wiring", func() {
	It("answers the snapshot past the cursor from the store", func() {
		base, id, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		res := serveReq(http.MethodGet, base+"/sessions/"+id+"/events?after=-1", "")
		var events []map[string]any
		serveJSON(res, &events)
		Expect([]string{res.Status, fmt.Sprint(len(events)), events[0]["type"].(string)}).To(Equal(
			[]string{"200 OK", "1", "turn.start"}))
	})

	It("streams the stored log in order and ends on the terminal status", func() {
		base, id, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		withFakeZai(fakeZaiServer(`{"choices":[{"delta":{"content":"hi"}}]}`))
		run := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
		if run.StatusCode != http.StatusOK {
			panic("run failed: " + run.Status)
		}

		res := serveReq(http.MethodGet, base+"/sessions/"+id+"/events/stream?after=-1", "")
		data, err := io.ReadAll(res.Body)
		must(err)
		frames := nonEmpty(strings.Split(string(data), "\n"))
		if !strings.Contains(frames[0], "turn.start") {
			panic("first frame is not turn.start: " + frames[0])
		}
		Expect([]string{res.Header.Get("Content-Type"), fmt.Sprint(len(frames)), frames[len(frames)-1]}).To(Equal(
			[]string{"text/event-stream", "6",
				`data: {"session_id":"` + id + `","status":"completed"}`}))
	})

	It("shuts down cleanly while a stream client is still attached", func() {
		base, id, stop := bootSeededSession()
		res := serveReq(http.MethodGet, base+"/sessions/"+id+"/events/stream?after=-1", "")
		if res.StatusCode != http.StatusOK {
			panic("stream did not open: " + res.Status)
		}
		Expect(stop()).To(Equal(0))
	})
})

var _ = Describe("Serve run wiring", func() {
	It("runs one turn synchronously and answers the projected events", func() {
		base, id, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		withFakeZai(fakeZaiServer(`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"y"}}]}`))

		res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
		var events []map[string]any
		serveJSON(res, &events)
		inspect := serveReq(http.MethodGet, base+"/sessions/"+id, "")
		var row map[string]any
		serveJSON(inspect, &row)
		Expect([]string{res.Status, events[len(events)-1]["type"].(string), row["status"].(string)}).To(Equal(
			[]string{"200 OK", "turn.end", "completed"}))
	})

	It("pauses the session and answers 409 budget_exceeded on a budget stop", func() {
		base, id, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		withFakeZai(fakeZaiServer(`{"choices":[{"delta":{"content":"hello world"}}]}`,
			`{"choices":[{"delta":{"content":" again"}}]}`))

		res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", `{"budget":5}`)
		var ec map[string]any
		serveJSON(res, &ec)
		inspect := serveReq(http.MethodGet, base+"/sessions/"+id, "")
		var row map[string]any
		serveJSON(inspect, &row)
		Expect([]string{res.Status, ec["code"].(string), row["status"].(string)}).To(Equal(
			[]string{"409 Conflict", "budget_exceeded", "paused"}))
	})

	It("answers 502 provider_error on a provider failure", func() {
		base, id, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"502","message":"down"}}`))
		}))
		DeferCleanup(bad.Close)
		withFakeZai(bad)

		res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
		var ec map[string]any
		serveJSON(res, &ec)
		Expect([]string{res.Status, ec["code"].(string)}).To(Equal([]string{"502 Bad Gateway", "provider_error"}))
	})

	It("answers 404 session_not_found for an unknown session", func() {
		base, _, stop := bootSeededSession()
		defer func() { mustExit(stop()) }()
		res := serveReq(http.MethodPost, base+"/sessions/01a1f000-0000-4000-8000-00000000dead/run", "")
		Expect(res.StatusCode).To(Equal(http.StatusNotFound))
	})
})

var _ = Describe("Serve creation wiring", func() {
	It("creates a session answering the session-new shape", func() {
		base, stop := serveBoot(nil)
		defer func() { mustExit(stop()) }()
		res := serveReq(http.MethodPost, base+"/sessions",
			`{"provider":"z.ai","model":"glm-5.3","task":"greet"}`)
		var rec map[string]any
		serveJSON(res, &rec)
		inspect := serveReq(http.MethodGet, base+"/sessions/"+rec["id"].(string), "")
		var row map[string]any
		serveJSON(inspect, &row)
		Expect([]string{res.Status, rec["status"].(string), row["provider"].(string), row["harness"].(string)}).To(Equal(
			[]string{"200 OK", "active", "z.ai", app.DefaultHarness}))
	})

	It("resolves config defaults and keeps body fields overriding them", func() {
		cfgPath := filepath.Join(GinkgoT().TempDir(), "flowstate.json")
		must(os.WriteFile(cfgPath, []byte(
			`{"default_provider":"z.ai","default_model":"glm-5.3"}`), 0o600))
		base, stop := serveBoot(nil, "--config", cfgPath)
		defer func() { mustExit(stop()) }()

		fromConfig := serveReq(http.MethodPost, base+"/sessions", `{"task":"from config"}`)
		var cfgRec map[string]any
		serveJSON(fromConfig, &cfgRec)
		override := serveReq(http.MethodPost, base+"/sessions",
			`{"provider":"anthropic","model":"claude","task":"override"}`)
		var overRec map[string]any
		serveJSON(override, &overRec)
		if fromConfig.StatusCode != http.StatusOK || override.StatusCode != http.StatusOK {
			panic("create failed: " + fromConfig.Status + " / " + override.Status)
		}
		var cfgRow, overRow map[string]any
		serveJSON(serveReq(http.MethodGet, base+"/sessions/"+cfgRec["id"].(string), ""), &cfgRow)
		serveJSON(serveReq(http.MethodGet, base+"/sessions/"+overRec["id"].(string), ""), &overRow)
		Expect([]string{cfgRow["provider"].(string), cfgRow["model_id"].(string),
			overRow["provider"].(string)}).To(Equal([]string{"z.ai", "glm-5.3", "anthropic"}))
	})

	It("answers 400 usage when neither body nor config supplies a provider", func() {
		base, stop := serveBoot(nil)
		defer func() { mustExit(stop()) }()
		res := serveReq(http.MethodPost, base+"/sessions", `{"task":"no provider"}`)
		var ec map[string]any
		serveJSON(res, &ec)
		Expect([]string{res.Status, ec["code"].(string)}).To(Equal([]string{"400 Bad Request", "usage"}))
	})
})

var _ = Describe("Serve run failure classification", func() {
	// post boots serve on a session of the named provider, lets setup
	// adjust the environment (db is the served database), and returns
	// the HTTP status, contract code and message of POST /run.
	post := func(providerName string, setup func(id, db string)) (int, string, string) {
		base, id, db, stop := bootSeededProviderSession(providerName)
		defer func() { mustExit(stop()) }()
		setup(id, db)
		res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
		var ec map[string]string
		serveJSON(res, &ec)
		return res.StatusCode, ec["code"], ec["message"]
	}

	Context("a storage failure while recording the turn", func() {
		var status int
		var code, message string
		BeforeEach(func() {
			status, code, message = post("z.ai", func(_, db string) { withProvider(failDeltaAppends(db)) })
		})
		It("answers 500", func() { Expect(status).To(Equal(http.StatusInternalServerError)) })
		It("reports the internal code", func() { Expect(code).To(Equal("internal")) })
		It("does not blame the provider", func() { Expect(message).NotTo(ContainSubstring("provider_error")) })
		It("reports the storage cause", func() { Expect(message).To(ContainSubstring("disk full")) })
	})

	Context("a provider HTTP failure", func() {
		var status int
		var code, message string
		BeforeEach(func() {
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"429","message":"slow down"}}`))
			}))
			DeferCleanup(bad.Close)
			status, code, message = post("z.ai", func(string, string) { withFakeZai(bad) })
		})
		It("answers 502", func() { Expect(status).To(Equal(http.StatusBadGateway)) })
		It("reports provider_error", func() { Expect(code).To(Equal("provider_error")) })
		It("names provider_error once in the message", func() {
			Expect(strings.Count(message, "provider_error")).To(Equal(1))
		})
	})

	Context("a provider tool-protocol failure", func() {
		var status int
		var code string
		BeforeEach(func() {
			status, code, _ = post("z.ai", func(string, string) {
				withProvider(&scriptedStreamer{events: []provider.StreamEvent{
					{Err: &zai.ToolProtocolError{Reason: "duplicate tool ID"}}}})
			})
		})
		It("answers 502", func() { Expect(status).To(Equal(http.StatusBadGateway)) })
		It("reports provider_error", func() { Expect(code).To(Equal("provider_error")) })
	})

	Context("a provider without streaming support", func() {
		var status int
		var code string
		BeforeEach(func() {
			status, code, _ = post("z.ai", func(string, string) { withProvider(plainProvider{}) })
		})
		It("answers 502", func() { Expect(status).To(Equal(http.StatusBadGateway)) })
		It("reports provider_error", func() { Expect(code).To(Equal("provider_error")) })
	})

	Context("a missing provider key", func() {
		var status int
		var code, message string
		BeforeEach(func() {
			status, code, message = post("z.ai", func(string, string) { isolatedProviderConfig() })
		})
		It("answers 502", func() { Expect(status).To(Equal(http.StatusBadGateway)) })
		It("reports provider_error", func() { Expect(code).To(Equal("provider_error")) })
		It("names provider_error once in the message", func() {
			Expect(strings.Count(message, "provider_error")).To(Equal(1))
		})
	})

	Context("an unknown FLOWSTATE_ANTHROPIC_AUTH on the anthropic route", func() {
		var status int
		var code string
		BeforeEach(func() {
			status, code, _ = post("anthropic", func(string, string) {
				isolatedProviderConfig("FLOWSTATE_ANTHROPIC_AUTH=magic")
			})
		})
		It("answers 502", func() { Expect(status).To(Equal(http.StatusBadGateway)) })
		It("reports provider_error", func() { Expect(code).To(Equal("provider_error")) })
	})
})
