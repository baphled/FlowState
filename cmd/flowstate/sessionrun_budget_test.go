package main

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// runCLIRunBudget runs `session run` with a budget flag against the
// per-spec temp DB.
func runCLIRunBudget(id, flag, v string) result {
	return runArgv([]string{"session", "run", id, flag, v, "--db", dbFor()})
}

// newBudgetSession makes a session whose fake provider streams
// exactly "hello world" (11 chars) in four chunks; no assertions.
func newBudgetSession() string {
	srv := fakeZaiServer(
		`{"choices":[{"delta":{"content":"he"}}]}`,
		`{"choices":[{"delta":{"content":"ll"}}]}`,
		`{"choices":[{"delta":{"content":"o"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
	)
	withFakeZai(srv)
	created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
	return decodeNew(created.stdout).ID
}

// lastEvent decodes the final JSONL event envelope of a run; no
// assertions, so it stays a pure projection helper.
func lastEvent(out string) (eventType string, payload json.RawMessage) {
	got := nonEmpty(strings.Split(out, "\n"))
	var last struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(got[len(got)-1]), &last); err != nil {
		return "", nil
	}
	return last.Type, last.Payload
}

var _ = Describe("Session run budget flags", func() {
	Context("--budget", func() {
		It("exits 5 when the cap is hit", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			Expect(run.exit).To(Equal(5))
		})

		It("ends the stream with session.paused as the final event", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			t, _ := lastEvent(run.stdout)
			Expect(t).To(Equal("session.paused"))
		})

		It("carries the budget_exceeded reason in the pause payload", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			_, p := lastEvent(run.stdout)
			Expect(strings.Contains(string(p), `"reason":"budget_exceeded"`)).To(BeTrue())
		})

		It("emits a budget_exceeded error object on stderr", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			Expect(run.stderrJSON().Code).To(Equal("budget_exceeded"))
		})

		It("does not emit turn.end, so the pause is not terminal", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			Expect(strings.Contains(run.stdout, `"type":"turn.end"`)).To(BeFalse())
		})

		It("rejects a non-integer cap with usage exit 2", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "abc")
			Expect(run.exit).To(Equal(2))
		})

		It("rejects a non-integer cap with a usage error object", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "abc")
			Expect(run.stderrJSON().Code).To(Equal("usage"))
		})

		It("completes with exit 0 when the budget exceeds the response", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "10000")
			Expect(run.exit).To(Equal(0))
		})

		It("ends with turn.end when the budget exceeds the response", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "10000")
			t, _ := lastEvent(run.stdout)
			Expect(t).To(Equal("turn.end"))
		})
	})

	Context("--max-tokens", func() {
		It("bounds the turn identically to --budget (exit 5)", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--max-tokens", "3")
			Expect(run.exit).To(Equal(5))
		})

		It("bounds the turn identically to --budget (session.paused)", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--max-tokens", "3")
			t, _ := lastEvent(run.stdout)
			Expect(t).To(Equal("session.paused"))
		})

		It("supports the --flag=value form", func() {
			id := newBudgetSession()
			run := runArgv([]string{"session", "run", id, "--max-tokens=3", "--db", dbFor()})
			Expect(run.exit).To(Equal(5))
		})

		It("applies the tighter cap when both flags are set", func() {
			id := newBudgetSession()
			run := runArgv([]string{"session", "run", id, "--budget", "10000", "--max-tokens", "3", "--db", dbFor()})
			Expect(run.exit).To(Equal(5))
		})
	})
})
