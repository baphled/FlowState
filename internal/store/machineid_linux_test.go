//go:build linux

package store

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Linux machine id read", func() {
	var dir string

	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			panic(err)
		}
		return p
	}

	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("reads the first file", func() {
		first := write("a", "id-one\n")
		second := write("b", "id-two\n")
		Expect(firstIDFrom([]string{first, second})).To(Equal("id-one\n"))
	})

	It("falls through to the second file when the first is missing", func() {
		second := write("b", "id-two\n")
		Expect(firstIDFrom([]string{filepath.Join(dir, "none"), second})).To(Equal("id-two\n"))
	})

	It("falls through when the first file is empty", func() {
		first := write("a", "")
		second := write("b", "id-two\n")
		Expect(firstIDFrom([]string{first, second})).To(Equal("id-two\n"))
	})

	It("reads nothing when no file exists", func() {
		Expect(firstIDFrom([]string{filepath.Join(dir, "none")})).To(BeEmpty())
	})

	It("reads nothing when the directory is missing", func() {
		Expect(firstIDFrom([]string{filepath.Join(dir, "nodir", "x")})).To(BeEmpty())
	})

	It("feeds MachineIdentity through the real seam list", func() {
		p := write("machine-id", "abc123\n")
		orig := linuxIDFiles
		DeferCleanup(func() { linuxIDFiles = orig })
		linuxIDFiles = []string{p}
		Expect(readMachineID()).To(Equal("abc123\n"))
	})
})
