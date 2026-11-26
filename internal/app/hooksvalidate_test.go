package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func allHookPoints() HookAccepted {
	return HookAccepted{Transform: true, Points: []HookPoint{HookSessionStart, HookUserPrompt, HookToolBefore,
		HookToolAfter, HookTurnAfter, HookCompactionBefore, HookCompactionAfter, HookSessionEnd}}
}

func putHookScript(dir, name string) string {
	return putHookMode(dir, name, "#!/bin/sh\nexit 0\n", 0o700)
}

func cmdHook(id, point, extra string) string {
	return `{"id":"` + id + `","point":"` + point + `","kind":"command",` + extra + `"argv":["./x.sh"]}`
}

var _ = Describe("Hook document validation", func() {
	var dir string
	var ctx hookContext

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		putHookScript(dir, "x.sh")
		ctx = hookContext{accepted: allHookPoints(), dir: dir, origin: "h.json", source: HookSourceUser}
	})

	doc := func(hooks string) []byte { return []byte(`{"version":1,"hooks":[` + hooks + `]}`) }

	DescribeTable("refuses",
		func(body, want string) {
			_, err := parseHookDocument([]byte(body), ctx)
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("a secret-looking key", `{"version":1,"hooks":[],"api_key":"x"}`, "looks like a secret"),
		Entry("a secret-looking key nested in a hook", `{"version":1,"hooks":[{"id":"a","secret_x":1}]}`, "looks like a secret"),
		Entry("an unknown field", `{"version":1,"hooks":[],"extra":1}`, "unknown field"),
		Entry("an unknown hook field", `{"version":1,"hooks":[{"id":"a","bogus":1}]}`, "unknown field"),
		Entry("trailing content", `{"version":1,"hooks":[]} {}`, "not valid JSON"),
		Entry("another version", `{"version":2,"hooks":[]}`, "version must be 1"),
		Entry("a missing version", `{"hooks":[]}`, "version must be 1"),
	)

	DescribeTable("refuses a hook with",
		func(hook, want string) {
			_, err := parseHookDocument(doc(hook), ctx)
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("an unknown point", cmdHook("a", "tool.sideways", ""), "unknown point"),
		Entry("an invalid id", cmdHook("Bad_ID", "tool.before", ""), "id must match"),
		Entry("a match on a non-tool point", cmdHook("a", "turn.after", `"match":["fs.*"],`), "match only applies"),
		Entry("an empty match", cmdHook("a", "tool.before", `"match":[],`), "match is empty"),
		Entry("a regex-looking match", cmdHook("a", "tool.before", `"match":["fs.(w|r)"],`), "glob"),
		Entry("on_failure deny on tool.after", cmdHook("a", "tool.after", `"on_failure":"deny",`), "nothing to deny"),
		Entry("on_failure deny on turn.after", cmdHook("a", "turn.after", `"on_failure":"deny",`), "nothing to deny"),
		Entry("an unknown on_failure", cmdHook("a", "tool.before", `"on_failure":"maybe",`), "deny or allow"),
		Entry("a timeout over 60 s", cmdHook("a", "tool.before", `"timeout_ms":60001,`), "timeout_ms"),
		Entry("a zero timeout", cmdHook("a", "tool.before", `"timeout_ms":0,`), "timeout_ms"),
		Entry("transform off tool.before", cmdHook("a", "tool.after", `"transform":true,`), "only allowed on tool.before"),
		Entry("an unknown kind", `{"id":"a","point":"tool.before","kind":"shell"}`, "command or decision"),
		Entry("a decision hook", `{"id":"a","point":"tool.before","kind":"decision"}`, "not supported"),
		Entry("a system_one block", `{"version":1,"system_one":{"model":"k"},"hooks":[]}`, "unknown field"),
		Entry("a command with no argv", `{"id":"a","point":"tool.before","kind":"command"}`, "non-empty argv"),
		Entry("a secret-named env", `{"id":"a","point":"tool.before","kind":"command","argv":["x"],"env":["MY_SECRET"]}`, "secret-looking"),
	)

	It("refuses a duplicate id inside one file", func() {
		_, err := parseHookDocument(doc(cmdHook("a", "tool.before", "")+","+cmdHook("a", "turn.after", "")), ctx)
		Expect(err).To(MatchError(ContainSubstring("declared twice")))
	})

	DescribeTable("defaults",
		func(hooks string, field func(Hook) any, want any) {
			parsed, err := parseHookDocument(doc(hooks), ctx)
			mustHook(err)
			Expect(field(parsed[0])).To(Equal(want))
		},
		Entry("a gate point's on_failure to deny", cmdHook("a", "tool.before", ""),
			func(h Hook) any { return h.OnFailure }, HookOnFailureDeny),
		Entry("a non-gate point's on_failure to allow", cmdHook("a", "turn.after", ""),
			func(h Hook) any { return h.OnFailure }, HookOnFailureAllow),
		Entry("the timeout to 10 s", cmdHook("a", "tool.before", ""),
			func(h Hook) any { return h.TimeoutMS }, DefaultHookTimeoutMS),
	)

	It("keeps hooks in file order", func() {
		hooks, err := parseHookDocument(doc(cmdHook("z", "tool.before", "")+","+cmdHook("a", "turn.after", "")), ctx)
		mustHook(err)
		Expect([]string{hooks[0].ID, hooks[1].ID}).To(Equal([]string{"z", "a"}))
	})

	DescribeTable("the accepted-point set",
		func(accepted HookAccepted, point, extra string) {
			ctx.accepted = accepted
			_, err := parseHookDocument(doc(cmdHook("a", point, extra)), ctx)
			Expect(err).To(MatchError(ErrHookPointUnsupported))
		},
		Entry("refuses a point outside the set", HookAccepted{Points: []HookPoint{HookToolBefore}}, "tool.after", ""),
		Entry("refuses turn.after when the set is the production one", AcceptedHooks(), "turn.after", ""),
		Entry("refuses compaction.before when the set is the production one", AcceptedHooks(), "compaction.before", ""),
		Entry("refuses compaction.after when the set is the production one", AcceptedHooks(), "compaction.after", ""),
		Entry("refuses transform: true before it is accepted", HookAccepted{Points: []HookPoint{HookToolBefore}}, "tool.before", `"transform":true,`),
	)

	DescribeTable("the run-level points the production set accepts (packet 4a)",
		func(point string) {
			ctx.accepted = AcceptedHooks()
			hooks, err := parseHookDocument(doc(cmdHook("a", point, "")), ctx)
			mustHook(err)
			Expect(string(hooks[0].Point)).To(Equal(point))
		},
		Entry("user.prompt", "user.prompt"),
		Entry("session.start", "session.start"),
		Entry("session.end", "session.end"),
	)

	It("accepts transform: true once it is accepted", func() {
		hooks, err := parseHookDocument(doc(cmdHook("a", "tool.before", `"transform":true,`)), ctx)
		mustHook(err)
		Expect(hooks[0].Transform).To(BeTrue())
	})
})
