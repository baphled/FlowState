package manifest_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	manifestpkg "github.com/baphled/flowstate/internal/plugin/manifest"
)

var _ = Describe("Manifest", func() {
	It("loads and parses a valid manifest", func() {
		path := writeManifestFile(`{"name":"example","version":"1.0.0","description":"Demo","command":"run","args":["--flag"],"hooks":["chat.params"],"timeout":10}`)

		m, err := manifestpkg.LoadManifest(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Name).To(Equal("example"))
		Expect(m.Version).To(Equal("1.0.0"))
		Expect(m.Description).To(Equal("Demo"))
		Expect(m.Command).To(Equal("run"))
		Expect(m.Args).To(Equal([]string{"--flag"}))
		Expect(m.Hooks).To(Equal([]string{"chat.params"}))
		Expect(m.Timeout).To(Equal(10))
	})

	It("returns an error when name is missing", func() {
		m := &manifestpkg.Manifest{Version: "1.0.0", Command: "run", Hooks: []string{"chat.params"}}

		err := manifestpkg.Validate(m)
		Expect(err).To(MatchError(ContainSubstring("name is required")))
	})

	It("returns an error when command is missing", func() {
		m := &manifestpkg.Manifest{Name: "example", Version: "1.0.0", Hooks: []string{"chat.params"}}

		err := manifestpkg.Validate(m)
		Expect(err).To(MatchError(ContainSubstring("command is required")))
	})

	It("returns an error when hooks are empty", func() {
		m := &manifestpkg.Manifest{Name: "example", Version: "1.0.0", Command: "run"}

		err := manifestpkg.Validate(m)
		Expect(err).To(MatchError(ContainSubstring("at least one hook is required")))
	})

	It("returns an error when a hook type is invalid", func() {
		m := &manifestpkg.Manifest{Name: "example", Version: "1.0.0", Command: "run", Hooks: []string{"invalid.hook"}}

		err := manifestpkg.Validate(m)
		Expect(err).To(MatchError(ContainSubstring("valid types")))
		Expect(err).To(MatchError(ContainSubstring("chat.params")))
	})

	It("returns a parse error for malformed json", func() {
		path := writeManifestFile(`{"name":"example",`)

		_, err := manifestpkg.LoadManifest(path)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parse manifest"))
	})

	It("applies the default timeout when missing", func() {
		m := &manifestpkg.Manifest{Name: "example", Version: "1.0.0", Command: "run", Hooks: []string{"chat.params"}}

		err := manifestpkg.Validate(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Timeout).To(Equal(5))
	})
})

func writeManifestFile(contents string) string {
	file, err := os.CreateTemp("", "manifest-*.json")
	Expect(err).NotTo(HaveOccurred())

	_, err = file.WriteString(contents)
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())

	DeferCleanup(func() {
		_ = os.Remove(file.Name())
	})

	return filepath.Clean(file.Name())
}
