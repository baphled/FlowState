package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// result captures the exit code and stdout of one CLI run.
type result struct {
	exit   int
	stdout string
}

// runCLI executes dispatch against the per-spec DB with captured stdout.
func runCLI(args ...string) result {
	argv := append([]string{"session", "--db", dbFor()}, args...)
	return runArgv(argv)
}

// runArgv runs a full session argv including any --db override. Output
// is drained concurrently so a full pipe buffer cannot deadlock.
func runArgv(argv []string) result {
	outR, outW, err := os.Pipe()
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- string(b)
	}()
	exit := dispatch(argv, outW, outW)
	_ = outW.Close()
	out := <-done
	_ = outR.Close()
	return result{exit: exit, stdout: out}
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, l := range lines(s) {
		return l
	}
	return ""
}

// lines returns the non-empty lines of s.
func lines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			l := s[start:i]
			if l != "" {
				out = append(out, l)
			}
			start = i + 1
		}
	}
	return out
}

// runCLIClose runs `session close <id>` against the per-spec temp DB.
func runCLIClose(id string) result {
	return runArgv([]string{"session", "close", id, "--db", dbFor()})
}

// runCLIEvents runs `session events <id>` against the per-spec temp DB.
func runCLIEvents(id string) result {
	return runArgv([]string{"session", "events", id, "--db", dbFor()})
}

// runCLINew runs `session new` with the given args against the
// per-spec temp DB.
func runCLINew(args ...string) result {
	return runArgv(append([]string{"session", "new", "--db", dbFor()}, args...))
}

// runCLIList runs `session list` with the given args against the
// per-spec temp DB.
func runCLIList(args ...string) result {
	return runArgv(append([]string{"session", "list", "--db", dbFor()}, args...))
}

// newRecord captures the JSON record printed by `session new`.
type newRecord struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// decodeNew parses the first line of `session new` output as JSON.
func decodeNew(out string) newRecord {
	var rec newRecord
	ExpectWithOffset(1, json.Unmarshal([]byte(firstLine(out)), &rec)).To(Succeed())
	return rec
}

var _ = Describe("Session CLI usage", func() {
	It("exits 2 with usage when invoked with no args", func() {
		Expect(runCLI().exit).To(Equal(2))
	})

	It("exits 2 with usage for an unknown command", func() {
		Expect(runCLI("bogus").exit).To(Equal(2))
	})

	It("exits 2 with usage when session new has no flags", func() {
		Expect(runCLI("new").exit).To(Equal(2))
	})

	It("exits 2 with usage for an unknown flag", func() {
		Expect(runCLIList("--nope").exit).To(Equal(2))
	})

	It("exits 2 with usage when a flag value is missing", func() {
		Expect(runCLIList("--db").exit).To(Equal(2))
	})

	It("exits 2 with usage for an unreadable @task file on session new", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "@/nonexistent/file")
		Expect(r.exit).To(Equal(2))
	})

	It("exits 4 for an invalid DB path blocked by a regular file", func() {
		dir := GinkgoT().TempDir()
		blocker := filepath.Join(dir, "blocker")
		Expect(os.WriteFile(blocker, []byte("file"), 0o644)).To(Succeed())
		r := runArgv([]string{"session", "list", "--db", filepath.Join(blocker, "x.db")})
		Expect(r.exit).To(Equal(4))
	})
})

var _ = Describe("Session list", func() {
	It("exits 0 with no output against a fresh DB", func() {
		r := runCLIList()
		Expect(r.exit).To(Equal(0))
		Expect(r.stdout).To(BeEmpty())
	})

	It("exits 2 with usage for an invalid status filter", func() {
		Expect(runCLIList("--status", "zombie").exit).To(Equal(2))
	})

	It("lists a created session", func() {
		r := runCLINew("--provider", "prov", "--model", "m1", "--task", "t")
		Expect(r.exit).To(Equal(0))
		decodeNew(r.stdout)
		l := runCLIList()
		Expect(l.exit).To(Equal(0))
		Expect(lines(l.stdout)).To(HaveLen(1))
	})

	It("lists a closed session when filtered by status cancelled", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "t")
		rec := decodeNew(r.stdout)
		runCLIClose(rec.ID)
		l := runCLIList("--status", "cancelled")
		Expect(l.exit).To(Equal(0))
		Expect(lines(l.stdout)).To(HaveLen(1))
	})
})

var _ = Describe("Session new", func() {
	It("prints a JSON record with status active and exit 0", func() {
		r := runCLINew("--provider", "prov", "--model", "m1", "--task", "do things")
		Expect(r.exit).To(Equal(0))
		rec := decodeNew(r.stdout)
		Expect(rec.Status).To(Equal("active"))
		Expect(rec.ID).NotTo(BeEmpty())
	})

	It("uses @task file contents as the task", func() {
		f := GinkgoT().TempDir() + "/task.txt"
		Expect(os.WriteFile(f, []byte("task from file"), 0o644)).To(Succeed())
		r := runCLINew("--provider", "p", "--model", "m", "--task", "@"+f)
		Expect(r.exit).To(Equal(0))
	})
})

var _ = Describe("Session events", func() {
	It("exits 1 for an unknown session id", func() {
		Expect(runCLIEvents("missing-id").exit).To(Equal(1))
	})

	It("exits 2 with usage when the id is missing", func() {
		Expect(runCLI("events").exit).To(Equal(2))
	})

	It("emits a single turn.start JSONL event for a created session", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "hello")
		rec := decodeNew(r.stdout)
		e := runCLIEvents(rec.ID)
		Expect(e.exit).To(Equal(0))
		Expect(lines(e.stdout)).To(HaveLen(1))
		var ev struct {
			Type string `json:"type"`
			Seq  int    `json:"seq"`
		}
		Expect(json.Unmarshal([]byte(firstLine(e.stdout)), &ev)).To(Succeed())
		Expect(ev.Type).To(Equal("turn.start"))
		Expect(ev.Seq).To(Equal(0))
	})
})

var _ = Describe("Session close", func() {
	It("exits 1 for an unknown session id", func() {
		Expect(runCLIClose("missing-id").exit).To(Equal(1))
	})

	It("cancels an active session with exit 0 and status output", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "t")
		rec := decodeNew(r.stdout)
		c := runCLIClose(rec.ID)
		Expect(c.exit).To(Equal(0))
		var out struct {
			Status string `json:"status"`
		}
		Expect(json.Unmarshal([]byte(firstLine(c.stdout)), &out)).To(Succeed())
		Expect(out.Status).To(Equal("cancelled"))
	})

	It("exits 6 when closing an already closed session", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "t")
		rec := decodeNew(r.stdout)
		runCLIClose(rec.ID)
		Expect(runCLIClose(rec.ID).exit).To(Equal(6))
	})

	It("emits session.cancelled as the last event after close", func() {
		r := runCLINew("--provider", "p", "--model", "m", "--task", "t")
		rec := decodeNew(r.stdout)
		runCLIClose(rec.ID)
		e := runCLIEvents(rec.ID)
		evs := lines(e.stdout)
		Expect(evs).To(HaveLen(2))
		var last struct {
			Type string `json:"type"`
		}
		Expect(json.Unmarshal([]byte(evs[1]), &last)).To(Succeed())
		Expect(last.Type).To(Equal("session.cancelled"))
	})
})

var _ = Describe("Session events replay integrity", func() {
	It("exits 7 on a tampered event payload", func() {
		db := GinkgoT().TempDir() + "/t.db"
		st, err := store.Open(db)
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		sess, err := st.CreateSession(ctx, "sid-tamper", "p", "m", "h", "sha")
		Expect(err).NotTo(HaveOccurred())
		w, err := st.NewEventWriter(ctx, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Append(ctx, "user", "turn.start", map[string]any{"task": "t"})
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		err = st.TamperEventPayload(ctx, sess.ID, 0, `{"task":"tampered"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Close()).To(Succeed())
		r := runArgv([]string{"session", "events", sess.ID, "--db", db})
		Expect(r.exit).To(Equal(7))
		fmt.Fprintln(os.Stderr)
	})
})
