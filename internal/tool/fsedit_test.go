package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fsEditLoop builds an fs.edit loop over a temp workdir.
func fsEditLoop(workdir string, opts ...FSOption) *Loop {
	t := NewFSEdit(workdir, opts...)
	l, _ := newFSLoop(t, []string{FSEditName})
	return l
}

var _ = Describe("fs.edit", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		l = fsEditLoop(workdir)
	})
	It("replaces the unique match and returns {path,bytes}", func() {
		write(workdir, "a.txt", "hello world")
		res, err := invoke(l, FSEditName, `{"path":"a.txt","find":"world","replace":"flowstate"}`)
		Expect(err).NotTo(HaveOccurred())
		m := res.(map[string]any)
		Expect(m["path"]).To(Equal("a.txt"))
		Expect(m["bytes"]).To(Equal(len("hello flowstate")))
		Expect(read(workdir, "a.txt")).To(Equal("hello flowstate"))
	})
	It("edits a nested path", func() {
		write(workdir, "sub/b.txt", "aaa")
		_, err := invoke(l, FSEditName, `{"path":"sub/b.txt","find":"aa","replace":"b"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "sub/b.txt")).To(Equal("ba"))
	})
	It("errors fs_edit_ambiguous when find occurs more than once", func() {
		write(workdir, "dup.txt", "x x x")
		_, err := invoke(l, FSEditName, `{"path":"dup.txt","find":"x","replace":"y"}`)
		Expect(errors.Is(err, ErrEditAmbiguous)).To(BeTrue())
		Expect(read(workdir, "dup.txt")).To(Equal("x x x"))
	})
	It("errors fs_edit_not_found on zero matches", func() {
		write(workdir, "z.txt", "nothing here")
		_, err := invoke(l, FSEditName, `{"path":"z.txt","find":"absent","replace":"y"}`)
		Expect(errors.Is(err, ErrEditNotFound)).To(BeTrue())
		Expect(read(workdir, "z.txt")).To(Equal("nothing here"))
	})
	It("returns ErrFileNotFound for a missing file", func() {
		_, err := invoke(l, FSEditName, `{"path":"nope.txt","find":"a","replace":"b"}`)
		Expect(errors.Is(err, ErrFileNotFound)).To(BeTrue())
	})
	It("returns ErrPathOutsideWorkdir for traversal", func() {
		_, err := invoke(l, FSEditName, `{"path":"../esc.txt","find":"a","replace":"b"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
	It("rejects oversize source with ErrOversize", func() {
		write(workdir, "big.txt", "0123456789")
		_, err := NewFSEdit(workdir, WithMaxReadBytes(4)).Invoke(context.Background(),
			json.RawMessage(`{"path":"big.txt","find":"0","replace":"1"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
		Expect(read(workdir, "big.txt")).To(Equal("0123456789"))
	})
	It("rejects oversize result with ErrOversize without writing", func() {
		write(workdir, "grow.txt", "ab")
		_, err := NewFSEdit(workdir, WithMaxWriteBytes(4)).Invoke(context.Background(),
			json.RawMessage(`{"path":"grow.txt","find":"b","replace":"bbbbbb"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
		Expect(read(workdir, "grow.txt")).To(Equal("ab"))
	})
	It("rejects malformed argument JSON", func() {
		_, err := invoke(l, FSEditName, `not-json`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("fs.edit typed error codes", func() {
	It("maps ErrEditAmbiguous to fs_edit_ambiguous", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "d.txt", "x x")
		l, rs := newFSLoop(NewFSEdit(workdir), []string{FSEditName})
		_, _ = invoke(l, FSEditName, `{"path":"d.txt","find":"x","replace":"y"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeEditAmbiguous))
	})
	It("maps ErrEditNotFound to fs_edit_not_found", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "n.txt", "content")
		l, rs := newFSLoop(NewFSEdit(workdir), []string{FSEditName})
		_, _ = invoke(l, FSEditName, `{"path":"n.txt","find":"q","replace":"y"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeEditNotFound))
	})
})

var _ = Describe("fs.edit symlink escape", func() {
	It("refuses a symlink escaping the workdir", func() {
		workdir := GinkgoT().TempDir()
		out := GinkgoT().TempDir()
		Expect(os.Symlink(out, filepath.Join(workdir, "link"))).To(Succeed())
		_, err := fsEditLoop(workdir).Invoke(context.Background(), FSEditName,
			json.RawMessage(`{"path":"link/esc.txt","find":"a","replace":"b"}`))
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})
})

var _ = Describe("fs.edit allowlist integration", func() {
	It("is denied when not allowlisted", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "a.txt", "hi")
		l, _ := newFSLoop(NewFSEdit(workdir), []string{FSReadName})
		_, err := invoke(l, FSEditName, `{"path":"a.txt","find":"i","replace":"o"}`)
		Expect(errors.Is(err, ErrDenied)).To(BeTrue())
	})
})
