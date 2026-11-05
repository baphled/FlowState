package tool

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// newFSLoop registers one tool over a temp session.
func newFSLoop(t Tool, allow []string) (*Loop, *replayStore) {
	st, sid, w := newStoreWithSession()
	r := NewRegistry()
	must(r.Register(t))
	l, err := NewLoop(r, w, allow)
	must(err)
	return l, &replayStore{st: st, sid: sid}
}

type replayStore struct {
	st  *store.Store
	sid string
}

func (r *replayStore) events() ([]store.RecordedEvent, error) {
	return r.st.Replay(context.Background(), r.sid)
}

func write(workdir, rel, content string) {
	must(os.MkdirAll(filepath.Dir(filepath.Join(workdir, rel)), 0o755))
	must(os.WriteFile(filepath.Join(workdir, rel), []byte(content), 0o644))
}

func read(workdir, rel string) string {
	b, err := os.ReadFile(filepath.Join(workdir, rel))
	must(err)
	return string(b)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func invoke(l *Loop, name, args string) (any, error) {
	return l.Invoke(context.Background(), name, json.RawMessage(args))
}

// lastResultCode returns the latest tool.result error.code.
func lastResultCode(rs *replayStore) string {
	evs, err := rs.events()
	if err != nil {
		panic(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type != TypeToolResult {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(evs[i].PayloadJSON), &p) != nil {
			return ""
		}
		e, _ := p["error"].(map[string]any)
		c, _ := e["code"].(string)
		return c
	}
	return ""
}

var _ = Describe("fs.read", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		write(workdir, "a.txt", "hello")
		write(workdir, "sub/b.txt", "deep")
		l, _ = newFSLoop(NewFSRead(workdir), []string{FSReadName})
	})
	It("reads a file under the workdir", func() {
		res, _ := invoke(l, FSReadName, `{"path":"a.txt"}`)
		Expect(res.(map[string]any)["content"]).To(Equal("hello"))
	})
	It("reads a nested file with a cleaned relative path", func() {
		res, _ := invoke(l, FSReadName, `{"path":"./sub/../sub/b.txt"}`)
		Expect(res.(map[string]any)["content"]).To(Equal("deep"))
	})
	It("rejects traversal", func() {
		_, err := invoke(l, FSReadName, `{"path":"../escape.txt"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for a host absolute path", func() {
		abs := filepath.Join(GinkgoT().TempDir(), "passwd")
		if runtime.GOOS == "windows" {
			abs = `C:\Windows\system.ini`
		}
		_, err := invoke(l, FSReadName, `{"path":`+strconv.Quote(abs)+`}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for an empty path", func() {
		_, err := invoke(l, FSReadName, `{"path":""}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrFileNotFound for a missing file", func() {
		_, err := invoke(l, FSReadName, `{"path":"nope.txt"}`)
		Expect(errors.Is(err, ErrFileNotFound)).To(BeTrue())
	})
	It("returns ErrOversize above the read cap", func() {
		write(workdir, "big.txt", "too large")
		_, err := NewFSRead(workdir, WithMaxReadBytes(3)).Invoke(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
	})
	It("rejects malformed argument JSON", func() {
		_, err := invoke(l, FSReadName, `not-json`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("fs.write", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		l, _ = newFSLoop(NewFSWrite(workdir), []string{FSWriteName})
	})
	It("overwrites an existing file", func() {
		write(workdir, "x.txt", "old")
		_, _ = invoke(l, FSWriteName, `{"path":"x.txt","content":"new"}`)
		Expect(read(workdir, "x.txt")).To(Equal("new"))
	})
	It("creates parent directories implicitly", func() {
		_, _ = invoke(l, FSWriteName, `{"path":"d1/d2/f.txt","content":"x"}`)
		Expect(read(workdir, "d1/d2/f.txt")).To(Equal("x"))
	})
	It("reads back a written file", func() {
		_, _ = invoke(l, FSWriteName, `{"path":"new.txt","content":"data"}`)
		Expect(read(workdir, "new.txt")).To(Equal("data"))
	})
	It("returns ErrPathOutsideWorkdir for traversal", func() {
		_, err := invoke(l, FSWriteName, `{"path":"../out.txt","content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for a host absolute path", func() {
		abs := filepath.Join(GinkgoT().TempDir(), "out.txt")
		if runtime.GOOS == "windows" {
			abs = `C:\Windows\out.txt`
		}
		_, err := invoke(l, FSWriteName, `{"path":`+strconv.Quote(abs)+`,"content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("rejects oversize writes with ErrOversize", func() {
		_, err := NewFSWrite(workdir, WithMaxWriteBytes(4)).Invoke(context.Background(), json.RawMessage(`{"path":"big.txt","content":"too large"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
	})
	It("does not create the file on an oversize write", func() {
		_, _ = NewFSWrite(workdir, WithMaxWriteBytes(4)).Invoke(context.Background(), json.RawMessage(`{"path":"big.txt","content":"too large"}`))
		_, serr := os.Stat(filepath.Join(workdir, "big.txt"))
		Expect(os.IsNotExist(serr)).To(BeTrue())
	})
	It("refuses a symlink escaping the workdir", func() {
		out := GinkgoT().TempDir()
		must(os.Symlink(out, filepath.Join(workdir, "link")))
		_, err := invoke(l, FSWriteName, `{"path":"link/esc.txt","content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
})

var _ = Describe("Loop typed fs error codes", func() {
	It("maps ErrPathOutsideWorkdir to its code", func() {
		l, rs := newFSLoop(NewFSRead(GinkgoT().TempDir()), []string{FSReadName})
		_, _ = invoke(l, FSReadName, `{"path":"../x"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodePathOutsideWorkdir))
	})
	It("maps ErrFileNotFound to its code", func() {
		l, rs := newFSLoop(NewFSRead(GinkgoT().TempDir()), []string{FSReadName})
		_, _ = invoke(l, FSReadName, `{"path":"missing.txt"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeNotFound))
	})
	It("maps ErrOversize to its code", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "big.txt", "0123456789")
		l, rs := newFSLoop(NewFSRead(workdir, WithMaxReadBytes(4)), []string{FSReadName})
		_, _ = invoke(l, FSReadName, `{"path":"big.txt"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeOversize))
	})
})

// importLintViolations reports forbidden internal/app or cmd/* imports
// in internal/tool production files (leaf-port architecture).
func importLintViolations() []string {
	dir, err := os.Getwd()
	if err != nil {
		return []string{err.Error()}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	var violations []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if perr != nil {
			violations = append(violations, perr.Error())
			continue
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, "github.com/baphled/flowstate/internal/app") ||
				strings.HasPrefix(path, "github.com/baphled/flowstate/cmd") {
				violations = append(violations, e.Name()+" imports "+path)
			}
		}
	}
	return violations
}

// internal/tool must stay a leaf port — no internal/app or cmd/* imports.
var _ = Describe("import lint", func() {
	It("internal/tool imports neither internal/app nor cmd packages", func() {
		Expect(importLintViolations()).To(BeEmpty())
	})
})
