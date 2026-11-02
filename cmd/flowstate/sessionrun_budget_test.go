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

var _ = Describe("Session run budget flags", func() {
	newBudgetSession := func() string {
		srv := fakeZaiServer(
			`{"choices":[{"delta":{"content":"he"}}]}`,
			`{"choices":[{"delta":{"content":"ll"}}]}`,
			`{"choices":[{"delta":{"content":"o"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
		)
		withFakeZai(srv)
		created := runCLINew("--provider", "z.ai", "--model", "glm-4.6", "--task", "greet")
		Expect(created.exit).To(Equal(0))
		return decodeNew(created.stdout).ID
	}

	lastEvent := func(out string) struct {
		Type    string          `json:"type"`
		Status  string          `json:"status"`
		Payload json.RawMessage `json:"payload"`
	} {
		got := nonEmpty(strings.Split(out, "\n"))
		var last struct {
			Type    string          `json:"type"`
			Status  string          `json:"status"`
			Payload json.RawMessage `json:"payload"`
		}
		Expect(json.Unmarshal([]byte(got[len(got)-1]), &last)).To(Succeed())
		return last
	}

	Context("--budget", func() {
		It("exits 5 with session.paused as the final event when the cap is hit", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			Expect(run.exit).To(Equal(5))
			last := lastEvent(run.stdout)
			Expect(last.Type).To(Equal("session.paused"))
			Expect(strings.Contains(string(last.Payload), `"reason":"budget_exceeded"`)).To(BeTrue())
		})

		It("emits a budget_exceeded error object on stderr", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "3")
			Expect(run.stderrJSON().Code).To(Equal("budget_exceeded"))
		})

		It("marks the session paused, not terminal", func() {
			id := newBudgetSession()
			Expect(runCLIRunBudget(id, "--budget", "3").exit).To(Equal(5))
			evts := runCLIEvents(id)
			var statuses []string
			for _, l := range nonEmpty(strings.Split(evts.stdout, "\n")) {
				var e struct {
					Type string `json:"type"`
				}
				Expect(json.Unmarshal([]byte(l), &e)).To(Succeed())
				statuses = append(statuses, e.Type)
			}
			Expect(statuses).NotTo(ContainElement("turn.end"))
			Expect(statuses).To(ContainElement("session.paused"))
		})

		It("rejects a non-integer cap with usage exit 2", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "abc")
			Expect(run.exit).To(Equal(2))
			Expect(run.stderrJSON().Code).To(Equal("usage"))
		})

		It("completes normally when the budget exceeds the response", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--budget", "10000")
			Expect(run.exit).To(Equal(0))
			Expect(lastEvent(run.stdout).Type).To(Equal("turn.end"))
		})
	})

	Context("--max-tokens", func() {
		It("bounds the turn identically to --budget", func() {
			id := newBudgetSession()
			run := runCLIRunBudget(id, "--max-tokens", "3")
			Expect(run.exit).To(Equal(5))
			Expect(lastEvent(run.stdout).Type).To(Equal("session.paused"))
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
