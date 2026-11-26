package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/server"
)

// crashedDoctorSession creates a session whose event log ends in a
// dangling turn.start with provider traffic — the crash shape a
// killed `session run` leaves behind — panicking on setup errors.
func crashedDoctorSession() string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "crash probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	id := decodeNew(created.stdout).ID
	st, oerr := app.OpenStore(dbFor())
	if oerr != nil {
		panic(oerr)
	}
	w, werr := st.NewEventWriter(context.Background(), id)
	if werr != nil {
		panic(werr)
	}
	if _, aerr := w.Append(context.Background(), "engine", "provider.request", map[string]any{}); aerr != nil {
		panic(aerr)
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	if serr := st.Close(); serr != nil {
		panic(serr)
	}
	return id
}

// turnEndDoctorSession creates a session whose log holds a provider
// request and one completed turn.end carrying reason (omitted when
// empty), panicking on setup errors.
func turnEndDoctorSession(reason string) string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "truncation probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	id := decodeNew(created.stdout).ID
	st, oerr := app.OpenStore(dbFor())
	if oerr != nil {
		panic(oerr)
	}
	w, werr := st.NewEventWriter(context.Background(), id)
	if werr != nil {
		panic(werr)
	}
	payload := map[string]any{"status": "completed"}
	if reason != "" {
		payload["finish_reason"] = reason
	}
	for _, ev := range []struct {
		typ string
		pl  map[string]any
	}{{"provider.request", map[string]any{}}, {"turn.end", payload}} {
		if _, aerr := w.Append(context.Background(), "engine", ev.typ, ev.pl); aerr != nil {
			panic(aerr)
		}
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	if serr := st.Close(); serr != nil {
		panic(serr)
	}
	return id
}

// doctorRecord runs `session doctor` for id against the per-spec DB
// and decodes the first stdout line as JSON.
func doctorRecord(id string) (result, map[string]any) {
	r := runArgv([]string{"session", "doctor", id, "--db", dbFor()})
	var rec map[string]any
	if uerr := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); uerr != nil {
		panic(uerr)
	}
	return r, rec
}

// noCompactions is the compactions object of a session whose log holds none.
var noCompactions = map[string]any{"count": float64(0), "invalid": float64(0), "summaries_reported": float64(0), "last_tokens_after": nil,
	"summary_input_tokens": nil, "summary_output_tokens": nil}

// compactedDoctorSession creates a session whose log holds two context.compacted events, the
// first carrying the summarising call's usage, panicking on setup errors.
func compactedDoctorSession() string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "compaction probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	id := decodeNew(created.stdout).ID
	st, oerr := app.OpenStore(dbFor())
	if oerr != nil {
		panic(oerr)
	}
	w, werr := st.NewEventWriter(context.Background(), id)
	if werr != nil {
		panic(werr)
	}
	if _, aerr := w.Append(context.Background(), "user", "turn.start", map[string]any{"task": "compaction probe"}); aerr != nil {
		panic(aerr)
	}
	for _, usage := range []map[string]any{{"input_tokens": 100, "output_tokens": 10}, nil} {
		payload := map[string]any{"summary": "SUM", "first_kept_seq": 1, "tokens_before": 900, "tokens_after": 300,
			"reason": "manual", "model": "m"}
		if usage != nil {
			payload["usage"] = usage
		}
		if _, aerr := w.Append(context.Background(), "engine", "context.compacted", payload); aerr != nil {
			panic(aerr)
		}
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	if serr := st.Close(); serr != nil {
		panic(serr)
	}
	return id
}

var _ = Describe("Session doctor CLI", func() {
	It("prints the compactions object with counts, the last tokens_after and the summary usage", func() {
		_, rec := doctorRecord(compactedDoctorSession())
		Expect(rec["compactions"]).To(Equal(map[string]any{"count": float64(2), "invalid": float64(0), "summaries_reported": float64(1),
			"last_tokens_after": float64(300), "summary_input_tokens": float64(100), "summary_output_tokens": float64(10)}))
	})

	It("keeps a compaction's usage out of the usage object", func() {
		_, rec := doctorRecord(compactedDoctorSession())
		Expect(rec["usage"].(map[string]any)["turns_reported"]).To(Equal(float64(0)))
	})

	It("validates a diagnosis with compactions against the session-doctor schema", func() {
		r, _ := doctorRecord(compactedDoctorSession())
		Expect(validateAgainst("session-doctor", firstLine(r.stdout))).To(Succeed())
	})

	It("exits 0 for a crashed session", func() {
		id := crashedDoctorSession()
		r, _ := doctorRecord(id)
		Expect(r.exit).To(Equal(0))
	})

	It("prints the full diagnosis record for a crashed session", func() {
		id := crashedDoctorSession()
		_, rec := doctorRecord(id)
		Expect(rec).To(Equal(map[string]any{
			"session_id": id, "status": "active", "crashed": true,
			"run_activity": true, "pending_messages": float64(0),
			"reserved": float64(0), "budget_pool": nil, "available": nil,
			"usage": map[string]any{"input_tokens": nil, "output_tokens": nil, "cache_read_tokens": nil,
				"cache_write_tokens": nil, "last_context_tokens": nil, "turns_reported": float64(0), "turns_total": float64(0)},
			"compactions":    noCompactions,
			"context_window": map[string]any{"tokens": float64(1000000), "source": "table"},
		}))
	})

	It("validates the crashed diagnosis against the session-doctor schema", func() {
		id := crashedDoctorSession()
		r, _ := doctorRecord(id)
		verr := validateAgainst("session-doctor", firstLine(r.stdout))
		Expect(verr).NotTo(HaveOccurred())
	})

	It("rejects a diagnosis record missing a required field", func() {
		err := validateAgainst("session-doctor",
			`{"session_id":"01a1f000-0000-4000-8000-000000000000","status":"active",`+
				`"run_activity":true,"pending_messages":0,"reserved":0,"budget_pool":null,"available":null}`)
		Expect(err).To(HaveOccurred())
	})

	It("exits 0 for a paused session", func() {
		id := newPausedSession()
		r, _ := doctorRecord(id)
		Expect(r.exit).To(Equal(0))
	})

	It("reports a paused session as un-crashed", func() {
		id := newPausedSession()
		_, rec := doctorRecord(id)
		Expect(rec["crashed"]).To(BeFalse())
	})

	It("reports the paused row status", func() {
		id := newPausedSession()
		_, rec := doctorRecord(id)
		Expect(rec["status"]).To(Equal("paused"))
	})

	It("exits 0 for a fresh session", func() {
		created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "fresh probe")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		r, _ := doctorRecord(decodeNew(created.stdout).ID)
		Expect(r.exit).To(Equal(0))
	})

	It("reports no run activity for a fresh session", func() {
		created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "fresh probe")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		_, rec := doctorRecord(decodeNew(created.stdout).ID)
		Expect(rec["run_activity"]).To(BeFalse())
	})

	It("exits 1 for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "doctor-unknown.db")
		r := runArgv([]string{"session", "doctor", "01a1f000-0000-4000-8000-000000000000", "--db", db})
		Expect(r.exit).To(Equal(1))
	})

	It("reports stderr code session_not_found for an unknown session id", func() {
		db := filepath.Join(GinkgoT().TempDir(), "doctor-unknown.db")
		r := runArgv([]string{"session", "doctor", "01a1f000-0000-4000-8000-000000000000", "--db", db})
		var e struct {
			Code string `json:"code"`
		}
		if uerr := json.Unmarshal([]byte(firstLine(r.stderr)), &e); uerr != nil {
			panic(uerr)
		}
		Expect(e.Code).To(Equal("session_not_found"))
	})

	It("exits 2 when the session id is missing", func() {
		r := runArgv([]string{"session", "doctor", "--db", dbFor()})
		Expect(r.exit).To(Equal(2))
	})

	It("reports truncated true for a length-terminated final turn", func() {
		_, rec := doctorRecord(turnEndDoctorSession("length"))
		Expect(rec["truncated"]).To(BeTrue())
	})

	It("reports the finish reason of a length-terminated final turn", func() {
		_, rec := doctorRecord(turnEndDoctorSession("length"))
		Expect(rec["finish_reason"]).To(Equal("length"))
	})

	It("validates a truncated diagnosis against the session-doctor schema", func() {
		r, _ := doctorRecord(turnEndDoctorSession("length"))
		Expect(validateAgainst("session-doctor", firstLine(r.stdout))).To(Succeed())
	})

	It("omits truncated for a stop-terminated final turn", func() {
		_, rec := doctorRecord(turnEndDoctorSession("stop"))
		Expect(rec).NotTo(HaveKey("truncated"))
	})

	It("reports the stop finish reason", func() {
		_, rec := doctorRecord(turnEndDoctorSession("stop"))
		Expect(rec["finish_reason"]).To(Equal("stop"))
	})

	It("keeps today's record for a turn.end without a finish reason", func() {
		id := turnEndDoctorSession("")
		_, rec := doctorRecord(id)
		Expect(rec).To(Equal(map[string]any{
			"session_id": id, "status": "active", "crashed": false,
			"run_activity": true, "pending_messages": float64(0),
			"reserved": float64(0), "budget_pool": nil, "available": nil,
			"usage": map[string]any{"input_tokens": nil, "output_tokens": nil, "cache_read_tokens": nil,
				"cache_write_tokens": nil, "last_context_tokens": nil, "turns_reported": float64(0), "turns_total": float64(0)},
			"compactions":    noCompactions,
			"context_window": map[string]any{"tokens": float64(1000000), "source": "table"},
		}))
	})
})

// usageLoopServer is a loopback fake z.ai: the first request answers with a tool
// call and the second with the final answer, each finish chunk carrying usage.
func usageLoopServer() *httptest.Server {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":` +
				`{"name":"echo","arguments":"{\"message\":\"hi\"}"}}]}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		}
		if atomic.AddInt32(&calls, 1) > 1 {
			chunks = []string{`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":` +
				`{"prompt_tokens":150,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":50}}}`}
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	DeferCleanup(srv.Close)
	return srv
}

// usageTextServer is a loopback fake z.ai answering one plain text turn whose
// last chunk carries usage.
func usageTextServer() *httptest.Server {
	return fakeZaiServer(`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)
}

// usageAnthropicServer is a loopback fake Messages API answering one streamed
// turn whose usage arrives in message_start and message_delta.
func usageAnthropicServer() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":12,"cache_read_input_tokens":3,` +
				`"cache_creation_input_tokens":2,"output_tokens":1}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
	}))
	DeferCleanup(srv.Close)
	return srv
}

// e2eDoctorUsage runs `session doctor` through the built binary and returns
// the raw first stdout line.
func e2eDoctorUsage(baseURL string, extraEnv []string, db, id string) string {
	return firstLine(e2eRunWith(baseURL, "", extraEnv, "session", "doctor", id, "--db", db).stdout)
}

var _ = Describe("Session doctor usage after a real run", Label("e2e"), func() {
	var db string

	BeforeEach(func() { db = filepath.Join(GinkgoT().TempDir(), "usage.db") })

	It("reports the totals of a run through the registry-built z.ai route", func() {
		srv := usageLoopServer()
		id := e2eNewSession(srv.URL, db)
		if r := e2eRun(srv.URL, "session", "run", "--db", db, "--allow", "echo", id); r.exit != 0 {
			panic("session run failed: " + r.stderr)
		}
		Expect(gjsonUsage(e2eDoctorUsage(srv.URL, nil, db, id))).To(Equal(map[string]any{
			"input_tokens": float64(200), "output_tokens": float64(30), "cache_read_tokens": float64(50),
			"cache_write_tokens": float64(0), "turns_reported": float64(2), "turns_total": float64(2),
			"last_context_tokens": float64(170)}))
	})

	It("reports the usage of a plain text turn through the registry-built z.ai route", func() {
		srv := usageTextServer()
		id := e2eNewSession(srv.URL, db)
		if r := e2eRun(srv.URL, "session", "run", "--db", db, id); r.exit != 0 {
			panic("session run failed: " + r.stderr)
		}
		Expect(gjsonUsage(e2eDoctorUsage(srv.URL, nil, db, id))).To(HaveKeyWithValue("last_context_tokens", float64(13)))
	})

	It("validates that doctor output against the session-doctor schema", func() {
		srv := usageLoopServer()
		id := e2eNewSession(srv.URL, db)
		if r := e2eRun(srv.URL, "session", "run", "--db", db, "--allow", "echo", id); r.exit != 0 {
			panic("session run failed: " + r.stderr)
		}
		Expect(validateAgainst("session-doctor", e2eDoctorUsage(srv.URL, nil, db, id))).To(Succeed())
	})

	It("reports the usage of a text turn on the Anthropic API-key route", func() {
		srv := usageAnthropicServer()
		env := []string{"FLOWSTATE_ANTHROPIC_AUTH=api_key", "ANTHROPIC_API_KEY=sk-ant-synthetic-test-key",
			"ANTHROPIC_BASE_URL=" + srv.URL}
		created := e2eRunWith(srv.URL, "", env, "session", "new", "--db", db, "--provider", "anthropic",
			"--model", "claude-sonnet-5-5", "--task", "greet")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		id := decodeNew(created.stdout).ID
		if r := e2eRunWith(srv.URL, "", env, "session", "run", "--db", db, id); r.exit != 0 {
			panic("session run failed: " + r.stderr)
		}
		Expect(gjsonUsage(e2eDoctorUsage(srv.URL, env, db, id))).To(HaveKeyWithValue("last_context_tokens", float64(24)))
	})
})

// gjsonUsage decodes a doctor line and returns its usage object.
func gjsonUsage(line string) map[string]any {
	var rec struct {
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		panic(err)
	}
	return rec.Usage
}

var _ = Describe("Session doctor usage through the serve port", func() {
	It("carries the recorded usage through the store-backed doctor port", func() {
		created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "usage probe")
		if created.exit != 0 {
			panic("session new failed: " + created.stderr)
		}
		id := decodeNew(created.stdout).ID
		st, oerr := app.OpenStore(dbFor())
		must(oerr)
		DeferCleanup(func() { must(st.Close()) })
		w, werr := st.NewEventWriter(context.Background(), id)
		must(werr)
		_, aerr := w.Append(context.Background(), "engine", "assistant.message", map[string]any{
			"content": "x", "usage": map[string]any{"input_tokens": 4, "output_tokens": 1,
				"cache_read_tokens": 2, "cache_write_tokens": 3}})
		must(aerr)
		must(w.Close())
		d, err := servePorts(st, &flags{}).DiagnoseSession(context.Background(), id)
		must(err)
		Expect(d.Usage).To(Equal(server.DiagnosisUsage{InputTokens: 4, OutputTokens: 1, CacheReadTokens: 2,
			CacheWriteTokens: 3, TurnsReported: 1, TurnsTotal: 1, LastContextTokens: 10}))
	})
})

// windowDoctor creates a session on provider/model and returns its doctor record, run with
// the config body written to a file (no --config when body is empty).
func windowDoctor(providerName, model, body string) (result, map[string]any) {
	created := runCLINew("--provider", providerName, "--model", model, "--task", "window probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	argv := []string{"session", "doctor", decodeNew(created.stdout).ID, "--db", dbFor()}
	if body != "" {
		argv = append(argv, "--config", writeConfigFixture(body))
	}
	r := runArgv(argv)
	var rec map[string]any
	if uerr := json.Unmarshal([]byte(firstLine(r.stdout)), &rec); uerr != nil {
		panic(uerr)
	}
	return r, rec
}

var _ = Describe("Session doctor hooks", func() {
	It("omits hooks for a log without hook events", func() {
		_, rec := doctorRecord(crashedDoctorSession())
		Expect(rec).NotTo(HaveKey("hooks"))
	})

	It("lists an invocation no result followed, with its point, id and seq", func() {
		id := hookDoctorSession(hookLog(app.TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard", "kind": "command"}))
		Expect(hooksOf(id)["unmatched"]).To(Equal([]any{
			map[string]any{"point": "tool.before", "id": "guard", "seq": float64(1)}}))
	})

	It("summarises the last hooks.loaded: scope, digest, ids by source, skipped and inert", func() {
		loaded := map[string]any{"scope": "both", "set_sha256": "abc123", "ids": []string{"guard", "lint"},
			"hooks":   []map[string]any{{"id": "guard", "source": "user"}, {"id": "lint", "source": "project"}},
			"skipped": []map[string]any{{"id": "prof", "reason": "scope"}}, "inert": []string{"late"}}
		Expect(hooksOf(hookDoctorSession(hookLog(app.TypeHooksLoaded, loaded)))["loaded"]).To(Equal(map[string]any{
			"scope": "both", "set_sha256": "abc123", "ids": map[string]any{"user": []any{"guard"}, "project": []any{"lint"}},
			"skipped": []any{map[string]any{"id": "prof", "reason": "scope"}}, "inert": []any{"late"}}))
	})

	It("renders loaded as null for a log that records no hooks.loaded, as for a child", func() {
		id := hookDoctorSession(hookLog(app.TypeHookResult, hookResult("tool.before", "guard", "allow")))
		Expect(hooksOf(id)["loaded"]).To(BeNil())
	})

	It("counts the failed hook results", func() {
		id := hookDoctorSession(hookLog(app.TypeHookResult, hookResult("tool.before", "guard", "failed")),
			hookLog(app.TypeHookResult, hookResult("tool.after", "guard", "allow")),
			hookLog(app.TypeHookResult, hookResult("tool.after", "guard", "failed")))
		Expect(hooksOf(id)["failed"]).To(Equal(float64(2)))
	})

	It("lists each denying hook with its count and last reason", func() {
		denied := hookResult("tool.before", "guard", "deny")
		denied["reason"] = "nope"
		id := hookDoctorSession(hookLog(app.TypeHookResult, denied), hookLog(app.TypeHookResult, denied))
		Expect(hooksOf(id)["denials"]).To(Equal([]any{
			map[string]any{"point": "tool.before", "id": "guard", "count": float64(2), "reason": "nope"}}))
	})

	It("omits an empty denial reason", func() {
		id := hookDoctorSession(hookLog(app.TypeHookResult, hookResult("tool.before", "guard", "deny")))
		Expect(hooksOf(id)["denials"]).To(Equal([]any{map[string]any{"point": "tool.before", "id": "guard", "count": float64(1)}}))
	})

	It("reports a refusal outcome with the hook id and reason", func() {
		refused := hookResult("user.prompt", "guard", "deny")
		refused["reason"] = "blocked"
		Expect(hooksOf(hookDoctorSession(hookLog(app.TypeHookResult, refused)))["outcome"]).To(Equal(
			map[string]any{"code": "hook_refused", "id": "guard", "reason": "blocked"}))
	})

	It("reports a fail-closed user.prompt failure as hook_refused with the failure code", func() {
		failed := hookResult("user.prompt", "guard", "failed")
		failed["failure"], failed["on_failure"] = "exit_status", app.HookOnFailureDeny
		Expect(hooksOf(hookDoctorSession(hookLog(app.TypeHookResult, failed)))["outcome"]).To(Equal(
			map[string]any{"code": "hook_refused", "id": "guard", "reason": "exit_status"}))
	})

	It("omits the outcome when no hook stopped or refused the run", func() {
		Expect(hooksOf(hookDoctorSession(hookLog(app.TypeHookResult, hookResult("tool.before", "guard", "allow"))))).NotTo(
			HaveKey("outcome"))
	})

	It("prints a hook reason stripped of escape sequences, not raw", func() {
		denied := hookResult("user.prompt", "guard", "deny")
		denied["reason"] = "\x1b[31mred\x1b[0m \u202ever" + "sion"
		Expect(hooksOf(hookDoctorSession(hookLog(app.TypeHookResult, denied)))["outcome"]).To(Equal(
			map[string]any{"code": "hook_refused", "id": "guard", "reason": "[31mred[0m version"}))
	})

	It("validates a diagnosis with hooks against the session-doctor schema", func() {
		r, _ := doctorRecord(hookDoctorSession(hookLog(app.TypeHookInvoked, map[string]any{"point": "tool.before", "id": "guard"}),
			hookLog(app.TypeHookResult, hookResult("turn.after", "guard", "failed"))))
		Expect(validateAgainst("session-doctor", firstLine(r.stdout))).To(Succeed())
	})

	DescribeTable("rejects a hooks object the session-doctor schema does not allow",
		func(hooks string) {
			line := `{"session_id":"01a1f000-0000-4000-8000-000000000000","status":"paused","crashed":false,` +
				`"run_activity":true,"pending_messages":0,"reserved":0,"budget_pool":null,"available":null,` +
				`"usage":{"input_tokens":null,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":null,` +
				`"turns_reported":0,"turns_total":0,"last_context_tokens":null},` +
				`"compactions":{"count":0,"invalid":0,"last_tokens_after":null,"summaries_reported":0,` +
				`"summary_input_tokens":null,"summary_output_tokens":null},` +
				`"context_window":{"tokens":1000,"source":"table"},"hooks":` + hooks + `}`
			Expect(validateAgainst("session-doctor", line)).To(HaveOccurred())
		},
		Entry("a failed count that is not a number", `{"loaded":null,"unmatched":[],"denials":[],"failed":"1"}`),
		Entry("an unknown key", `{"loaded":null,"unmatched":[],"denials":[],"failed":0,"stops":0}`),
		Entry("a missing failed count", `{"loaded":null,"unmatched":[],"denials":[]}`),
		Entry("a denial without its count", `{"loaded":null,"unmatched":[],"failed":0,"denials":[{"point":"tool.before","id":"g"}]}`),
	)
})

// hookLog is one engine event of a hookDoctorSession log.
func hookLog(typ string, payload map[string]any) hookDoctorEvent {
	return hookDoctorEvent{typ: typ, payload: payload}
}

// hookResult is a hook.result payload for hook id at point with the given decision.
func hookResult(point, id, decision string) map[string]any {
	return map[string]any{"point": point, "id": id, "decision": decision, "duration_ms": 1, "exit_code": 0,
		"stdout": "", "stderr": ""}
}

// hookDoctorEvent is one event of a hookDoctorSession log.
type hookDoctorEvent struct {
	typ     string
	payload map[string]any
}

// hookDoctorSession creates a session whose log holds the given engine events, so a doctor
// spec starts from a known hook log; it panics on setup errors.
func hookDoctorSession(events ...hookDoctorEvent) string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-5.3", "--task", "hook probe")
	if created.exit != 0 {
		panic("session new failed: " + created.stderr)
	}
	id := decodeNew(created.stdout).ID
	st, oerr := app.OpenStore(dbFor())
	if oerr != nil {
		panic(oerr)
	}
	w, werr := st.NewEventWriter(context.Background(), id)
	if werr != nil {
		panic(werr)
	}
	for _, ev := range events {
		if _, aerr := w.Append(context.Background(), "engine", ev.typ, ev.payload); aerr != nil {
			panic(aerr)
		}
	}
	if cerr := w.Close(); cerr != nil {
		panic(cerr)
	}
	if serr := st.Close(); serr != nil {
		panic(serr)
	}
	return id
}

// hooksOf runs the doctor for id and returns its hooks object.
func hooksOf(id string) map[string]any {
	_, rec := doctorRecord(id)
	hooks, _ := rec["hooks"].(map[string]any)
	return hooks
}

var _ = Describe("Session doctor context window", func() {
	It("warns for a model with no window, naming the fix", func() {
		_, rec := windowDoctor("ollama", "llama3", "")
		Expect(rec["warnings"]).To(ConsistOf(
			"no context window is known for this model; set compaction.context_window in the config " +
				"(automatic compaction will need it)"))
	})

	It("reports a null window and the unknown source for it", func() {
		_, rec := windowDoctor("ollama", "llama3", "")
		Expect(rec["context_window"]).To(Equal(map[string]any{"tokens": nil, "source": "unknown"}))
	})

	It("validates the unknown-window diagnosis against the schema", func() {
		r, _ := windowDoctor("ollama", "llama3", "")
		Expect(validateAgainst("session-doctor", firstLine(r.stdout))).To(Succeed())
	})

	It("does not warn for a model the table knows", func() {
		_, rec := windowDoctor("z.ai", "glm-5.3", "")
		Expect(rec).NotTo(HaveKey("warnings"))
	})

	It("does not warn for a near-miss id that the config covers", func() {
		_, rec := windowDoctor("anthropic", "claude-sonnet-5-5-extra", `{"compaction":{"context_window":50000}}`)
		Expect(rec).NotTo(HaveKey("warnings"))
	})

	It("reports the config window and source", func() {
		_, rec := windowDoctor("ollama", "llama3", `{"compaction":{"context_window":50000}}`)
		Expect(rec["context_window"]).To(Equal(map[string]any{"tokens": float64(50000), "source": "config"}))
	})

	It("lets the config beat the table", func() {
		_, rec := windowDoctor("z.ai", "glm-5.3", `{"compaction":{"context_window":50000}}`)
		Expect(rec["context_window"]).To(Equal(map[string]any{"tokens": float64(50000), "source": "config"}))
	})

	It("validates the table-window diagnosis against the schema", func() {
		r, _ := windowDoctor("anthropic", "claude-sonnet-5-5", "")
		Expect(validateAgainst("session-doctor", firstLine(r.stdout))).To(Succeed())
	})
})
