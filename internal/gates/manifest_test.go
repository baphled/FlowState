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

	Context("inputs declaration (multi-key coord-store payload)", func() {
		It("parses a list of named inputs each with member + output_key", func() {
			dir := writeManifest(`name: relevance-gate
exec: ./gate.py
inputs:
  - name: task_plan
    member: coordinator
    output_key: task-plan
  - name: research
    member: ${target}
    output_key: output
`)

			m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))

			Expect(err).ToNot(HaveOccurred())
			Expect(m.Inputs).To(HaveLen(2))
			Expect(m.Inputs[0].Name).To(Equal("task_plan"))
			Expect(m.Inputs[0].Member).To(Equal("coordinator"))
			Expect(m.Inputs[0].OutputKey).To(Equal("task-plan"))
			Expect(m.Inputs[1].Name).To(Equal("research"))
			Expect(m.Inputs[1].Member).To(Equal("${target}"))
			Expect(m.Inputs[1].OutputKey).To(Equal("output"))
		})

		It("treats an omitted inputs block as a nil/empty Inputs slice", func() {
			dir := writeManifest(`name: simple
exec: ./gate.sh
`)

			m, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))

			Expect(err).ToNot(HaveOccurred())
			Expect(m.Inputs).To(BeEmpty())
		})

		It("rejects an inputs entry with an empty name", func() {
			dir := writeManifest(`name: relevance-gate
exec: ./gate.py
inputs:
  - name: ""
    member: coordinator
    output_key: task-plan
`)
			_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
			Expect(err).To(MatchError(ContainSubstring("inputs[0].name")))
		})

		It("rejects an inputs entry with an empty member", func() {
			dir := writeManifest(`name: relevance-gate
exec: ./gate.py
inputs:
  - name: task_plan
    member: ""
    output_key: task-plan
`)
			_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
			Expect(err).To(MatchError(ContainSubstring("inputs[0].member")))
		})

		It("rejects an inputs entry with an empty output_key", func() {
			dir := writeManifest(`name: relevance-gate
exec: ./gate.py
inputs:
  - name: task_plan
    member: coordinator
    output_key: ""
`)
			_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
			Expect(err).To(MatchError(ContainSubstring("inputs[0].output_key")))
		})

		It("rejects duplicate input names", func() {
			dir := writeManifest(`name: relevance-gate
exec: ./gate.py
inputs:
  - name: task_plan
    member: coordinator
    output_key: task-plan
  - name: task_plan
    member: researcher
    output_key: output
`)
			_, err := gates.LoadManifest(filepath.Join(dir, "manifest.yml"))
			Expect(err).To(MatchError(ContainSubstring("duplicate")))
		})
	})
})

func writeManifest(body string) string {
	dir, err := os.MkdirTemp("", "gates-manifest-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	Expect(os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte(body), 0o600)).To(Succeed())
	return dir
}
