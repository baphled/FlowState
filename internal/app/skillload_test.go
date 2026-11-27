package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

const tddBody = "Write the test\tfirst.\n" // a tab: a control the text rule leaves allowed

func skillText(name, body string) string {
	return "---\nname: " + name + "\ndescription: About " + name + "\n---\n" + body
}

// putSkill writes a skill directory below root and returns the skill's directory.
func putSkill(root, name, body string) string {
	dir := filepath.Join(root, name)
	putAgent(dir, skill.FileName, skillText(name, body))
	return dir
}

func putFile(dir, rel, content string) {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	putAgent(filepath.Dir(path), filepath.Base(path), content)
}

func skillCall(id, args string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: tool.SkillLoadName, Arguments: json.RawMessage(args)}
}

// skillRun is one root run's fixture; userSkills is the user skills directory the run reads.
type skillRun struct {
	st         *store.Store
	sess       *store.Session
	dbPath     string
	workdir    string
	userSkills string
	events     []store.RecordedEvent
	streamer   *execLoopStreamer
	rec        *registryRecorder // the registries of every loop the run starts, the root's and any child's
}

// run runs the loop over turns and replays the log; the error is the spec's to judge.
func (r *skillRun) run(req SessionExecLoopRequest, turns ...[]provider.StreamEvent) error {
	r.streamer = &execLoopStreamer{turns: turns}
	req.Workdir, req.MaxTurns, req.Lease = r.workdir, 4, heldSessionLease(r.st, r.sess)
	_, err := RunSessionExecLoop(context.Background(), r.st, r.dbPath, r.sess, r.streamer, req)
	r.events = must(r.st.Replay(context.Background(), r.sess.ID))
	return err
}

func (r *skillRun) toolMessage(callID string) string {
	for i := len(r.streamer.reqs) - 1; i >= 0; i-- {
		for _, m := range r.streamer.reqs[i].Messages {
			if m.Role == "tool" && m.ToolCallID == callID {
				return m.Content
			}
		}
	}
	panic("no tool message for " + callID)
}

func (r *skillRun) types() []string {
	out := []string{}
	for _, e := range r.events {
		out = append(out, e.Type)
	}
	return out
}

func (r *skillRun) ofType(typ string) []store.RecordedEvent {
	return slices.DeleteFunc(slices.Clone(r.events), func(e store.RecordedEvent) bool { return e.Type != typ })
}

// resultCode is the error.code of the run's one tool.result.
func (r *skillRun) resultCode() string {
	failure, _ := payloadMap(r.one(tool.TypeToolResult).PayloadJSON)["error"].(map[string]any)
	code, _ := failure["code"].(string)
	return code
}

func (r *skillRun) one(typ string) store.RecordedEvent {
	found := r.ofType(typ)
	if len(found) != 1 {
		panic("expected one " + typ + ", found " + strings.Join(r.types(), ","))
	}
	return found[0]
}

// childID is the one session of the fixture that is not the root's.
func (r *skillRun) childID() string {
	others := slices.DeleteFunc(must(r.st.ListSessions(context.Background(), "")), func(s store.Session) bool { return s.ID == r.sess.ID })
	return others[0].ID
}

func (r *skillRun) fileHash(rel string) string {
	data, err := os.ReadFile(filepath.Join(r.userSkills, "tdd", filepath.FromSlash(rel)))
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// framed is the skill.load result of a file of the tdd skill as the model receives it; file empty is SKILL.md.
func (r *skillRun) framed(file, text string) string {
	doc := cmp.Or(file, skill.FileName)
	frame := `<<<skill name="tdd" sha256="` + r.fileHash(doc) + `" file="` + file + `">>>` + "\n" +
		tool.SkillStance + "\n" + text + "\n" + skill.CloseFence
	return string(must(json.Marshal(frame)))
}

// registryRecorder records the registry of every loop RunExecLoop runs, keyed by session id.
type registryRecorder struct {
	mu      sync.Mutex
	names   map[string][]string
	onStart func() // runs as each loop starts: after its registry was built, before any turn
}

func recordRegistries() *registryRecorder {
	r := &registryRecorder{names: map[string][]string{}}
	prev := observeExecLoop
	observeExecLoop = func(cfg ExecLoopConfig) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.names[cfg.Session.ID] = cfg.Registry.Names()
		if r.onStart != nil {
			r.onStart()
		}
	}
	DeferCleanup(func() { observeExecLoop = prev })
	return r
}

func (r *registryRecorder) of(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names[id]
}

var _ = Describe("skill.load in a session run (ADR-013 packet 3)", func() {
	var (
		run     *skillRun
		offered SessionExecLoopRequest
	)

	BeforeEach(func() {
		st, sess := execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		setUserConfigDir(GinkgoT(), GinkgoT().TempDir())
		run = &skillRun{st: st, sess: sess, dbPath: filepath.Join(GinkgoT().TempDir(), "skills.db"),
			workdir: GinkgoT().TempDir(), userSkills: must(UserSkillsPath()), rec: recordRegistries()}
		putSkill(run.userSkills, "tdd", tddBody)
		putFile(run.userSkills, "outside.md", "outside\n") // reached only through tdd/../outside.md
		putFile(filepath.Join(run.userSkills, "tdd"), "reference/big.md", strings.Repeat("a", skill.MaxFileBytes+1))
		putFile(filepath.Join(run.userSkills, "tdd"), "reference/empty.md", "")
		putFile(filepath.Join(run.userSkills, "tdd"), "reference/bel.md", "a\x07b\n") // a control other than tab, newline or carriage return
		offered = SessionExecLoopRequest{Allow: []string{tool.SkillLoadName}, SkillsScope: SkillsScopeUser, SkillsDefault: []string{"tdd"}}
	})

	Context("that loads SKILL.md", func() {
		BeforeEach(func() {
			check(run.run(offered, callTurn(skillCall("s1", `{"name":"tdd"}`)), finishText("done")))
		})

		It("offers the tool to the model", func() {
			Expect(toolNames(run.streamer.reqs[0])).To(Equal([]string{tool.SkillLoadName}))
		})

		It("shows the model the fenced body", func() {
			Expect(run.toolMessage("s1")).To(Equal(run.framed("", strings.TrimSpace(tddBody))))
		})

		It("writes skill.loaded after the tool.call and tool.result of its call", func() {
			Expect(run.types()[1:5]).To(Equal([]string{TypeAssistantMessage, tool.TypeToolCall, tool.TypeToolResult, TypeSkillLoaded}))
		})

		It("records the file's hash, root-relative path, bytes and call id for the operator", func() {
			Expect(payloadMap(run.one(TypeSkillLoaded).PayloadJSON)).To(Equal(map[string]any{"name": "tdd", "file": "",
				"sha256": run.fileHash(skill.FileName), "path": "tdd/" + skill.FileName, "bytes": float64(len(skillText("tdd", tddBody))), "tool_call_id": "s1"}))
		})

		It("records the call with the model's own arguments", func() {
			Expect(payloadFieldRaw(run.one(tool.TypeToolCall).PayloadJSON, "args")).To(MatchJSON(`{"name":"tdd"}`))
		})

		It("is ignored by the rebuild of a resumed run", func() {
			without := slices.DeleteFunc(slices.Clone(run.events), func(e store.RecordedEvent) bool { return e.Type == TypeSkillLoaded })
			Expect(rebuildHistory(run.events)).To(Equal(rebuildHistory(without)))
		})

		It("is not a replayed event for compaction", func() {
			Expect(replayedType(run.one(TypeSkillLoaded), newReplayFacts(run.events))).To(BeFalse())
		})

		It("adds nothing to a compaction span", func() {
			Expect(spanEntries(run.ofType(TypeSkillLoaded), run.events)).To(BeEmpty())
		})
	})

	It("never shows the model the skills root when the skill directory vanishes before the load", func() {
		run.rec.onStart = func() { check(os.RemoveAll(filepath.Join(run.userSkills, "tdd"))) }
		check(run.run(offered, callTurn(skillCall("s6", `{"name":"tdd"}`)), finishText("done")))
		Expect(run.toolMessage("s6") + payloadFieldRaw(run.one(tool.TypeToolResult).PayloadJSON, "error")).NotTo(Or(ContainSubstring(run.userSkills), ContainSubstring("/")))
	})

	It("delivers a supporting file below the skill directory, framed with its own name", func() {
		putFile(filepath.Join(run.userSkills, "tdd"), "reference/guide.md", "Read the guide.\n")
		check(run.run(offered, callTurn(skillCall("s3", `{"name":"tdd","file":"reference/guide.md"}`)), finishText("done")))
		Expect(run.toolMessage("s3")).To(Equal(run.framed("reference/guide.md", "Read the guide.\n")))
	})

	It("records a supporting file's root-relative path and name", func() {
		putFile(filepath.Join(run.userSkills, "tdd"), "reference/guide.md", "Read the guide.\n")
		check(run.run(offered, callTurn(skillCall("s3", `{"name":"tdd","file":"reference/guide.md"}`)), finishText("done")))
		Expect(payloadMap(run.one(TypeSkillLoaded).PayloadJSON)["path"]).To(Equal("tdd/reference/guide.md"))
	})

	It("echoes at most 128 runes of a file path it refuses", func() {
		check(run.run(offered, callTurn(skillCall("s10", `{"name":"tdd","file":"`+strings.Repeat("a", 300)+`/.."}`)), finishText("done")))
		Expect(run.toolMessage("s10")).To(ContainSubstring(strings.Repeat("a", 128) + `": a path segment`))
	})

	It("refuses a SKILL.md edited mid-session to another valid name, as discovery would have", func() {
		run.rec.onStart = func() { putAgent(filepath.Join(run.userSkills, "tdd"), skill.FileName, skillText("other", tddBody)) }
		check(run.run(offered, callTurn(skillCall("s7", `{"name":"tdd"}`)), finishText("done")))
		Expect(run.resultCode()).To(Equal(tool.ErrCodeSkillRefused))
	})

	It("writes no skill.loaded when a tool.after hook cannot record its result after the tool.result (deferred: the record follows the loop)", func() {
		sink := &captureSink{}
		gate := &hookGate{after: []Hook{{Point: HookToolAfter, Kind: HookKindDecision}}, runner: tool.HookRunner{}, events: &failingSink{inner: sink, failures: map[string]error{TypeHookResult: os.ErrDeadlineExceeded}}}
		catalog := tool.SkillCatalog{Finder: func(string) (string, error) { return run.userSkills, nil }, Format: skillFormat()}
		loop := must(tool.NewLoop(must(NewSessionToolRegistry(run.workdir, tool.WithOfferedSkills(catalog))), sink, []string{tool.SkillLoadName})).WithGate(gate)
		_, _ = appendResult(context.Background(), sink, loop, skillCall("s8", `{"name":"tdd"}`), nil)
		Expect(sink.types).To(Equal([]string{tool.TypeToolCall, tool.TypeToolResult, TypeHookInvoked}))
	})

	DescribeTable("answers a refused load with its typed code",
		func(args, code string) {
			check(run.run(offered, callTurn(skillCall("s4", args)), finishText("done")))
			Expect(run.resultCode()).To(Equal(code))
		},
		Entry("a file outside the skill directory", `{"name":"tdd","file":"../outside.md"}`, tool.ErrCodeSkillRefused),
		Entry("an oversize supporting file, instead of truncating it", `{"name":"tdd","file":"reference/big.md"}`, tool.ErrCodeSkillOversize),
		Entry("an empty supporting file, instead of recording an empty delivery", `{"name":"tdd","file":"reference/empty.md"}`, tool.ErrCodeSkillNotFound),
		Entry("a supporting file holding a BEL", `{"name":"tdd","file":"reference/bel.md"}`, tool.ErrCodeSkillRefused),
		Entry("a name the run does not offer", `{"name":"other"}`, tool.ErrCodeSkillNotOffered))

	DescribeTable("writes no skill.loaded for a refused load",
		func(args string) {
			check(run.run(offered, callTurn(skillCall("s5", args)), finishText("done")))
			Expect(run.ofType(TypeSkillLoaded)).To(BeEmpty())
		},
		Entry("a file outside the skill directory", `{"name":"tdd","file":"../outside.md"}`),
		Entry("an oversize supporting file", `{"name":"tdd","file":"reference/big.md"}`),
		Entry("a name the run does not offer", `{"name":"other"}`))

	DescribeTable("registers no skill.load when nothing is offered",
		func(names []string) {
			req := offered
			req.Allow, req.SkillsDefault = []string{"echo"}, names
			check(run.run(req, finishText("done")))
			Expect(run.rec.of(run.sess.ID)).NotTo(ContainElement(tool.SkillLoadName))
		},
		Entry("no skills_default", nil),
		Entry("a skills_default naming no loaded skill", []string{"nope"}),
		Entry("a prefix matching no loaded skill", []string{"zz*"}),
		Entry("a loaded skill's file, not its name", []string{"reference"}))

	DescribeTable("offers the skills skills_default names, exactly or by prefix",
		func(names []string) {
			req := offered
			req.SkillsDefault = names
			check(run.run(req, finishText("done")))
			Expect(toolNames(run.streamer.reqs[0])).To(Equal([]string{tool.SkillLoadName}))
		},
		Entry("exact", []string{"tdd"}),
		Entry("prefix", []string{"td*"}),
		Entry("every skill", []string{"*"}))

	DescribeTable("finds a project skill under the project root, never under --workdir",
		func(place func(project string), allow []string, registered bool) {
			project := GinkgoT().TempDir()
			place(project)
			req := offered
			req.Allow, req.SkillsScope, req.ProjectRoot = allow, SkillsScopeProject, project
			check(run.run(req, finishText("done")))
			Expect(slices.Contains(run.rec.of(run.sess.ID), tool.SkillLoadName)).To(Equal(registered))
		},
		Entry("a skill under the project root", func(p string) { putSkill(ProjectSkillsPath(p), "tdd", tddBody) }, []string{tool.SkillLoadName}, true),
		Entry("a skill only under --workdir", func(string) { putSkill(ProjectSkillsPath(run.workdir), "tdd", tddBody) }, []string{"echo"}, false))

	It("answers a call to a tool it was not offered as unknown", func() {
		req := offered
		req.Allow, req.SkillsDefault = []string{"echo"}, nil
		check(run.run(req, callTurn(skillCall("s9", `{"name":"tdd"}`)), finishText("done")))
		Expect(run.toolMessage("s9")).To(Equal("error: " + tool.ErrUnknownTool.Error() + ": " + tool.SkillLoadName))
	})

	It("refuses a skills_default under a scope that was never set, as config_invalid", func() {
		req := offered
		req.SkillsScope = ""
		Expect(run.run(req, finishText("done"))).To(MatchError(ErrConfigInvalid))
	})

	It("writes nothing before refusing the scope", func() {
		req := offered
		req.SkillsScope = ""
		_ = run.run(req, finishText("done"))
		Expect(run.types()).To(Equal([]string{session.TypeTurnStart}))
	})

	DescribeTable("offers skill.load to the root alone, never to its delegated child (D12)",
		func(id func() string, registered bool) {
			putAgent(filepath.Join(run.userSkills, "..", "agents"), "reviewer.md", agentText("reviewer"))
			req := offered
			req.Allow, req.Agents = []string{tool.SkillLoadName, "delegate"}, "user"
			check(run.run(req, callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), finishText("child says hi"), finishText("parent done")))
			Expect(slices.Contains(run.rec.of(id()), tool.SkillLoadName)).To(Equal(registered))
		},
		Entry("the root", func() string { return run.sess.ID }, true),
		Entry("the child", func() string { return run.childID() }, false))

	Describe("the session registry", func() {
		// catalog is any offered catalog: its finder is never called by these specs.
		catalog := func() tool.SkillCatalog {
			return tool.SkillCatalog{Finder: func(string) (string, error) { return run.workdir, nil }, Format: skillFormat()}
		}

		It("registers skill.load beside the builtins when the options offer skills", func() {
			Expect(must(NewSessionToolRegistry(run.workdir, tool.WithOfferedSkills(catalog()))).Names()).To(ContainElement(tool.SkillLoadName))
		})

		It("registers no skill.load without the option", func() {
			Expect(must(NewSessionToolRegistry(run.workdir)).Names()).NotTo(ContainElement(tool.SkillLoadName))
		})

		It("names the cause when skill.load is allowed but nothing is offered", func() {
			_, err := SessionToolDefinitions(must(NewSessionToolRegistry(run.workdir)), []string{tool.SkillLoadName})
			Expect(err).To(MatchError(ContainSubstring("no skill is offered")))
		})

		It("describes skill.load to the model", func() {
			defs := must(SessionToolDefinitions(must(NewSessionToolRegistry(run.workdir, tool.WithOfferedSkills(catalog()))), []string{tool.SkillLoadName}))
			Expect(defs[0].Parameters).To(MatchJSON(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"]}`))
		})
	})
})
