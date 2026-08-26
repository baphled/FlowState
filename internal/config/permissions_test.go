package config_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// Permissions covers Slice A of the Tool-Scoped Permissions plan: the
// YAML loader and the per-tool glob matcher. Behaviour is deliberately
// stand-alone here — no caller wires the matcher in Slice A, so the
// existing legacy VaultPath/pathguard semantics remain in force in
// production. These specs lock the shape of the foundation that later
// slices will route through.
var _ = Describe("Permissions", func() {
	Describe("LoadPermissions", func() {
		It("parses a valid version-1 file with allow-only and allow+deny tools", func() {
			perms, err := config.LoadPermissions(filepath.Join("testdata", "permissions", "valid.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).NotTo(BeNil())
			Expect(perms.Version).To(Equal(1))
			Expect(perms.Tools).To(HaveKey("read"))
			Expect(perms.Tools).To(HaveKey("bash"))

			read := perms.Tools["read"]
			Expect(read.Allow).To(ConsistOf("/tmp/allowed/**"))
			Expect(read.Deny).To(BeEmpty())

			bash := perms.Tools["bash"]
			Expect(bash.Allow).To(ConsistOf("/tmp/work/**"))
			Expect(bash.Deny).To(ConsistOf("/tmp/work/secrets/**"))
		})

		It("returns (nil, nil) for a missing file (not an error)", func() {
			perms, err := config.LoadPermissions(filepath.Join("testdata", "permissions", "does-not-exist.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).To(BeNil())
		})

		It("returns (nil, nil) for an unknown version and logs a slog warning", func() {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			DeferCleanup(func() { slog.SetDefault(prev) })

			perms, err := config.LoadPermissions(filepath.Join("testdata", "permissions", "unknown_version.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).To(BeNil())
			Expect(buf.String()).To(ContainSubstring("level=WARN"))
			Expect(buf.String()).To(ContainSubstring("unknown permissions version"))
		})
	})

	Describe("Match", func() {
		var perms *config.Permissions

		BeforeEach(func() {
			perms = &config.Permissions{
				Version: 1,
				Tools: map[string]config.ToolRules{
					"read": {
						Allow: []string{"/tmp/allowed/**"},
					},
					"bash": {
						Allow: []string{"/tmp/work/**"},
						Deny:  []string{"/tmp/work/secrets/**"},
					},
					"recursive": {
						Allow: []string{"/tmp/deep/**"},
					},
					"deny-only": {
						Deny: []string{"/etc/shadow"},
					},
				},
			}
		})

		It("returns (\"allow\", true) when an allow glob matches and no deny does", func() {
			decision, matched := perms.Match("read", "/tmp/allowed/file.txt")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("allow"))
		})

		It("returns (\"deny\", true) when a deny-only entry matches", func() {
			decision, matched := perms.Match("deny-only", "/etc/shadow")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("deny"))
		})

		It("returns (\"deny\", true) when both allow AND deny match (deny wins)", func() {
			decision, matched := perms.Match("bash", "/tmp/work/secrets/api-key")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("deny"))
		})

		It("returns (\"\", false) when the tool has no entry (caller falls through)", func() {
			decision, matched := perms.Match("unknown-tool", "/tmp/allowed/file.txt")
			Expect(matched).To(BeFalse())
			Expect(decision).To(BeEmpty())
		})

		It("recursive ** glob matches across multiple depths", func() {
			// proves doublestar wiring vs. stdlib path.Match (which only does single segment)
			decision, matched := perms.Match("recursive", "/tmp/deep/a/b/c/d/file.txt")
			Expect(matched).To(BeTrue())
			Expect(decision).To(Equal("allow"))
		})
	})

	// Plan-Mode Output Directory plan (May 2026) §3 Slice 1 adds
	// plan_output_dir to the permissions schema. The field carries the
	// absolute directory under which Plan mode permits write/edit/
	// multiedit/apply_patch — see pathguard.NewWithPermissionsAndPlanOutputDir
	// for the overlay that consumes the value.
	Describe("PlanOutputDir field (Slice 1)", func() {
		It("parses a populated plan_output_dir field from YAML", func() {
			yamlContent := []byte(`version: 1
plan_output_dir: /tmp/flowstate-plans
tools:
  write:
    allow:
      - /vault/**
`)
			path := filepath.Join(GinkgoT().TempDir(), "permissions.yaml")
			Expect(os.WriteFile(path, yamlContent, 0o600)).To(Succeed())

			perms, err := config.LoadPermissions(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).NotTo(BeNil())
			Expect(perms.PlanOutputDir).To(Equal("/tmp/flowstate-plans"))
		})

		It("leaves plan_output_dir empty when the field is absent", func() {
			yamlContent := []byte(`version: 1
tools:
  write:
    allow:
      - /vault/**
`)
			path := filepath.Join(GinkgoT().TempDir(), "permissions.yaml")
			Expect(os.WriteFile(path, yamlContent, 0o600)).To(Succeed())

			perms, err := config.LoadPermissions(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).NotTo(BeNil())
			Expect(perms.PlanOutputDir).To(BeEmpty(),
				"missing plan_output_dir MUST default to empty so the overlay collapses safely (Plan-mode writes fail closed)")
		})
	})
})
