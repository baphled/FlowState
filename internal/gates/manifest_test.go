package gates_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/gates"
)

var _ = Describe("Manifest loader", func() {
	It("parses a complete manifest", func() {
		dir := writeManifest(`name: vault-fact-check
description: Score a member claim against the vault.
version: "0.1.0"
exec: ./gate.py
timeout: 10s
policy:
  threshold: 0.65
  top_k: 3
`)

		m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))

		Expect(err).ToNot(HaveOccurred())
		Expect(m.Name).To(Equal("vault-fact-check"))
		Expect(m.Exec).To(Equal("./gate.py"))
		Expect(m.Timeout).To(Equal(10 * time.Second))
		Expect(m.Dir).To(Equal(dir))
		Expect(m.Policy["threshold"]).To(Equal(0.65))
	})

	It("defaults timeout to 30s when omitted", func() {
		dir := writeManifest(`name: x
exec: ./gate.sh
`)
		m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(m.Timeout).To(Equal(30 * time.Second))
	})

	It("rejects empty name", func() {
		dir := writeManifest(`name: ""
exec: ./gate.sh
`)
		_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).To(MatchError(ContainSubstring("name")))
	})

	It("rejects empty exec", func() {
		dir := writeManifest(`name: x
exec: ""
`)
		_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).To(MatchError(ContainSubstring("exec")))
	})

	It("rejects negative timeout", func() {
		dir := writeManifest(`name: x
exec: ./gate.sh
timeout: -1s
`)
		_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).To(MatchError(ContainSubstring("timeout")))
	})

	It("returns AbsoluteExecPath for a relative exec", func() {
		dir := writeManifest(`name: x
exec: ./gate.sh
`)
		m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(m.AbsoluteExecPath()).To(Equal(filepath.Join(dir, "gate.sh")))
	})

	It("returns AbsoluteExecPath for an absolute exec verbatim", func() {
		dir := writeManifest(`name: x
exec: /usr/bin/jq
`)
		m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(m.AbsoluteExecPath()).To(Equal("/usr/bin/jq"))
	})
})

func writeManifest(body string) string {
	dir, err := os.MkdirTemp("", "gates-manifest-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	Expect(os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte(body), 0o600)).To(Succeed())
	return dir
}
