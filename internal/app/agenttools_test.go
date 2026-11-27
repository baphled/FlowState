package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

type catalogSink struct{}

func (catalogSink) Append(context.Context, string, string, any) (int64, error) { return 0, nil }

// mustExecDescription returns the advertised exec.run definition
// description, panicking on setup errors so specs keep a single
// behavioural expectation.
func mustExecDescription(workdir string) string {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecRun(workdir)); err != nil {
		panic(err)
	}
	defs, err := SessionToolDefinitions(registry, []string{tool.ExecRunName})
	if err != nil {
		panic(err)
	}
	return defs[0].Description
}

var _ = Describe("Session tool catalog", func() {
	var registry *tool.Registry
	var workdir string
	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		var err error
		registry, err = NewSessionToolRegistry(workdir)
		Expect(err).NotTo(HaveOccurred())
	})

	It("registers the builtins with exec only under its existing environment opt-in", func() {
		names := []string{"echo", "fs.edit", "fs.read", "fs.write"}
		if tool.ExecEnabled() {
			names = []string{"echo", "exec.run", "fs.edit", "fs.read", "fs.write"}
		}
		Expect(registry.Names()).To(Equal(names))
	})

	It("advertises nothing without explicit permission", func() {
		definitions, err := SessionToolDefinitions(registry, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(definitions).To(BeEmpty())
	})

	It("advertises only the requested registered builtins in allowlist order", func() {
		allow := []string{"fs.write", "echo"}
		definitions, err := SessionToolDefinitions(registry, allow)
		Expect(err).NotTo(HaveOccurred())
		names := []string{}
		for _, definition := range definitions {
			names = append(names, definition.Name)
		}
		Expect(names).To(Equal(allow))
	})

	It("rejects a missing registry", func() {
		_, err := SessionToolDefinitions(nil, []string{"echo"})
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects invalid allow entries", func(allow []string) {
		_, err := SessionToolDefinitions(registry, allow)
		Expect(err).To(HaveOccurred())
	}, Entry("unknown name", []string{"missing"}), Entry("empty name", []string{""}),
		Entry("duplicate name", []string{"echo", "echo"}))

	It("never returns a partially admitted list", func() {
		definitions, _ := SessionToolDefinitions(registry, []string{"echo", "missing"})
		Expect(definitions).To(BeNil())
	})

	It("does not register exec to satisfy an allow entry", func() {
		_, err := SessionToolDefinitions(tool.NewRegistry(), []string{"exec.run"})
		Expect(err).To(MatchError(ContainSubstring("unknown tool")))
	})

	It("advertises the deny-by-default policy in the exec definition description", func() {
		Expect(mustExecDescription(GinkgoT().TempDir())).To(ContainSubstring("deny-by-default"))
	})
	It("names git in the exec definition description", func() {
		Expect(mustExecDescription(GinkgoT().TempDir())).To(ContainSubstring("git"))
	})
	It("renders the exec definition description deterministically", func() {
		wd := GinkgoT().TempDir()
		Expect(mustExecDescription(wd)).To(Equal(mustExecDescription(wd)))
	})

	DescribeTable("rejects implementations without a qualified builtin schema", func(name string) {
		custom := tool.NewRegistry()
		err := custom.Register(&fakeTool{name: name})
		Expect(err).NotTo(HaveOccurred())
		_, err = SessionToolDefinitions(custom, []string{name})
		Expect(err).To(HaveOccurred())
	}, Entry("custom name", "custom"), Entry("spoofed builtin name", "fs.read"))

	It("rejects a typed-nil builtin accepted by the registry", func() {
		nilRead := reflect.Zero(reflect.TypeOf(tool.NewFSRead(workdir))).Interface().(tool.Tool)
		custom := tool.NewRegistry()
		err := custom.Register(nilRead)
		Expect(err).NotTo(HaveOccurred())
		_, err = SessionToolDefinitions(custom, []string{"fs.read"})
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("describes the actual object arguments", func(name, expected string) {
		// Register exec directly for metadata inspection without enabling execution.
		if name == tool.ExecRunName {
			registry = tool.NewRegistry()
			err := registry.Register(tool.NewExecRun(workdir))
			Expect(err).NotTo(HaveOccurred())
		}
		definitions, err := SessionToolDefinitions(registry, []string{name})
		Expect(err).NotTo(HaveOccurred())
		Expect(definitions[0].Parameters).To(MatchJSON(expected))
	},
		Entry("echo", "echo", `{"type":"object","additionalProperties":true}`),
		Entry("read", "fs.read", `{"type":"object","properties":{"path":{"type":"string","minLength":1}},"required":["path"]}`),
		Entry("write", "fs.write", `{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string","default":""}},"required":["path"]}`),
		Entry("edit", "fs.edit", `{"type":"object","properties":{"path":{"type":"string","minLength":1},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string","default":""},"all":{"type":"boolean","default":false}},"required":["path","old_text"]}`),
		Entry("exec", "exec.run", `{"type":"object","properties":{"cmd":{"type":"string","minLength":1},"args":{"type":"array","items":{"type":"string"}}},"required":["cmd"]}`))

	It("returns independently owned schema bytes", func() {
		first, err := SessionToolDefinitions(registry, []string{"echo"})
		Expect(err).NotTo(HaveOccurred())
		first[0].Parameters[0] = '!'
		next, err := SessionToolDefinitions(registry, []string{"echo"})
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Valid(next[0].Parameters)).To(BeTrue())
	})

	DescribeTable("uses the advertised filesystem argument fields", func(name, args, expected string) {
		err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello hello"), 0o600)
		Expect(err).NotTo(HaveOccurred())
		implementation, err := registry.Get(name)
		Expect(err).NotTo(HaveOccurred())
		_, err = implementation.Invoke(context.Background(), json.RawMessage(args))
		Expect(err).NotTo(HaveOccurred())
		content, err := os.ReadFile(filepath.Join(workdir, "note.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(expected))
	}, Entry("write content", "fs.write", `{"path":"note.txt","content":"written"}`, "written"),
		Entry("write omitted content", "fs.write", `{"path":"note.txt"}`, ""),
		Entry("edit all matches", "fs.edit", `{"path":"note.txt","old_text":"hello","new_text":"hi","all":true}`, "hi hi"),
		Entry("edit omitted replacement", "fs.edit", `{"path":"note.txt","old_text":"hello hello"}`, ""))

	It("reads the file selected by the advertised path field", func() {
		err := os.WriteFile(filepath.Join(workdir, "note.txt"), []byte("hello"), 0o600)
		Expect(err).NotTo(HaveOccurred())
		implementation, err := registry.Get("fs.read")
		Expect(err).NotTo(HaveOccurred())
		result, err := implementation.Invoke(context.Background(), json.RawMessage(`{"path":"note.txt"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(map[string]any{"path": "note.txt", "content": "hello", "bytes": 5}))
	})

	DescribeTable("preserves the existing session workdir selection", func(explicit bool) {
		dbPath := filepath.Join(workdir, "events.db")
		req := ToolRequest{SessionID: "session-one"}
		expected := filepath.Join(SessionWorkdirRoot(dbPath), req.SessionID, "note.txt")
		if explicit {
			req.Workdir = filepath.Join(workdir, "override")
			expected = filepath.Join(req.Workdir, "note.txt")
		}
		err := os.MkdirAll(filepath.Dir(expected), 0o750)
		Expect(err).NotTo(HaveOccurred())
		loop, err := newSessionToolLoop(catalogSink{}, dbPath, req, []string{"fs.write"})
		Expect(err).NotTo(HaveOccurred())
		_, err = loop.Invoke(context.Background(), "fs.write", json.RawMessage(`{"path":"note.txt","content":"scoped"}`))
		Expect(err).NotTo(HaveOccurred())
		content, err := os.ReadFile(expected)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("scoped"))
	}, Entry("default", false), Entry("explicit", true))
})

// protectedResults is the tool.result events of the session whose error code is fs_path_protected.
func protectedResults(st *store.Store, id string) []store.RecordedEvent {
	var out []store.RecordedEvent
	for _, e := range must(st.Replay(context.Background(), id)) {
		if e.Type == tool.TypeToolResult && strings.Contains(e.PayloadJSON, `"`+tool.ErrCodePathProtected+`"`) {
			out = append(out, e)
		}
	}
	return out
}

// setUserConfigDir points os.UserConfigDir at dir through the variable it reads on this OS (the stdlib's own switch):
// %AppData% on Windows, $HOME on Darwin and $XDG_CONFIG_HOME elsewhere. XDG_CONFIG_HOME alone fakes nothing on
// Windows, where the user hook file is then read from the real %AppData%.
func setUserConfigDir(t GinkgoTInterface, dir string) {
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", dir)
	case "darwin", "ios":
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

// userHooksUnder points the OS user config directory at <workdir>/cfg (setUserConfigDir) and returns the user hook
// file's path relative to workdir: the file the default --hooks scope loads next, which a spec can have the model
// write from that workdir.
func userHooksUnder(workdir string) string {
	cfg := filepath.Join(workdir, "cfg")
	setUserConfigDir(GinkgoT(), cfg)
	return must(filepath.Rel(workdir, must(UserHooksPath())))
}

// writeCallTo is an fs.write call of "x" at path.
func writeCallTo(id, path string) provider.ToolCall {
	args := must(json.Marshal(map[string]string{"path": path, "content": "x"}))
	return provider.ToolCall{ID: id, Name: tool.FSWriteName, Arguments: args}
}

var _ = Describe("The protected hook trees of a session registry (ADR-012 D20)", func() {
	var (
		home string
		user string
		root string
	)
	// call is one filesystem tool invocation through a registry built over home with dirs protected.
	call := func(dirs []string, name, args string) error {
		registry := must(NewSessionToolRegistry(home, tool.WithProtectedDirs(dirs...)))
		implementation, err := registry.Get(name)
		if err != nil {
			panic(err)
		}
		_, err = implementation.Invoke(context.Background(), json.RawMessage(args))
		return err
	}
	const editArgs = `,"old_text":"o","new_text":"n"`

	BeforeEach(func() {
		home = GinkgoT().TempDir()
		user = userHooksUnder(home)
		root = GinkgoT().TempDir()
	})

	It("are the directories the hook loader reads its two hook files from", func() {
		var origins []string
		for _, f := range []struct{ dir, id string }{{filepath.Join(home, filepath.Dir(user)), "user-guard"}, {filepath.Join(root, ".flowstate"), "project-guard"}} {
			putHookMode(f.dir, "guard.sh", "#!/bin/sh\nexit 0\n", 0o700)
			putHookFile(f.dir, "hooks.json", hookFileWith(`{"id":"`+f.id+`","point":"tool.before","kind":"command","argv":["./guard.sh"]}`))
		}
		loaded, err := LoadHooks(HooksRequest{Scope: HookScopeBoth, ProjectRoot: root, Accepted: AcceptedHooks()})
		check(err)
		for _, h := range loaded.Hooks {
			origins = append(origins, filepath.Dir(h.Origin))
		}
		Expect(ProtectedHookDirs(root)).To(ConsistOf(origins))
	})

	DescribeTable("refuses a write into the user hook directory, which has no dot-name",
		func(name, extra string) {
			Expect(call(ProtectedHookDirs(root), name, fmt.Sprintf(`{"path":%q%s}`, user, extra))).To(MatchError(tool.ErrPathProtected))
		},
		Entry("fs.write", tool.FSWriteName, ""),
		Entry("fs.edit", tool.FSEditName, editArgs))

	DescribeTable("refuses a write under a .flowstate component",
		func(name, extra string) {
			Expect(call(ProtectedHookDirs(root), name, `{"path":"repo/.flowstate/hooks.json"`+extra+`}`)).To(MatchError(tool.ErrPathProtected))
		},
		Entry("fs.write", tool.FSWriteName, ""),
		Entry("fs.edit", tool.FSEditName, editArgs))

	It("creates no user hook directory for a refused write", func() {
		_ = call(ProtectedHookDirs(root), tool.FSWriteName, fmt.Sprintf(`{"path":%q,"content":"x"}`, user))
		Expect(filepath.Join(home, filepath.Dir(user))).NotTo(BeADirectory())
	})

	It("still writes an ordinary path", func() {
		Expect(call(ProtectedHookDirs(root), tool.FSWriteName, `{"path":"note.txt","content":"x"}`)).To(Succeed())
	})

	It("still reads a hook file", func() {
		putHookFile(filepath.Join(home, ".flowstate"), "hooks.json", hookFileWith())
		Expect(call(ProtectedHookDirs(root), tool.FSReadName, `{"path":".flowstate/hooks.json"}`)).To(Succeed())
	})
})

var _ = Describe("The skill.load definition over an empty offer (ADR-013 D6)", func() {
	// catalog is a catalog marked None as given, as the composition marks a session offered nothing: none offers
	// nothing, so a profile that lists the tool keeps it and every name is refused.
	catalog := func(none bool) tool.SkillCatalog {
		finder := func(string) (string, error) { return "", fmt.Errorf("not offered") }
		if !none {
			finder = func(string) (string, error) { return GinkgoT().TempDir(), nil }
		}
		return tool.SkillCatalog{Finder: finder, Format: skillFormat(), None: none}
	}

	DescribeTable("tells the model whether a load can succeed",
		func(none bool, want string) {
			defs := must(SessionToolDefinitions(must(NewSessionToolRegistry("", tool.WithOfferedSkills(catalog(none)))), []string{tool.SkillLoadName}))
			Expect(defs[0].Description).To(Equal(want))
		},
		Entry("no skill offered", true, tool.SkillNoneDescription),
		Entry("a skill offered: the builtin's description, byte for byte", false,
			"Load a skill the session offers: its SKILL.md, or a supporting file below its directory given as a /-separated relative path. The text is third-party guidance."))

	It("keeps the builtin's schema over an empty offer", func() {
		defs := must(SessionToolDefinitions(must(NewSessionToolRegistry("", tool.WithOfferedSkills(catalog(true)))), []string{tool.SkillLoadName}))
		Expect(defs[0].Parameters).To(MatchJSON(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"]}`))
	})
})
