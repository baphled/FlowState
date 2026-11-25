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

// rootLinkSpecs pins the symlink confinement of the file tools: every path is
// resolved physically inside an os.Root, so ".." after a link and dangling links
// cannot leave the workdir.
func rootLinkSpecs() {
	var outer, workdir string
	link := func(target, name string) {
		must(os.MkdirAll(filepath.Dir(filepath.Join(workdir, name)), 0o755))
		must(os.Symlink(target, filepath.Join(workdir, name)))
	}
	writeVia := func(path string) error {
		_, err := NewFSWrite(workdir).Invoke(context.Background(),
			json.RawMessage(`{"path":`+strconv.Quote(path)+`,"content":"pwn"}`))
		return err
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	BeforeEach(func() {
		outer = GinkgoT().TempDir()
		workdir = filepath.Join(outer, "w")
		must(os.Mkdir(workdir, 0o755))
	})
	It("refuses the three-link layout whose .. follows a link", func() {
		must(os.Mkdir(filepath.Join(workdir, "a"), 0o755))
		link("../../a", "p/q/s")
		link("p/q/s/../../esc", "dl")
		Expect(errors.Is(writeVia("dl"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("creates nothing outside the workdir for the three-link layout", func() {
		must(os.Mkdir(filepath.Join(workdir, "a"), 0o755))
		link("../../a", "p/q/s")
		link("p/q/s/../../esc", "dl")
		_ = writeVia("dl")
		Expect(exists(filepath.Join(outer, "esc"))).To(BeFalse())
	})
	It("applies .. after a link to the physical directory, inside the workdir", func() {
		must(os.MkdirAll(filepath.Join(workdir, "out", "deep"), 0o755))
		link("out/deep", "s")
		link("s/../newfile", "dl")
		must(writeVia("dl"))
		Expect(read(workdir, "out/newfile")).To(Equal("pwn"))
	})
	It("leaves no file at the lexical place for the physical .. variant", func() {
		must(os.MkdirAll(filepath.Join(workdir, "out", "deep"), 0o755))
		link("out/deep", "s")
		link("s/../newfile", "dl")
		must(writeVia("dl"))
		Expect(exists(filepath.Join(workdir, "newfile"))).To(BeFalse())
	})
	It("refuses a dangling link to a missing absolute target outside", func() {
		link(filepath.Join(outer, "created"), "dl")
		Expect(errors.Is(writeVia("dl"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("creates nothing outside through a dangling link", func() {
		link(filepath.Join(outer, "created"), "dl")
		_ = writeVia("dl")
		Expect(exists(filepath.Join(outer, "created"))).To(BeFalse())
	})
	It("refuses a dangling relative link escaping with ..", func() {
		link("../created", "dl")
		Expect(errors.Is(writeVia("dl"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("refuses a dangling link in the middle of the path", func() {
		link(filepath.Join(outer, "missing"), "dl")
		Expect(errors.Is(writeVia("dl/x"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("refuses a chain of links ending outside", func() {
		link(filepath.Join(outer, "created"), "b")
		link("b", "a")
		Expect(errors.Is(writeVia("a"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("refuses a link to an existing file outside", func() {
		must(os.WriteFile(filepath.Join(outer, "f"), []byte("x"), 0o644))
		link(filepath.Join(outer, "f"), "fl")
		Expect(errors.Is(writeVia("fl"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("refuses a new file under a link to a directory outside", func() {
		link(outer, "d")
		Expect(errors.Is(writeVia("d/new"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("fails a link loop without hanging", func() {
		link("b", "a")
		link("a", "b")
		Expect(writeVia("a")).To(HaveOccurred())
	})
	It("creates the target inside for a dangling link that stays inside", func() {
		link("inside.txt", "dl")
		must(writeVia("dl"))
		Expect(read(workdir, "inside.txt")).To(Equal("pwn"))
	})
	It("refuses a .. in the middle of a path that leaves the workdir", func() {
		Expect(errors.Is(writeVia("a/../../x"), ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("creates the missing parent of a path whose .. follows a link", func() {
		must(os.MkdirAll(filepath.Join(workdir, "out", "deep"), 0o755))
		link("out/deep", "s")
		must(writeVia("s/../sub/f.txt"))
		Expect(read(workdir, "out/sub/f.txt")).To(Equal("pwn"))
	})
	It("allows a .. in the middle of a path that stays inside", func() {
		must(os.Mkdir(filepath.Join(workdir, "a"), 0o755))
		must(writeVia("a/../x.txt"))
		Expect(read(workdir, "x.txt")).To(Equal("pwn"))
	})
	It("reads through a symlink that stays inside", func() {
		write(workdir, "real/f.txt", "inner")
		link("real", "alias")
		res, err := NewFSRead(workdir).Invoke(context.Background(), json.RawMessage(`{"path":"alias/f.txt"}`))
		must(err)
		Expect(res.(map[string]any)["content"]).To(Equal("inner"))
	})
	It("refuses to read through a symlink to a file outside", func() {
		must(os.WriteFile(filepath.Join(outer, "secret"), []byte("s"), 0o644))
		link(filepath.Join(outer, "secret"), "sl")
		_, err := NewFSRead(workdir).Invoke(context.Background(), json.RawMessage(`{"path":"sl"}`))
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	editVia := func(path string) {
		_, err := NewFSEdit(workdir).Invoke(context.Background(),
			json.RawMessage(`{"path":`+strconv.Quote(path)+`,"old_text":"old","new_text":"NEW"}`))
		must(err)
	}
	linkBeyondDotDot := func() {
		must(os.MkdirAll(filepath.Join(workdir, "a", "b", "c"), 0o755))
		write(workdir, "a/f", "old")
		must(os.WriteFile(filepath.Join(outer, "f"), []byte("old"), 0o644))
		link("a/b/c", "l")
	}
	It("edits the physical file when .. follows a link", func() {
		linkBeyondDotDot()
		editVia("l/../../f")
		Expect(read(workdir, "a/f")).To(Equal("NEW"))
	})
	It("leaves the lexical file outside untouched when .. follows a link", func() {
		linkBeyondDotDot()
		editVia("l/../../f")
		Expect(read(outer, "f")).To(Equal("old"))
	})
	It("edits through a symlink that stays inside", func() {
		write(workdir, "real.txt", "old")
		link("real.txt", "sl")
		_, err := NewFSEdit(workdir).Invoke(context.Background(),
			json.RawMessage(`{"path":"sl","old_text":"old","new_text":"new"}`))
		must(err)
		Expect(read(workdir, "real.txt")).To(Equal("new"))
	})
	It("refuses to edit through a symlink to a file outside", func() {
		must(os.WriteFile(filepath.Join(outer, "secret"), []byte("old"), 0o644))
		link(filepath.Join(outer, "secret"), "sl")
		_, err := NewFSEdit(workdir).Invoke(context.Background(),
			json.RawMessage(`{"path":"sl","old_text":"old","new_text":"new"}`))
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("leaves an outside file untouched when an edit through a link is refused", func() {
		must(os.WriteFile(filepath.Join(outer, "secret"), []byte("old"), 0o644))
		link(filepath.Join(outer, "secret"), "sl")
		_, _ = NewFSEdit(workdir).Invoke(context.Background(),
			json.RawMessage(`{"path":"sl","old_text":"old","new_text":"new"}`))
		b, err := os.ReadFile(filepath.Join(outer, "secret"))
		must(err)
		Expect(string(b)).To(Equal("old"))
	})
}

var _ = Describe("file tools confined by os.Root", func() {
	if runtime.GOOS != "windows" {
		rootLinkSpecs()
	}
	It("pins the os.Root escape message the error mapping matches", func() {
		root, err := os.OpenRoot(GinkgoT().TempDir())
		must(err)
		defer func() { _ = root.Close() }()
		_, err = root.Stat("../x")
		Expect(err).To(MatchError(ContainSubstring(rootEscapeMessage)))
	})
	It("captures the os.Root escape sentinel from the probe on this toolchain", func() {
		root, err := os.OpenRoot(GinkgoT().TempDir())
		must(err)
		defer func() { _ = root.Close() }()
		_, err = root.Stat("../x")
		Expect(errors.Is(err, rootEscapeSentinel())).To(BeTrue())
	})
	It("does not take a file named like the escape message for an escape", func() {
		Expect(isRootEscape(errors.New("open " + rootEscapeMessage + ": denied"))).To(BeFalse())
	})
	It("maps a missing workdir to ErrPathOutsideWorkdir for fs.read", func() {
		_, err := NewFSRead(filepath.Join(GinkgoT().TempDir(), "missing")).Invoke(context.Background(),
			json.RawMessage(`{"path":"x"}`))
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("maps a missing workdir to ErrPathOutsideWorkdir for fs.write", func() {
		_, err := NewFSWrite(filepath.Join(GinkgoT().TempDir(), "missing")).Invoke(context.Background(),
			json.RawMessage(`{"path":"x","content":"c"}`))
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	if runtime.GOOS != "windows" {
		It("creates files 0600 and directories 0750 as before", func() {
			workdir := GinkgoT().TempDir()
			_, err := NewFSWrite(workdir).Invoke(context.Background(), json.RawMessage(`{"path":"d/f","content":"c"}`))
			must(err)
			info, serr := os.Stat(filepath.Join(workdir, "d", "f"))
			must(serr)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})
		It("creates missing parent directories 0750", func() {
			workdir := GinkgoT().TempDir()
			_, err := NewFSWrite(workdir).Invoke(context.Background(), json.RawMessage(`{"path":"d/f","content":"c"}`))
			must(err)
			info, serr := os.Stat(filepath.Join(workdir, "d"))
			must(serr)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o750)))
		})
	}
	if runtime.GOOS == "linux" {
		It("does not leak file descriptors across invocations", func() {
			workdir := GinkgoT().TempDir()
			write(workdir, "f.txt", "x")
			count := func() int {
				entries, err := os.ReadDir("/proc/self/fd")
				must(err)
				return len(entries)
			}
			tools := []Tool{NewFSRead(workdir), NewFSWrite(workdir), NewFSEdit(workdir)}
			args := []string{`{"path":"f.txt"}`, `{"path":"f.txt","content":"x"}`,
				`{"path":"f.txt","old_text":"x","new_text":"x"}`}
			run := func() {
				for i, t := range tools {
					_, _ = t.Invoke(context.Background(), json.RawMessage(args[i]))
					_, _ = t.Invoke(context.Background(), json.RawMessage(`{"path":"../x","old_text":"a","content":"a"}`))
				}
			}
			run()
			before := count()
			for range 20 {
				run()
			}
			Expect(count()).To(Equal(before))
		})
	}
})

// chdirTo makes dir the current directory for one spec and restores the previous
// one afterwards (Ginkgo runs these specs serially in-process).
func chdirTo(dir string) {
	previous, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	DeferCleanup(func() {
		if err := os.Chdir(previous); err != nil {
			panic(err)
		}
	})
}

var _ = Describe("fs tools under a relative workdir", func() {
	var base string

	BeforeEach(func() {
		base = GinkgoT().TempDir()
		write(base, "big.txt", "top")
		write(base, "sub/x.txt", "nested")
		chdirTo(base)
	})

	DescribeTable("fs.read resolves the path against the current directory",
		func(workdir, path, want string) {
			l, _ := newFSLoop(NewFSRead(workdir), []string{FSReadName})
			res, err := invoke(l, FSReadName, `{"path":`+strconv.Quote(path)+`}`)
			if err != nil {
				panic(err)
			}
			Expect(res.(map[string]any)["content"]).To(Equal(want))
		},
		Entry("with workdir . and a plain name", ".", "big.txt", "top"),
		Entry("with workdir . and a ./ prefixed name", ".", "./big.txt", "top"),
		Entry("with workdir ./sub", "./sub", "x.txt", "nested"),
		Entry("with workdir sub", "sub", "x.txt", "nested"),
		Entry("with workdir sub/..", "sub/..", "big.txt", "top"),
	)

	DescribeTable("fs.read still refuses an escape",
		func(workdir, path string) {
			l, _ := newFSLoop(NewFSRead(workdir), []string{FSReadName})
			_, err := invoke(l, FSReadName, `{"path":`+strconv.Quote(path)+`}`)
			Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
		},
		Entry("a .. traversal from workdir .", ".", "../big.txt"),
		Entry("a .. traversal from workdir sub", "sub", "../big.txt"),
		Entry("an absolute path", ".", hostAbsolutePath()),
	)

	It("refuses an absolute path naming a file inside the workdir", func() {
		l, _ := newFSLoop(NewFSRead("."), []string{FSReadName})
		_, err := invoke(l, FSReadName, `{"path":`+strconv.Quote(filepath.Join(base, "big.txt"))+`}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("refuses an empty workdir", func() {
		l, _ := newFSLoop(NewFSRead(""), []string{FSReadName})
		_, err := invoke(l, FSReadName, `{"path":"big.txt"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("treats ~ as a missing relative directory and refuses it as before", func() {
		l, _ := newFSLoop(NewFSRead("~"), []string{FSReadName})
		_, err := invoke(l, FSReadName, `{"path":"big.txt"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	if runtime.GOOS != "windows" {
		It("refuses a symlink that leaves a relative workdir", func() {
			outside := GinkgoT().TempDir()
			write(outside, "secret.txt", "secret")
			must(os.Symlink(outside, filepath.Join(base, "link")))
			l, _ := newFSLoop(NewFSRead("."), []string{FSReadName})
			_, err := invoke(l, FSReadName, `{"path":"link/secret.txt"}`)
			Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
		})

		It("reads through an absolute workdir that is a symlink to a directory", func() {
			viaLink := filepath.Join(GinkgoT().TempDir(), "rootlink")
			must(os.Symlink(base, viaLink))
			l, _ := newFSLoop(NewFSRead(viaLink), []string{FSReadName})
			res, err := invoke(l, FSReadName, `{"path":"big.txt"}`)
			if err != nil {
				panic(err)
			}
			Expect(res.(map[string]any)["content"]).To(Equal("top"))
		})
	}

	It("fs.write creates a file under a relative workdir", func() {
		l, _ := newFSLoop(NewFSWrite("."), []string{FSWriteName})
		_, err := invoke(l, FSWriteName, `{"path":"made/new.txt","content":"fresh"}`)
		if err != nil {
			panic(err)
		}
		Expect(read(base, "made/new.txt")).To(Equal("fresh"))
	})

	It("fs.edit edits a file under a relative workdir", func() {
		l, _ := newFSLoop(NewFSEdit("."), []string{FSEditName})
		_, err := invoke(l, FSEditName, `{"path":"big.txt","old_text":"top","new_text":"changed"}`)
		if err != nil {
			panic(err)
		}
		Expect(read(base, "big.txt")).To(Equal("changed"))
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

// hostAbsolutePath returns a path that is absolute on the host: on Windows a
// bare "/x" has no volume and is relative, so a volume-qualified one is used.
func hostAbsolutePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\win.ini`
	}
	return "/big.txt"
}
