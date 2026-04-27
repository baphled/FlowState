package gates_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/gates"
)

var _ = Describe("Discover", func() {
	It("returns each manifest in <gates_dir>/<name>/manifest.yml", func() {
		root := newGatesDir(map[string]string{
			"alpha": `name: alpha
exec: ./gate.sh`,
			"beta": `name: beta
exec: ./gate.sh`,
			"deeper": `name: deeper
exec: ./gate.sh`,
		})

		got, err := gates.Discover(root)

		Expect(err).ToNot(HaveOccurred())
		names := []string{}
		for _, m := range got {
			names = append(names, m.Name)
		}
		Expect(names).To(ConsistOf("alpha", "beta", "deeper"))
	})

	It("ignores files at the root of gates_dir", func() {
		root := newGatesDir(nil)
		Expect(os.WriteFile(filepath.Join(root, "stray.yml"), []byte("name: stray"), 0o600)).To(Succeed())

		got, err := gates.Discover(root)

		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("ignores subdirectories that do not contain manifest.yml", func() {
		root := newGatesDir(nil)
		Expect(os.MkdirAll(filepath.Join(root, "empty-dir"), 0o700)).To(Succeed())

		got, err := gates.Discover(root)

		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("returns no error and no entries when gates_dir does not exist", func() {
		got, err := gates.Discover("/tmp/does-not-exist-flowstate-gates")

		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("returns the malformed manifest's error wrapped with its path", func() {
		root := newGatesDir(map[string]string{
			"broken": `name: ""
exec: ./gate.sh`,
		})

		_, err := gates.Discover(root)

		Expect(err).To(MatchError(ContainSubstring("broken/manifest.yml")))
	})
})

func newGatesDir(entries map[string]string) string {
	root, err := os.MkdirTemp("", "gates-discover-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(root) })
	for name, manifest := range entries {
		dir := filepath.Join(root, name)
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte(manifest), 0o600)).To(Succeed())
	}
	return root
}
