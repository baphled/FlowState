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
	ExpectWithOffset(1, r.exit).To(Equal(0))
	return decodeNew(r.stdout).ID
}

var _ = Describe("Session tool", func() {
	It("persists tool.call and tool.result events for an allowed echo invocation", func() {
		id := newTestSession()
		r := runTool(id, "--tool", "echo", "--tool-arg", `{"a":1}`, "--allow", "echo")
		Expect(r.exit).To(Equal(0))
		Expect(r.stdout).To(ContainSubstring(`"ok":true`))
		ev := runCLIEvents(id)
		Expect(ev.exit).To(Equal(0))
		Expect(ev.stdout).To(ContainSubstring(tool.TypeToolCall))
		Expect(ev.stdout).To(ContainSubstring(tool.TypeToolResult))
		Expect(ev.stdout).To(ContainSubstring(`"tool:echo"`))
	})

	It("denies an un-allowlisted tool with a typed event and usage exit", func() {
		id := newTestSession()
		r := runTool(id, "--tool", "echo")
		Expect(r.exit).To(Equal(exitUsage))
		ev := runCLIEvents(id)
		Expect(ev.stdout).To(ContainSubstring("tool_denied"))
	})

	It("exits usage for an unknown tool without emitting a tool.call event", func() {
		id := newTestSession()
		r := runTool(id, "--tool", "ghost", "--allow", "ghost")
		Expect(r.exit).To(Equal(exitUsage))
		ev := runCLIEvents(id)
		Expect(ev.stdout).NotTo(ContainSubstring(tool.TypeToolCall))
	})

	It("allows a tool listed in an allow-file manifest", func() {
		manifest := filepath.Join(GinkgoT().TempDir(), "allow.json")
		Expect(os.WriteFile(manifest, []byte(`["echo"]`), 0o644)).To(Succeed())
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
		Expect(os.WriteFile(bad, []byte(`{"not":"an array"}`), 0o644)).To(Succeed())
		r := runTool(id, "--tool", "echo", "--allow-file", bad)
		Expect(r.exit).To(Equal(exitUsage))
	})

	It("exits terminal when invoking a tool on a closed session", func() {
		id := newTestSession()
		Expect(runCLIClose(id).exit).To(Equal(0))
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
		Expect(runTool(id, "--tool", "echo", "--allow", "echo").exit).To(Equal(0))
		Expect(runTool(id, "--tool", "echo", "--allow", "echo").exit).To(Equal(0))
		ev := runCLIEvents(id)
		var seqs []int
		for _, line := range strings.Split(strings.TrimSpace(ev.stdout), "\n") {
			var e struct {
				Seq int `json:"seq"`
			}
			Expect(json.Unmarshal([]byte(line), &e)).To(Succeed())
			seqs = append(seqs, e.Seq)
		}
		Expect(seqs).To(Equal([]int{0, 1, 2, 3, 4}))
	})
})
