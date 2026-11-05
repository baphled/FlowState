package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// newFSLoop registers one tool and allowlist over a temp session.
func newFSLoop(t Tool, allow []string) (*Loop, *replayStore) {
	st, sid, w := newStoreWithSession()
	r := NewRegistry()
	must(r.Register(t))
	l, err := NewLoop(r, w, allow)
	must(err)
	return l, &replayStore{st: st, sid: sid}
}

// replayStore pairs store+session for event replay.
type replayStore struct {
	st  *store.Store
	sid string
}

func (r *replayStore) events() ([]store.RecordedEvent, error) {
	return r.st.Replay(context.Background(), r.sid)
}

// write creates rel under workdir (arrange helper).
func write(workdir, rel, content string) {
	must(os.MkdirAll(filepath.Dir(filepath.Join(workdir, rel)), 0o755))
	must(os.WriteFile(filepath.Join(workdir, rel), []byte(content), 0o644))
}

// read returns file content under workdir (assert helper).
func read(workdir, rel string) string {
	b, err := os.ReadFile(filepath.Join(workdir, rel))
	must(err)
	return string(b)
}

// must panics on fixture failure (arrange-only).
func must(err error) {
	if err != nil {
		panic(err)
	}
}

func invoke(l *Loop, name, args string) (any, error) {
	return l.Invoke(context.Background(), name, json.RawMessage(args))
}

// lastResultCode: latest tool.result error.code ("" when absent).
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

var _ = Describe("FSRead", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		write(workdir, "a.txt", "hello")
		write(workdir, "sub/b.txt", "deep")
		l, _ = newFSLoop(NewFSRead(workdir), []string{FSRead})
	})

	It("reads a file under the workdir", func() {
		res, err := invoke(l, FSRead, `{"path":"a.txt"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["content"]).To(Equal("hello"))
	})
	It("reads a nested file with a cleaned relative path", func() {
		res, err := invoke(l, FSRead, `{"path":"./sub/../sub/b.txt"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.(map[string]any)["content"]).To(Equal("deep"))
	})
	It("returns ErrPathOutsideWorkdir for traversal", func() {
		_, err := invoke(l, FSRead, `{"path":"../escape.txt"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for an absolute path", func() {
		_, err := invoke(l, FSRead, `{"path":"/etc/passwd"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("returns ErrPathOutsideWorkdir for an empty path", func() {
		_, err := invoke(l, FSRead, `{"path":""}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("returns ErrFileNotFound for a missing file", func() {
		_, err := invoke(l, FSRead, `{"path":"nope.txt"}`)
		Expect(errors.Is(err, ErrFileNotFound)).To(BeTrue())
	})

	It("returns ErrOversize above the read cap", func() {
		write(workdir, "big.txt", "too large")
		_, err := NewFSRead(workdir, WithMaxReadBytes(3)).Invoke(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
	})

	It("rejects malformed argument JSON", func() {
		_, err := invoke(l, FSRead, `not-json`)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeFalse())
	})
})

var _ = Describe("FSWrite", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		l, _ = newFSLoop(NewFSWrite(workdir), []string{FSWrite})
	})

	It("writes a new file under the workdir", func() {
		_, err := invoke(l, FSWrite, `{"path":"new.txt","content":"data"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "new.txt")).To(Equal("data"))
	})
	It("overwrites an existing file", func() {
		write(workdir, "x.txt", "old")
		_, err := invoke(l, FSWrite, `{"path":"x.txt","content":"new"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "x.txt")).To(Equal("new"))
	})
	It("creates parent directories implicitly", func() {
		_, err := invoke(l, FSWrite, `{"path":"d1/d2/f.txt","content":"x"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "d1/d2/f.txt")).To(Equal("x"))
	})

	It("returns ErrPathOutsideWorkdir for traversal", func() {
		_, err := invoke(l, FSWrite, `{"path":"../out.txt","content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for an absolute path", func() {
		_, err := invoke(l, FSWrite, `{"path":"/tmp/out.txt","content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("rejects oversize writes without creating the file", func() {
		_, err := NewFSWrite(workdir, WithMaxWriteBytes(4)).Invoke(context.Background(), json.RawMessage(`{"path":"big.txt","content":"too large"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
		_, serr := os.Stat(filepath.Join(workdir, "big.txt"))
		Expect(os.IsNotExist(serr)).To(BeTrue())
	})

	It("refuses a symlink escaping the workdir", func() {
		out := GinkgoT().TempDir()
		must(os.Symlink(out, filepath.Join(workdir, "link")))
		_, err := invoke(l, FSWrite, `{"path":"link/esc.txt","content":"x"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
})

var _ = Describe("Loop typed fs error codes", func() {
	It("maps ErrPathOutsideWorkdir to its code", func() {
		l, rs := newFSLoop(NewFSRead(GinkgoT().TempDir()), []string{FSRead})
		_, err := invoke(l, FSRead, `{"path":"../x"}`)
		Expect(err).To(HaveOccurred())
		Expect(lastResultCode(rs)).To(Equal(ErrCodePathOutsideWorkdir))
	})
	It("maps ErrFileNotFound to its code", func() {
		l, rs := newFSLoop(NewFSRead(GinkgoT().TempDir()), []string{FSRead})
		_, err := invoke(l, FSRead, `{"path":"missing.txt"}`)
		Expect(err).To(HaveOccurred())
		Expect(lastResultCode(rs)).To(Equal(ErrCodeNotFound))
	})
	It("maps ErrOversize to its code", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "big.txt", "0123456789")
		l, rs := newFSLoop(NewFSRead(workdir, WithMaxReadBytes(4)), []string{FSRead})
		_, err := invoke(l, FSRead, `{"path":"big.txt"}`)
		Expect(err).To(HaveOccurred())
		Expect(lastResultCode(rs)).To(Equal(ErrCodeOversize))
	})
})
