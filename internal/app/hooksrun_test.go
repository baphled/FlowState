//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

var _ = Describe("The run-level hooks of a root run (ADR-012 packet 4a)", func() {
	var (
		dir  string
		root string
		st   *store.Store
		sess *store.Session
		ctx  context.Context
		req  RootRunHooksRequest
	)

	// project installs the project hook file and a script per hook: an id, a point and a script body.
	project := func(hooks map[string]string, points map[string]string, order ...string) {
		hookDir := filepath.Join(root, ".flowstate")
		specs := []string{}
		for _, id := range order {
			putHookMode(hookDir, id+".sh", "#!/bin/sh\n"+hooks[id]+"\n", 0o700)
			specs = append(specs, `{"id":"`+id+`","point":"`+points[id]+`","kind":"command","argv":["./`+id+`.sh"]}`)
		}
		putHookFile(hookDir, "hooks.json", hookFileWith(specs...))
	}
	// one installs a single project hook at a point.
	one := func(id, point, body string) {
		project(map[string]string{id: body}, map[string]string{id: point}, id)
	}
	begin := func() (*RootRunHooks, error) { return BeginRootRunHooks(ctx, st, sess, req) }
	types := func() []string { return gateTypes(gateEvents(st, sess.ID)) }
	// afterTask is the event types written after the turn.start the session was created with.
	afterTask := func() []string { return types()[1:] }

	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
		root = GinkgoT().TempDir()
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		lease, _ := acquireTestLease(st, "exec:"+sess.ID, "cli")
		req = RootRunHooksRequest{Scope: HookScopeProject, ProjectRoot: root, Workdir: dir, Lease: lease}
	})

	Describe("a user.prompt hook that allows", func() {
		BeforeEach(func() {
			one("watch", "user.prompt", "cat > "+filepath.Join(dir, "stdin.json"))
			if _, err := begin(); err != nil {
				panic(err)
			}
		})
		It("is recorded after hooks.loaded and before anything else", func() {
			Expect(afterTask()).To(Equal([]string{TypeHooksLoaded, TypeHookInvoked, TypeHookResult}))
		})
		It("reads the run's stored task as the prompt", func() {
			var doc map[string]any
			data, err := os.ReadFile(filepath.Join(dir, "stdin.json"))
			if err != nil {
				panic(err)
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				panic(err)
			}
			Expect(doc["prompt"]).To(Equal("do the thing"))
		})
		It("records the hook without a tool call id", func() {
			invoked := gateOfType(gateEvents(st, sess.ID), TypeHookInvoked)[0]
			Expect(invoked.PayloadJSON).NotTo(ContainSubstring("tool_call_id"))
		})
	})

	Describe("a user.prompt hook that refuses", func() {
		var err error
		BeforeEach(func() {
			one("gate", "user.prompt", "echo no >&2\nexit 2")
			_, err = begin()
		})
		It("refuses the run with hook_refused", func() { Expect(err).To(MatchError(ErrHookRefused)) })
		It("reports the code hook_refused", func() { Expect(ErrorCode(err)).To(Equal(CodeHookRefused)) })
		It("names the hook", func() { Expect(err).To(MatchError(ContainSubstring("gate"))) })
		It("writes only hooks.loaded, hook.invoked and hook.result", func() {
			Expect(afterTask()).To(Equal([]string{TypeHooksLoaded, TypeHookInvoked, TypeHookResult}))
		})
	})

	It("refuses a run whose user.prompt hook fails, as the fail-closed default says", func() {
		one("broken", "user.prompt", "exit 1")
		_, err := begin()
		Expect(err).To(MatchError(ErrHookRefused))
	})

	It("lets a run through when a failing user.prompt hook is advisory", func() {
		hookDir := filepath.Join(root, ".flowstate")
		putHookMode(hookDir, "adv.sh", "#!/bin/sh\nexit 1\n", 0o700)
		putHookFile(hookDir, "hooks.json", hookFileWith(
			`{"id":"adv","point":"user.prompt","kind":"command","argv":["./adv.sh"],"on_failure":"allow"}`))
		hk, err := begin()
		if err != nil {
			panic(err)
		}
		Expect(hk).NotTo(BeNil())
	})

	It("fails a user.prompt hook that returns context, which no point accepts yet", func() {
		one("ctx", "user.prompt", `echo '{"context":"hi"}'`)
		_, err := begin()
		Expect(err).To(MatchError(ErrHookRefused))
	})

	It("records the context_unsupported failure of that hook", func() {
		one("ctx", "user.prompt", `echo '{"context":"hi"}'`)
		_, _ = begin()
		result := gateOfType(gateEvents(st, sess.ID), TypeHookResult)[0]
		Expect(payloadFieldText(result.PayloadJSON, "failure")).To(Equal("context_unsupported"))
	})

	Describe("a refused run on a paused session", func() {
		BeforeEach(func() {
			w, err := st.NewEventWriter(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			if err := w.Pause(ctx, store.PauseInfo{Reason: "budget_exhausted"}); err != nil {
				panic(err)
			}
			_ = w.Close()
			one("gate", "user.prompt", "exit 2")
			_, _ = begin()
		})
		It("leaves the session paused", func() {
			got, err := st.GetSession(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			Expect(got.Status).To(Equal("paused"))
		})
		It("writes no session.resumed", func() {
			Expect(gateOfType(gateEvents(st, sess.ID), "session.resumed")).To(BeEmpty())
		})
		It("writes no user.message", func() {
			Expect(gateOfType(gateEvents(st, sess.ID), TypeUserMessage)).To(BeEmpty())
		})
	})

	It("takes hook events on a paused session, which the writer does not refuse", func() {
		w, err := st.NewEventWriter(ctx, sess.ID)
		if err != nil {
			panic(err)
		}
		if err := w.Pause(ctx, store.PauseInfo{Reason: "budget_exhausted"}); err != nil {
			panic(err)
		}
		_ = w.Close()
		one("watch", "user.prompt", "exit 0")
		_, _ = begin()
		Expect(gateOfType(gateEvents(st, sess.ID), TypeHookResult)).To(HaveLen(1))
	})

	It("fires user.prompt again on a second run with the stored task", func() {
		log := filepath.Join(dir, "stdin.log")
		one("watch", "user.prompt", "cat >> "+log)
		for range 2 {
			if _, err := begin(); err != nil {
				panic(err)
			}
		}
		data, err := os.ReadFile(log)
		if err != nil {
			panic(err)
		}
		Expect(strings.Count(string(data), `"prompt":"do the thing"`)).To(Equal(2))
	})

	Describe("a run with no scope", func() {
		BeforeEach(func() {
			one("gate", "user.prompt", "exit 2")
			req.Scope = ""
		})
		It("loads nothing and refuses nothing", func() {
			hk, err := begin()
			if err != nil {
				panic(err)
			}
			Expect(hk.Set()).To(BeNil())
		})
		It("writes no hooks.loaded", func() {
			_, _ = begin()
			Expect(types()).To(Equal([]string{session.TypeTurnStart}))
		})
	})

	Describe("hooks.loaded on the text-only path", func() {
		var loaded store.RecordedEvent
		BeforeEach(func() {
			req.TextOnly = true
			project(map[string]string{"guard": "exit 0", "greet": "exit 0"},
				map[string]string{"guard": "tool.before", "greet": "user.prompt"}, "guard", "greet")
			if _, err := begin(); err != nil {
				panic(err)
			}
			loaded = gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded)[0]
		})
		It("lists a hook on a tool point as inert", func() {
			Expect(payloadFieldRaw(loaded.PayloadJSON, "inert")).To(MatchJSON(`["guard"]`))
		})
		It("lists every hook of the set", func() {
			Expect(payloadFieldRaw(loaded.PayloadJSON, "ids")).To(MatchJSON(`["guard","greet"]`))
		})
	})

	It("lists no hook as inert on the loop path", func() {
		project(map[string]string{"guard": "exit 0"}, map[string]string{"guard": "tool.before"}, "guard")
		_, _ = begin()
		loaded := gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded)[0]
		Expect(payloadFieldRaw(loaded.PayloadJSON, "inert")).To(MatchJSON(`[]`))
	})

	It("hands the loop the set it recorded, so the loop records nothing again", func() {
		one("watch", "user.prompt", "exit 0")
		hk, err := begin()
		if err != nil {
			panic(err)
		}
		loaded := gateOfType(gateEvents(st, sess.ID), TypeHooksLoaded)[0]
		Expect(payloadFieldText(loaded.PayloadJSON, "set_sha256")).To(Equal(hk.Set().Digest()))
	})

	Describe("session.start and session.end around a run", func() {
		var hk *RootRunHooks
		var ran int
		var runErr error
		marker := func() (*TurnResult, error) {
			ran++
			w, err := st.NewEventWriter(ctx, sess.ID)
			if err != nil {
				panic(err)
			}
			defer func() { _ = w.Close() }()
			if _, err := w.Append(ctx, session.ActorEngine, "run.marker", map[string]any{}); err != nil {
				panic(err)
			}
			return nil, runErr
		}
		points := func() []string {
			out := []string{}
			for _, e := range gateEvents(st, sess.ID) {
				switch e.Type {
				case TypeHookInvoked:
					out = append(out, payloadFieldText(e.PayloadJSON, "point"))
				case "run.marker":
					out = append(out, "run")
				}
			}
			return out
		}
		BeforeEach(func() {
			ran, runErr = 0, nil
			project(map[string]string{"s": "exit 0", "e": "exit 0"},
				map[string]string{"s": "session.start", "e": "session.end"}, "s", "e")
			var err error
			if hk, err = begin(); err != nil {
				panic(err)
			}
		})
		It("fires session.start before the run and session.end after it", func() {
			_, _ = hk.Around(ctx, marker)
			Expect(points()).To(Equal([]string{"session.start", "run", "session.end"}))
		})
		It("fires session.end when the run fails", func() {
			runErr = ErrBudgetExceeded
			_, _ = hk.Around(ctx, marker)
			Expect(points()).To(Equal([]string{"session.start", "run", "session.end"}))
		})
		It("returns the run's own error", func() {
			runErr = ErrBudgetExceeded
			_, err := hk.Around(ctx, marker)
			Expect(err).To(MatchError(ErrBudgetExceeded))
		})
		It("fires neither point until the run is wrapped", func() {
			Expect(points()).To(BeEmpty())
		})
	})

	It("ignores a deny from a session.start hook and still runs", func() {
		one("s", "session.start", "echo no >&2\nexit 2")
		hk, err := begin()
		if err != nil {
			panic(err)
		}
		ran := false
		if _, rerr := hk.Around(ctx, func() (*TurnResult, error) { ran = true; return nil, nil }); rerr != nil {
			panic(rerr)
		}
		Expect(ran).To(BeTrue())
	})

	It("runs the later session.start hooks after one that denies", func() {
		project(map[string]string{"a": "exit 2", "b": "exit 0"},
			map[string]string{"a": "session.start", "b": "session.start"}, "a", "b")
		hk, err := begin()
		if err != nil {
			panic(err)
		}
		if _, err := hk.Around(ctx, func() (*TurnResult, error) { return nil, nil }); err != nil {
			panic(err)
		}
		Expect(gateOfType(gateEvents(st, sess.ID), TypeHookInvoked)).To(HaveLen(2))
	})

	It("fires no session.start or session.end for a refused run", func() {
		project(map[string]string{"g": "exit 2", "e": "exit 0"},
			map[string]string{"g": "user.prompt", "e": "session.end"}, "g", "e")
		_, _ = begin()
		Expect(gateOfType(gateEvents(st, sess.ID), TypeHookInvoked)).To(HaveLen(1))
	})

	It("runs a nil value's run without hooks", func() {
		var hk *RootRunHooks
		ran := false
		if _, err := hk.Around(ctx, func() (*TurnResult, error) { ran = true; return nil, nil }); err != nil {
			panic(err)
		}
		Expect(ran).To(BeTrue())
	})
})
