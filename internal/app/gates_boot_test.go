package app_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("App gate registration", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	It("registers each gate manifest discovered under gates_dir", func() {
		root := writeGate("alpha", "alpha-pass.sh", `#!/bin/bash
cat > /dev/null
echo '{"pass": true}'
`)

		errs := app.RegisterDiscoveredGates(context.Background(), &config.AppConfig{GatesDir: root})

		Expect(errs).To(BeEmpty())
		_, ok := swarm.LookupExtGate("alpha")
		Expect(ok).To(BeTrue())
	})

	It("does not error when gates_dir is empty / missing", func() {
		errs := app.RegisterDiscoveredGates(context.Background(), &config.AppConfig{GatesDir: "/tmp/does-not-exist-gates"})
		Expect(errs).To(BeEmpty())
	})

	It("returns per-gate errors without aborting boot", func() {
		root := writeGate("broken", "missing-exec.sh", `#!/bin/bash
echo nope
`)
		Expect(os.Remove(filepath.Join(root, "broken", "missing-exec.sh"))).To(Succeed())

		errs := app.RegisterDiscoveredGates(context.Background(), &config.AppConfig{GatesDir: root})

		Expect(errs).To(HaveLen(1))
		_, ok := swarm.LookupExtGate("broken")
		Expect(ok).To(BeFalse())
	})
})

func writeGate(name, execName, body string) string {
	root, err := os.MkdirTemp("", "gates-boot-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(root) })

	dir := filepath.Join(root, name)
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, execName), []byte(body), 0o755)).To(Succeed())
	manifest := "name: " + name + "\nexec: ./" + execName + "\n"
	Expect(os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte(manifest), 0o600)).To(Succeed())
	return root
}
