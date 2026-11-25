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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
	"github.com/baphled/flowstate/internal/store"
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

var _ = Describe("Serve run crash recovery", func() {
	// bootCrashSession boots serve on a database holding one seeded session.
	bootCrashSession := func(seed func(w *store.EventWriter), tamper bool) (string, string, string, func() int) {
		db := filepath.Join(GinkgoT().TempDir(), "serve.db")
		id := seedCrashSession(db, seed)
		if tamper {
			tamperFirstEvent(db, id)
		}
		base, stop := serveBootAt(db, nil)
		return base, id, db, stop
	}

	// The HTTP table has no replay_mismatch status, so a corrupt log answers
	// internal (500) as it did before crash recovery; it must not be relabelled
	// config_invalid in the message.
	It("does not relabel a corrupt log as config_invalid", func() {
		base, id, _, stop := bootCrashSession(func(*store.EventWriter) {}, true)
		defer func() { mustExit(stop()) }()
		var ec map[string]any
		serveJSON(serveReq(http.MethodPost, base+"/sessions/"+id+"/run", ""), &ec)
		Expect(ec["message"]).NotTo(ContainSubstring("config_invalid"))
	})

	It("answers a delegate call left unanswered with no dangling turn before the first request", func() {
		withFakeZai(fakeZaiServer(`{"choices":[{"delta":{"content":"ok"}}]}`))
		base, id, db, stop := bootCrashSession(unansweredDelegateLog, false)
		defer func() { mustExit(stop()) }()
		if res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", ""); res.StatusCode != http.StatusOK {
			panic("serve run failed: " + res.Status)
		}
		Expect(loggedTypesBeforeFirst(db, id, "assistant.delta")).To(ContainElement("tool.result"))
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

// cancelledReason replays one session's log from the database and returns
// the reason on its session.cancelled event ("" when there is none).
func cancelledReason(db, id string) string {
	st, err := app.OpenStore(db)
	must(err)
	defer func() { must(st.Close()) }()
	events, err := st.Replay(context.Background(), id)
	must(err)
	for _, e := range events {
		if e.Type != "session.cancelled" {
			continue
		}
		var payload struct {
			Reason string `json:"reason"`
		}
		must(json.Unmarshal([]byte(e.PayloadJSON), &payload))
		return payload.Reason
	}
	return ""
}

var _ = Describe("Serve run cancellation reason", func() {
	// bootStalledRun boots serve, starts a run whose provider stalls and
	// returns once the provider has begun streaming, so the run is live.
	bootStalledRun := func() (cancelClient context.CancelFunc, id, db string, stop func() int, finished <-chan struct{}) {
		srv, arrived := e2eStallProvider()
		withFakeZai(srv)
		var base string
		base, id, db, stop = bootSeededProviderSession("z.ai")
		reqCtx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/sessions/"+id+"/run", nil)
		must(err)
		done := make(chan struct{})
		go func() {
			defer close(done)
			if res, derr := http.DefaultClient.Do(req); derr == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
		}()
		<-arrived
		return cancel, id, db, stop, done
	}

	It("records client_disconnected when the client goes away mid-run", func() {
		cancelClient, id, db, stop, finished := bootStalledRun()
		defer func() { mustExit(stop()) }()
		cancelClient()
		<-finished
		Eventually(func() string { return cancelledReason(db, id) }).
			WithTimeout(10 * time.Second).WithPolling(50 * time.Millisecond).Should(Equal("client_disconnected"))
	})

	It("records shutdown when the server stops mid-run", func() {
		cancelClient, id, db, stop, finished := bootStalledRun()
		defer cancelClient()
		mustExit(stop())
		<-finished
		Expect(cancelledReason(db, id)).To(Equal("shutdown"))
	})
})

var _ = Describe("Serve run max_turns", func() {
	// postRun boots serve with an echo allowlist and a model asking for the
	// tool on its first toolTurns turns, then posts body to the run route.
	postRun := func(toolTurns int32, body string) (int, string) {
		cfg := filepath.Join(GinkgoT().TempDir(), "flowstate.json")
		must(os.WriteFile(cfg, []byte(`{"tool_allowlist":["echo"]}`), 0o600))
		var id string
		db := filepath.Join(GinkgoT().TempDir(), "serve.db")
		base, stop := serveBootAt(db, func(st *app.Store) {
			sess, err := app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "greet")
			must(err)
			id = sess.ID
		}, "--config", cfg)
		defer func() { mustExit(stop()) }()
		var requests atomic.Int32
		withFakeZai(toolThenAnswerServer(toolTurns, &requests))
		res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", body)
		var doc map[string]any
		data, err := io.ReadAll(res.Body)
		must(err)
		_ = json.Unmarshal(data, &doc)
		code, _ := doc["code"].(string)
		return res.StatusCode, code
	}

	It("stops a run at the body max_turns with 409", func() {
		status, _ := postRun(5, `{"max_turns":3}`)
		Expect(status).To(Equal(http.StatusConflict))
	})

	It("reports max_turns as the code of that stop", func() {
		_, code := postRun(5, `{"max_turns":3}`)
		Expect(code).To(Equal("max_turns"))
	})

	It("lets a run answering on turn 3 complete under max_turns 3", func() {
		status, _ := postRun(2, `{"max_turns":3}`)
		Expect(status).To(Equal(http.StatusOK))
	})

	It("uses the default of 25 turns when the body omits max_turns", func() {
		status, _ := postRun(24, `{"budget":0}`)
		Expect(status).To(Equal(http.StatusOK))
	})
})

var _ = Describe("Serve run takes the judged run lease", func() {
	// bootCrashedRun boots serve on a database holding one session whose log ends in a crashed turn.
	bootCrashedRun := func() (string, string, string, func() int) {
		db := filepath.Join(GinkgoT().TempDir(), "serve.db")
		id := seedCrashSession(db, crashedLog)
		base, stop := serveBootAt(db, nil)
		return base, id, db, stop
	}

	Context("a second run against a live one", func() {
		var res *http.Response
		var before, after string
		var code string
		BeforeEach(func() {
			base, id, db, stop := bootCrashedRun()
			DeferCleanup(func() { mustExit(stop()) })
			holdLeaseAt(db, id)
			read := func() string {
				data, err := io.ReadAll(serveReq(http.MethodGet, base+"/sessions/"+id+"/events?after=-1", "").Body)
				must(err)
				return string(data)
			}
			before = read()
			res = serveReq(http.MethodPost, base+"/sessions/"+id+"/run", "")
			var ec map[string]string
			serveJSON(res, &ec)
			code = ec["code"]
			after = read()
		})
		It("is refused with the status the held-lease mapping gives today", func() {
			Expect(res.StatusCode).To(Equal(http.StatusInternalServerError))
		})
		It("reports the internal code until the error contract moves", func() { Expect(code).To(Equal("internal")) })
		It("leaves the log byte-identical", func() { Expect(after).To(Equal(before)) })
	})

	Context("a run that ends", func() {
		var seen atomic.Value
		var db, id string
		BeforeEach(func() {
			var base string
			var stop func() int
			base, id, db, stop = bootCrashedRun()
			DeferCleanup(func() { mustExit(stop()) })
			withFakeZai(observingZai(db, id, &seen))
			if res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", ""); res.StatusCode != http.StatusOK {
				panic("serve run failed: " + res.Status)
			}
		})
		It("held a run:<uuid> lease while it ran", func() {
			Expect(seen.Load().(store.LeaseInfo).Owner).To(HavePrefix("run:"))
		})
		It("has released the lease row", func() {
			_, found := leaseAt(db, id)
			Expect(found).To(BeFalse())
		})
		It("has released the lock for the next holder", func() { Expect(canAcquireAt(db, id)).To(BeTrue()) })
	})

	Context("a run that fails after the acquire", func() {
		var db, id string
		BeforeEach(func() {
			var base string
			var stop func() int
			base, id, db, stop = bootCrashedRun()
			DeferCleanup(func() { mustExit(stop()) })
			tamperFirstEvent(db, id)
			if res := serveReq(http.MethodPost, base+"/sessions/"+id+"/run", ""); res.StatusCode == http.StatusOK {
				panic("serve run on a corrupt log succeeded")
			}
		})
		It("has released the lease row", func() {
			_, found := leaseAt(db, id)
			Expect(found).To(BeFalse())
		})
		It("has released the lock for the next holder", func() { Expect(canAcquireAt(db, id)).To(BeTrue()) })
	})

	Context("a run that panics after the acquire", func() {
		var db, id string
		BeforeEach(func() {
			var base string
			var stop func() int
			base, id, db, stop = bootCrashedRun()
			DeferCleanup(func() { mustExit(stop()) })
			old := providerResolver
			providerResolver = func(string) (provider.Provider, error) { panic("provider seam panic") }
			DeferCleanup(func() { providerResolver = old })
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/sessions/"+id+"/run", nil)
			must(err)
			if res, derr := http.DefaultClient.Do(req); derr == nil { // the server drops the connection on a panic
				_ = res.Body.Close()
			}
		})
		It("has released the lease row", func() {
			_, found := leaseAt(db, id)
			Expect(found).To(BeFalse())
		})
		It("has released the lock for the next holder", func() { Expect(canAcquireAt(db, id)).To(BeTrue()) })
	})
})
