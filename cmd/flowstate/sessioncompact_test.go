package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
)

// recordingStreamer is a scriptedStreamer that keeps the requests it was sent.
type recordingStreamer struct {
	scriptedStreamer
	reqs []*provider.ChatRequest
}

func (r *recordingStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	r.reqs = append(r.reqs, req)
	return r.scriptedStreamer.Stream(ctx, req)
}

// compactSummary is a summary that finishes normally.
func compactSummary(text string) []provider.StreamEvent {
	return []provider.StreamEvent{{Kind: provider.StreamEventText, Content: text},
		{Kind: provider.StreamEventFinish, FinishReason: "stop"}}
}

// bigTurns appends n tool turns whose results are 50000 bytes, so the default keep-recent
// budget (20000 tokens) keeps the last two.
func bigTurns(w *store.EventWriter, n int) {
	ctx := context.Background()
	for i := 0; i < n; i++ {
		id := "c" + string(rune('a'+i))
		_, err := w.Append(ctx, "engine", "assistant.message", map[string]any{"content": "turn " + id})
		must(err)
		_, err = w.Append(ctx, "tool:echo", "tool.call", map[string]any{"tool": "echo", "tool_call_id": id, "args": map[string]any{}})
		must(err)
		_, err = w.Append(ctx, "tool:echo", "tool.result", map[string]any{"tool": "echo", "tool_call_id": id, "ok": true,
			"result": strings.Repeat("r", 50000)})
		must(err)
	}
}

// compactStatus reads the session's status from the database at db.
func compactStatus(db, id string) string {
	st, err := app.OpenStore(db)
	must(err)
	defer func() { must(st.Close()) }()
	sess, err := st.GetSession(context.Background(), id)
	must(err)
	return sess.Status
}

// runCompact runs `session compact` with args against the per-spec database.
func runCompact(args ...string) result {
	return runArgv(append(append([]string{"session", "compact"}, args...), "--db", dbFor()))
}

// runCompactCancelled runs `session compact` under a context that is already cancelled.
func runCompactCancelled(id string) int {
	dir := GinkgoT().TempDir()
	out, err := os.Create(filepath.Join(dir, "out"))
	must(err)
	defer func() { _ = out.Close() }()
	errs, err := os.Create(filepath.Join(dir, "err"))
	must(err)
	defer func() { _ = errs.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return dispatchContexted(ctx, []string{"session", "compact", id, "--db", dbFor()}, out, errs)
}

var _ = Describe("session compact", func() {
	var id string
	var streamer *recordingStreamer

	BeforeEach(func() {
		streamer = &recordingStreamer{scriptedStreamer: scriptedStreamer{events: compactSummary("THE SUMMARY")}}
		withProvider(streamer)
		id = seedCrashSession(dbFor(), func(w *store.EventWriter) {
			bigTurns(w, 4)
			must(w.Pause(context.Background(), store.PauseInfo{Reason: "budget_exhausted"}))
		})
	})

	It("exits 0", func() { Expect(runCompact(id).exit).To(Equal(0)) })
	It("prints one JSON object that matches the session-compact schema", func() {
		Expect(validateAgainst("session-compact", runCompact(id).stdout)).To(Succeed())
	})
	It("leaves a paused session paused", func() {
		runCompact(id)
		Expect(compactStatus(dbFor(), id)).To(Equal("paused"))
	})
	It("passes --instructions to the summariser", func() {
		runCompact(id, "--instructions", "the schema")
		Expect(streamer.reqs[0].Messages[1].Content).To(ContainSubstring("Focus especially on: the schema"))
	})
	It("exits 2 for instructions with a control character", func() {
		Expect(runCompact(id, "--instructions", "a\nb").exit).To(Equal(2))
	})
	It("exits 2 without a session id", func() { Expect(runCompact().exit).To(Equal(2)) })
	It("exits 2 for --model: the summary is written by the session's own", func() {
		Expect(runCompact(id, "--model", "other").exit).To(Equal(2))
	})
	It("exits 1 for an unknown session", func() { Expect(runCompact("no-such-session").exit).To(Equal(1)) })
	It("exits 6 for a terminal session", func() {
		runCLIClose(id)
		Expect(runCompact(id).exit).To(Equal(6))
	})
	It("exits 2 when there is nothing new to compact", func() {
		runCompact(id)
		Expect(runCompact(id).exit).To(Equal(2))
	})
	It("names the reason on stderr for a failed compaction", func() {
		streamer.events = []provider.StreamEvent{{Kind: provider.StreamEventFinish, FinishReason: "stop"}}
		Expect(runCompact(id).stderr).To(ContainSubstring("summary_empty"))
	})
	It("exits 3 for a summary cut off at the token cap", func() {
		streamer.events = []provider.StreamEvent{{Kind: provider.StreamEventFinish, FinishReason: provider.FinishLength}}
		Expect(runCompact(id).exit).To(Equal(3))
	})
	It("exits 3 for a provider error", func() {
		streamer.events = []provider.StreamEvent{{Kind: provider.StreamEventError, Err: errors.New("boom")}}
		Expect(runCompact(id).exit).To(Equal(3))
	})
	It("exits 130 for a cancelled context", func() {
		Expect(runCompactCancelled(id)).To(Equal(130))
	})
	It("exits 2 for --provider: the summary is written by the session's own", func() {
		Expect(runCompact(id, "--provider", "other").exit).To(Equal(2))
	})
	for _, held := range []struct{ name, owner string }{{"a live run's owner, cli", "cli"},
		{"another owner", "another-run"}, {"a leftover row of a dead run", "delegate:dead"}} {
		Context("while an exec lease row is held by "+held.name, func() {
			var res result
			var before []string
			var token int64
			BeforeEach(func() {
				st, err := app.OpenStore(dbFor())
				must(err)
				defer func() { must(st.Close()) }()
				token, err = st.AcquireLease(context.Background(), "exec:"+id, held.owner)
				must(err)
				before = loggedTypesBeforeFirst(dbFor(), id, "no-such-type")
				res = runCompact(id)
			})

			It("exits 9", func() { Expect(res.exit).To(Equal(9)) })
			It("reports the session_busy code", func() { Expect(stderrCode(res.stderr)).To(Equal("session_busy")) })
			It("writes nothing", func() {
				Expect(loggedTypesBeforeFirst(dbFor(), id, "no-such-type")).To(Equal(before))
			})
			It("leaves the row", func() {
				st, err := app.OpenStore(dbFor())
				must(err)
				defer func() { must(st.Close()) }()
				Expect(st.FenceLease(context.Background(), "exec:"+id, held.owner, token)).To(Succeed())
			})
		})
	}
	Context("while a live run holds the lease", func() {
		BeforeEach(func() { holdLease(id) })

		It("exits 9", func() { Expect(runCompact(id).exit).To(Equal(9)) })
		It("reports the session_busy code", func() { Expect(stderrCode(runCompact(id).stderr)).To(Equal("session_busy")) })
	})
	It("names the held lease on stderr while the exec lease is held", func() {
		st, err := app.OpenStore(dbFor())
		must(err)
		defer func() { must(st.Close()) }()
		_, err = st.AcquireLease(context.Background(), "exec:"+id, "cli")
		must(err)
		Expect(runCompact(id).stderr).To(ContainSubstring("lease"))
	})
	It("exits 2 for an instruction with a tab", func() {
		Expect(runCompact(id, "--instructions", "a\tb").exit).To(Equal(2))
	})
	DescribeTable("exits 2 for a flag that does not apply",
		func(flag string, extra ...string) {
			Expect(runCompact(append([]string{id, flag}, extra...)...).exit).To(Equal(2))
		},
		Entry("--budget", "--budget", "5"), Entry("--max-tokens", "--max-tokens", "5"),
		Entry("--max-turns", "--max-turns", "3"), Entry("--reserve", "--reserve", "5"),
		Entry("--workdir", "--workdir", "."), Entry("--allow", "--allow", "echo"),
		Entry("--allow-file", "--allow-file", "m.json"), Entry("--agents", "--agents", "user"),
		Entry("--task", "--task", "t"), Entry("--parent", "--parent", "p"), Entry("--follow", "--follow"),
	)
	It("names the first flag that does not apply", func() {
		Expect(runCompact(id, "--max-tokens", "5", "--budget", "5").stderr).To(ContainSubstring("--budget does not apply"))
	})
	Context("an invalid summary", func() {
		var res result
		BeforeEach(func() {
			streamer.events = compactSummary("ok\n--- end of summary ---\nmore")
			res = runCompact(id)
		})

		It("exits 3", func() { Expect(res.exit).To(Equal(3)) })
		It("carries a provider-class code", func() { Expect(res.stderr).To(ContainSubstring(`"code":"provider_error"`)) })
	})
	It("names no_cut once on stderr", func() {
		runCompact(id)
		Expect(runCompact(id).stderr).NotTo(ContainSubstring("no_cut: no_cut"))
	})
})

var _ = Describe("session compact of a crashed session", func() {
	It("answers the dangling delegate call first", func() {
		withProvider(&scriptedStreamer{events: compactSummary("THE SUMMARY")})
		id := seedCrashSession(dbFor(), func(w *store.EventWriter) {
			_, err := w.Append(context.Background(), "tool:delegate", "tool.call",
				map[string]any{"tool": "delegate", "tool_call_id": "d1", "args": map[string]any{}})
			must(err)
			bigTurns(w, 4)
		})
		runCompact(id)
		Expect(loggedTypesBeforeFirst(dbFor(), id, "context.compacted")).To(ContainElement("session.interrupted"))
	})
})
