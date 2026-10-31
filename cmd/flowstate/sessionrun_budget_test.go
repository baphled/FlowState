package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// splitResult captures stdout and stderr separately for one dispatch.
type splitResult struct {
	exit   int
	stdout string
	stderr string
}

// runSplitArgv dispatches argv capturing stdout and stderr in distinct
// pipes so stderr error objects can be asserted independently.
func runSplitArgv(argv ...string) splitResult {
	outR, outW, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	outDone := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		outDone <- string(b)
	}()
	errDone := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(errR)
		errDone <- string(b)
	}()
	exit := dispatch(argv, outW, errW)
	_ = outW.Close()
	_ = errW.Close()
	out := <-outDone
	errOut := <-errDone
	_ = outR.Close()
	_ = errR.Close()
	return splitResult{exit: exit, stdout: out, stderr: errOut}
}

// newZaiSession provisions a z.ai session and returns its id.
func newZaiSession() string {
	created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
	if created.exit != 0 {
		panic("session new failed")
	}
	rec := decodeNew(created.stdout)
	return rec.ID
}

// longChunkStream returns a server streaming one long chunk.
func longChunkStream() *httptest.Server {
	return fakeZaiServer(`{"choices":[{"delta":{"content":"` + strings.Repeat("x", 64) + `"}}]}`)
}

// jsonLines parses each non-empty line of s as raw JSON.
func jsonLines(s string) []map[string]any {
	var out []map[string]any
	for _, l := range nonEmpty(strings.Split(s, "\n")) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			panic(err)
		}
		out = append(out, m)
	}
	return out
}

var _ = Describe("Session run budget", func() {
	It("exits 2 with usage when --budget has a non-numeric value", func() {
		Expect(runCLIRun("some-id", "--budget", "abc").exit).To(Equal(2))
	})

	It("exits 2 with usage when --max-tokens is missing a value", func() {
		Expect(runCLIRun("some-id", "--max-tokens").exit).To(Equal(2))
	})

	It("exits 5 when the budget cap is exceeded mid-stream", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		res := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		Expect(res.exit).To(Equal(5))
	})

	It("writes a budget_exceeded JSON error object to stderr on budget stop", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		res := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		var eobj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(res.stderr)), &eobj); err != nil {
			panic(err)
		}
		Expect(eobj["code"]).To(Equal("budget_exceeded"))
	})

	It("ends stdout with a session.paused event on budget stop", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		res := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		lines := jsonLines(res.stdout)
		last := lines[len(lines)-1]
		Expect(last["type"]).To(Equal("session.paused"))
	})

	It("leaves the session paused in the store after a budget stop", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		st, err := store.Open(dbFor())
		if err != nil {
			panic(err)
		}
		DeferCleanup(st.Close)
		sess, serr := st.GetSession(context.Background(), id)
		if serr != nil {
			panic(serr)
		}
		Expect(sess.Status).To(Equal("paused"))
	})

	It("exits 0 when a paused session is resumed for the next turn", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		first := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		if first.exit != 5 {
			panic("expected budget stop")
		}
		second := runSplitArgv("session", "run", "--db", dbFor(), id, "--max-tokens", "1000")
		Expect(second.exit).To(Equal(0))
	})

	It("emits a session.resumed event before the next turn", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		first := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		if first.exit != 5 {
			panic("expected budget stop")
		}
		second := runSplitArgv("session", "run", "--db", dbFor(), id, "--max-tokens", "1000")
		lines := jsonLines(second.stdout)
		resumedAt := -1
		for i, l := range lines {
			if l["type"] == "session.resumed" {
				resumedAt = i
			}
		}
		Expect(resumedAt).To(BeNumerically(">=", 0))
	})

	It("resumes the paused session before the next user message", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		first := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		if first.exit != 5 {
			panic("expected budget stop")
		}
		second := runSplitArgv("session", "run", "--db", dbFor(), id, "--max-tokens", "1000")
		lines := jsonLines(second.stdout)
		resumedAt := -1
		messageAt := -1
		for i, l := range lines {
			if l["type"] == "session.resumed" {
				resumedAt = i
			}
			if l["type"] == "user.message" {
				messageAt = i
			}
		}
		Expect(resumedAt).To(BeNumerically("<", messageAt))
	})

	It("resumes the paused session with the user as actor", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		first := runSplitArgv("session", "run", "--db", dbFor(), id, "--budget", "8")
		if first.exit != 5 {
			panic("expected budget stop")
		}
		second := runSplitArgv("session", "run", "--db", dbFor(), id, "--max-tokens", "1000")
		lines := jsonLines(second.stdout)
		resumedAt := -1
		for i, l := range lines {
			if l["type"] == "session.resumed" {
				resumedAt = i
			}
		}
		Expect(lines[resumedAt]["actor"]).To(Equal("user"))
	})
})

var _ = Describe("Session run interrupt", func() {
	It("cancels an active session with exit code 130", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		st, err := store.Open(dbFor())
		if err != nil {
			panic(err)
		}
		DeferCleanup(st.Close)
		ctx := context.Background()
		code := cancelOnInterrupt(ctx, st, id)
		Expect(code).To(Equal(130))
	})

	It("marks an interrupted session as cancelled in the store", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		st, err := store.Open(dbFor())
		if err != nil {
			panic(err)
		}
		DeferCleanup(st.Close)
		ctx := context.Background()
		_ = cancelOnInterrupt(ctx, st, id)
		sess, serr := st.GetSession(ctx, id)
		if serr != nil {
			panic(serr)
		}
		Expect(sess.Status).To(Equal("cancelled"))
	})

	It("persists a session.cancelled event on interrupt", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		st, err := store.Open(dbFor())
		if err != nil {
			panic(err)
		}
		DeferCleanup(st.Close)
		ctx := context.Background()
		_ = cancelOnInterrupt(ctx, st, id)
		evts, rerr := st.Replay(ctx, id)
		if rerr != nil {
			panic(rerr)
		}
		Expect(evts[len(evts)-1].Type).To(Equal("session.cancelled"))
	})

	It("returns 6 when cancelling an already-terminal session", func() {
		withFakeZai(longChunkStream())
		id := newZaiSession()
		st, err := store.Open(dbFor())
		if err != nil {
			panic(err)
		}
		DeferCleanup(st.Close)
		ctx := context.Background()
		if _, cerr := session.Close(ctx, st, id); cerr != nil {
			panic(cerr)
		}
		Expect(cancelOnInterrupt(ctx, st, id)).To(Equal(6))
	})
})
