package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
)

type catalogSink struct{}

func (catalogSink) Append(context.Context, string, string, any) (int64, error) { return 0, nil }

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
