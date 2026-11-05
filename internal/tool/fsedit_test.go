package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("fs.edit", func() {
	var workdir string
	var l *Loop

	BeforeEach(func() {
		workdir = GinkgoT().TempDir()
		write(workdir, "a.txt", "hello world")
		l, _ = newFSLoop(NewFSEdit(workdir), []string{FSEditName})
	})

	It("replaces a unique match", func() {
		res, err := invoke(l, FSEditName, `{"path":"a.txt","old_text":"world","new_text":"flowstate"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "a.txt")).To(Equal("hello flowstate"))
		Expect(res.(map[string]any)["occurrences"]).To(Equal(1))
	})

	It("edits a nested file with a cleaned relative path", func() {
		write(workdir, "sub/b.txt", "deep")
		_, err := invoke(l, FSEditName, `{"path":"./sub/../sub/b.txt","old_text":"deep","new_text":"shallow"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "sub/b.txt")).To(Equal("shallow"))
	})

	It("replaces all occurrences with all=true", func() {
		write(workdir, "rep.txt", "x-x-x")
		res, err := invoke(l, FSEditName, `{"path":"rep.txt","old_text":"x","new_text":"y","all":true}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(workdir, "rep.txt")).To(Equal("y-y-y"))
		Expect(res.(map[string]any)["occurrences"]).To(Equal(3))
	})

	It("returns ErrNotUnique for a non-unique match without all", func() {
		write(workdir, "rep.txt", "x-x-x")
		_, err := invoke(l, FSEditName, `{"path":"rep.txt","old_text":"x","new_text":"y"}`)
		Expect(errors.Is(err, ErrOldTextNotUnique)).To(BeTrue())
		Expect(read(workdir, "rep.txt")).To(Equal("x-x-x"))
	})

	It("returns ErrOldTextNotFound when old_text is absent", func() {
		_, err := invoke(l, FSEditName, `{"path":"a.txt","old_text":"zzz","new_text":"y"}`)
		Expect(errors.Is(err, ErrOldTextNotFound)).To(BeTrue())
		Expect(read(workdir, "a.txt")).To(Equal("hello world"))
	})

	It("returns ErrFileNotFound for a missing file", func() {
		_, err := invoke(l, FSEditName, `{"path":"nope.txt","old_text":"a","new_text":"b"}`)
		Expect(errors.Is(err, ErrFileNotFound)).To(BeTrue())
	})

	It("rejects an empty old_text", func() {
		_, err := invoke(l, FSEditName, `{"path":"a.txt","old_text":"","new_text":"b"}`)
		Expect(errors.Is(err, ErrOldTextNotFound)).To(BeTrue())
	})

	It("returns ErrPathOutsideWorkdir for traversal", func() {
		_, err := invoke(l, FSEditName, `{"path":"../escape.txt","old_text":"a","new_text":"b"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("returns ErrPathOutsideWorkdir for a host absolute path", func() {
		abs := filepath.Join(GinkgoT().TempDir(), "out.txt")
		if runtime.GOOS == "windows" {
			abs = `C:\Windows\out.txt`
		}
		_, err := invoke(l, FSEditName, `{"path":`+strconv.Quote(abs)+`,"old_text":"a","new_text":"b"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("returns ErrPathOutsideWorkdir for an empty path", func() {
		_, err := invoke(l, FSEditName, `{"path":"","old_text":"a","new_text":"b"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("refuses a symlink escaping the workdir", func() {
		out := GinkgoT().TempDir()
		must(os.Symlink(out, filepath.Join(workdir, "link")))
		_, err := invoke(l, FSEditName, `{"path":"link/esc.txt","old_text":"a","new_text":"b"}`)
		Expect(errors.Is(err, ErrPathOutsideWorkdir)).To(BeTrue())
	})

	It("rejects an oversize file with ErrOversize and does not modify it", func() {
		write(workdir, "big.txt", "0123456789")
		_, err := NewFSEdit(workdir, WithMaxEditBytes(4)).Invoke(context.Background(),
			json.RawMessage(`{"path":"big.txt","old_text":"0123","new_text":"x"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
		Expect(read(workdir, "big.txt")).To(Equal("0123456789"))
	})

	It("rejects oversize replacement content with ErrOversize", func() {
		_, err := NewFSEdit(workdir, WithMaxEditBytes(8)).Invoke(context.Background(),
			json.RawMessage(`{"path":"a.txt","old_text":"world","new_text":"a very long replacement"}`))
		Expect(errors.Is(err, ErrOversize)).To(BeTrue())
		Expect(read(workdir, "a.txt")).To(Equal("hello world"))
	})

	It("rejects malformed argument JSON", func() {
		_, err := invoke(l, FSEditName, `not-json`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Loop typed fs.edit error codes", func() {
	It("maps ErrOldTextNotUnique to its code", func() {
		workdir := GinkgoT().TempDir()
		write(workdir, "r.txt", "x-x")
		l, rs := newFSLoop(NewFSEdit(workdir), []string{FSEditName})
		_, _ = invoke(l, FSEditName, `{"path":"r.txt","old_text":"x","new_text":"y"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeOldTextNotUnique))
	})
	It("maps ErrOldTextNotFound to its code", func() {
		l, rs := newFSLoop(NewFSEdit(GinkgoT().TempDir()), []string{FSEditName})
		_, _ = invoke(l, FSEditName, `{"path":"m.txt","old_text":"a","new_text":"b"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodeNotFound))
	})
	It("maps ErrPathOutsideWorkdir to its code", func() {
		l, rs := newFSLoop(NewFSEdit(GinkgoT().TempDir()), []string{FSEditName})
		_, _ = invoke(l, FSEditName, `{"path":"../x","old_text":"a","new_text":"b"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodePathOutsideWorkdir))
	})
})
