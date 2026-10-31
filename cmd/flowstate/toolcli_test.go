package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
)

// runTool executes `session tool` against the per-spec test DB.
func runTool(id string, extra ...string) result {
	argv := append([]string{"session", "tool", "--db", dbFor(), id}, extra...)
	return runArgv(argv)
}

// newTestSession creates a session via the CLI and returns its id.
func newTestSession() string {
	r := runCLINew("--provider", "zai", "--model", "glm", "--task", "x")
	if r.exit != 0 {
		panic("session new failed")
	}
	return decodeNew(r.stdout).ID
}

var _ = Describe("Session tool", func() {
	Context("persists tool.call and tool.result events for an allowed echo invocation", func() {
		var observedExit int
		var observedSucceeded bool
		var observedHasToolCall bool
		var observedHasToolResult bool
		var observedHasActor bool
		BeforeEach(func() {
			id := newTestSession()
			r := runTool(id, "--tool", "echo", "--tool-arg", `{"a":1}`, "--allow", "echo")
			ev := runCLIEvents(id)
			observedExit = r.exit
			observedSucceeded = strings.Contains(r.stdout, `"ok":true`)
			observedHasToolCall = strings.Contains(ev.stdout, tool.TypeToolCall)
			observedHasToolResult = strings.Contains(ev.stdout, tool.TypeToolResult)
			observedHasActor = strings.Contains(ev.stdout, `"tool:echo"`)
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(0)) })
		It("prints a successful tool response", func() { Expect(observedSucceeded).To(BeTrue()) })
		It("records the expected tool call presence", func() { Expect(observedHasToolCall).To(BeTrue()) })
		It("records a tool result", func() { Expect(observedHasToolResult).To(BeTrue()) })
		It("attributes the event to the echo tool", func() { Expect(observedHasActor).To(BeTrue()) })
	})

	Context("denies an un-allowlisted tool with a typed event and usage exit", func() {
		var observedExit int
		var observedHasDenial bool
		BeforeEach(func() {
			id := newTestSession()
			r := runTool(id, "--tool", "echo")
			ev := runCLIEvents(id)
			observedExit = r.exit
			observedHasDenial = strings.Contains(ev.stdout, "tool_denied")
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(exitUsage)) })
		It("records a tool denial", func() { Expect(observedHasDenial).To(BeTrue()) })
	})

	Context("exits usage for an unknown tool without emitting a tool.call event", func() {
		var observedExit int
		var observedHasToolCall bool
		BeforeEach(func() {
			id := newTestSession()
			r := runTool(id, "--tool", "ghost", "--allow", "ghost")
			ev := runCLIEvents(id)
			observedExit = r.exit
			observedHasToolCall = strings.Contains(ev.stdout, tool.TypeToolCall)
		})
		It("returns the expected exit status", func() { Expect(observedExit).To(Equal(exitUsage)) })
		It("records the expected tool call presence", func() { Expect(observedHasToolCall).To(BeFalse()) })
	})

	It("allows a tool listed in an allow-file manifest", func() {
		manifest := filepath.Join(GinkgoT().TempDir(), "allow.json")
		if err := os.WriteFile(manifest, []byte(`["echo"]`), 0o644); err != nil {
			panic(err)
		}
		id := newTestSession()
		r := runTool(id, "--tool", "echo", "--allow-file", manifest)
		Expect(r.exit).To(Equal(0))
	})

	It("exits usage for a missing allow-file", func() {
		id := newTestSession()
		r := runTool(id, "--tool", "echo", "--allow-file", "/nonexistent.json")
		Expect(r.exit).To(Equal(exitUsage))
	})

	It("exits usage for an allow-file that is not a JSON array", func() {
		id := newTestSession()
		bad := filepath.Join(GinkgoT().TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte(`{"not":"an array"}`), 0o644); err != nil {
			panic(err)
		}
		r := runTool(id, "--tool", "echo", "--allow-file", bad)
		Expect(r.exit).To(Equal(exitUsage))
	})

	It("exits terminal when invoking a tool on a closed session", func() {
		id := newTestSession()
		if c := runCLIClose(id); c.exit != 0 {
			panic("session close failed")
		}
		r := runTool(id, "--tool", "echo", "--allow", "echo")
		Expect(r.exit).To(Equal(exitTerminal))
	})

	It("exits unknown-session for a tool invocation on a missing session", func() {
		r := runTool("no-such-session", "--tool", "echo", "--allow", "echo")
		Expect(r.exit).To(Equal(exitUnknownSession))
	})

	It("exits usage when no --tool flag is given", func() {
		id := newTestSession()
		Expect(runTool(id).exit).To(Equal(exitUsage))
	})

	It("continues event sequence contiguously across two invocations", func() {
		id := newTestSession()
		if r := runTool(id, "--tool", "echo", "--allow", "echo"); r.exit != 0 {
			panic("first tool call failed")
		}
		if r := runTool(id, "--tool", "echo", "--allow", "echo"); r.exit != 0 {
			panic("second tool call failed")
		}
		ev := runCLIEvents(id)
		var seqs []int
		for _, line := range strings.Split(strings.TrimSpace(ev.stdout), "\n") {
			var e struct {
				Seq int `json:"seq"`
			}
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				panic(err)
			}
			seqs = append(seqs, e.Seq)
		}
		Expect(seqs).To(Equal([]int{0, 1, 2, 3, 4}))
	})
})
