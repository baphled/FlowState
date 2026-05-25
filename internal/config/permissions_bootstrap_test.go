package config_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// PermissionsBootstrap covers Slice C of the Tool-Scoped Permissions
// plan: the first-run hook that materialises permissions.yaml under
// the XDG config dir from the embedded default, substituting the
// operator's vault path into the ${FLOWSTATE_VAULT_ROOT} placeholder.
var _ = Describe("EnsurePermissionsFile", func() {
	Describe("first-run write path", func() {
		It("creates permissions.yaml with ${FLOWSTATE_VAULT_ROOT} substituted to the vault path", func() {
			dir := GinkgoT().TempDir()
			vaultPath := filepath.Join(dir, "vault-root")

			err := config.EnsurePermissionsFile(dir, vaultPath)
			Expect(err).NotTo(HaveOccurred())

			target := filepath.Join(dir, "permissions.yaml")
			data, err := os.ReadFile(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(vaultPath + "/**"))
			Expect(string(data)).NotTo(ContainSubstring("${FLOWSTATE_VAULT_ROOT}"),
				"first-run write must substitute the placeholder when vaultPath is non-empty")

			perms, err := config.LoadPermissions(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(perms).NotTo(BeNil())
			Expect(perms.Version).To(Equal(1))
			Expect(perms.Tools).To(HaveKey("bash"))
			Expect(perms.Tools).To(HaveKey("read"))
			Expect(perms.Tools).To(HaveKey("write"))
			Expect(perms.Tools).To(HaveKey("edit"))
			Expect(perms.Tools).To(HaveKey("multiedit"))
			Expect(perms.Tools).To(HaveKey("apply_patch"))
		})

		It("warns and writes the placeholder intact when vaultPath is empty", func() {
			dir := GinkgoT().TempDir()
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			DeferCleanup(func() { slog.SetDefault(prev) })

			err := config.EnsurePermissionsFile(dir, "")
			Expect(err).NotTo(HaveOccurred())

			target := filepath.Join(dir, "permissions.yaml")
			data, err := os.ReadFile(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("${FLOWSTATE_VAULT_ROOT}/**"),
				"empty vaultPath must leave the placeholder intact per plan §2.2")
			Expect(buf.String()).To(ContainSubstring("level=WARN"))
			Expect(buf.String()).To(ContainSubstring("vault_path is empty"))
		})
	})

	Describe("idempotency", func() {
		It("is a no-op when permissions.yaml already exists (operator customisation preserved)", func() {
			dir := GinkgoT().TempDir()
			target := filepath.Join(dir, "permissions.yaml")
			operatorContent := "version: 1\ntools:\n  bash:\n    deny:\n      - /never/touch/**\n"
			Expect(os.WriteFile(target, []byte(operatorContent), 0o600)).To(Succeed())

			err := config.EnsurePermissionsFile(dir, "/some/vault")
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(operatorContent),
				"the bootstrap must never overwrite an existing operator-owned permissions.yaml")
		})
	})

	Describe("unwritable directory", func() {
		It("warns and returns nil so the caller falls back to the embedded default", func() {
			if runtime.GOOS == "windows" {
				Skip("unix-style 0o500 permission semantics — skipped on windows")
			}
			if os.Geteuid() == 0 {
				Skip("running as root — chmod 0o500 does not block writes")
			}

			dir := GinkgoT().TempDir()
			Expect(os.Chmod(dir, 0o500)).To(Succeed())
			DeferCleanup(func() { _ = os.Chmod(dir, 0o700) })

			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			DeferCleanup(func() { slog.SetDefault(prev) })

			err := config.EnsurePermissionsFile(dir, "/some/vault")
			Expect(err).NotTo(HaveOccurred(),
				"unwritable dir downgrades to a warning so boot is not blocked")

			_, statErr := os.Stat(filepath.Join(dir, "permissions.yaml"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
			Expect(buf.String()).To(ContainSubstring("level=WARN"))
			Expect(buf.String()).To(ContainSubstring("not writable"))
		})
	})
})

// XDG_CONFIG_HOME isolation guard: the bootstrap calls
// EnsurePermissionsFile(config.Dir(), ...) on every app.New, and
// config.Dir() is the single seam that resolves XDG_CONFIG_HOME ahead
// of the ~/.config fallback. Without this guard a future refactor that
// hardcodes ~/.config/flowstate (bypassing Dir()) would silently leak
// the bootstrap into operators' real config dirs whenever they ran
// tests or smokes against a temp XDG. A real leak surfaced during the
// Slice C live verification: a smoke that set XDG_CONFIG_HOME for one
// invocation, then ran a follow-up without it, wrote permissions.yaml
// into ~/.config/flowstate/. The fix surface is awareness — pin the
// invariant in a test so any regression of Dir()'s XDG behaviour fails
// loudly here rather than silently mutating $HOME for every developer
// and CI worker.
var _ = Describe("XDG_CONFIG_HOME isolation", func() {
	It("routes EnsurePermissionsFile via config.Dir() into the XDG tempdir, never into ~/.config/flowstate", func() {
		xdgDir := GinkgoT().TempDir()
		os.Setenv("XDG_CONFIG_HOME", xdgDir)
		DeferCleanup(func() { os.Unsetenv("XDG_CONFIG_HOME") })

		resolved := config.Dir()
		Expect(resolved).To(Equal(filepath.Join(xdgDir, "flowstate")),
			"config.Dir() must resolve to the XDG tempdir — any bypass leaks the bootstrap into the operator's real ~/.config/flowstate")

		Expect(os.MkdirAll(resolved, 0o755)).To(Succeed())
		err := config.EnsurePermissionsFile(resolved, "/tmp/smoke-vault-root")
		Expect(err).NotTo(HaveOccurred())

		// Wrote into the tempdir.
		_, statErr := os.Stat(filepath.Join(resolved, "permissions.yaml"))
		Expect(statErr).NotTo(HaveOccurred(),
			"the bootstrap must materialise permissions.yaml under the XDG tempdir")

		// Did NOT write into the operator's real ~/.config/flowstate.
		// This is the exact path the Slice C smoke leak landed on.
		homeDir, hErr := os.UserHomeDir()
		Expect(hErr).NotTo(HaveOccurred())
		realDir := filepath.Join(homeDir, ".config", "flowstate")
		Expect(resolved).NotTo(Equal(realDir),
			"sanity: tempdir XDG must not resolve to the operator's real config dir")
	})
})

var _ = Describe("LoadDefaultPermissions", func() {
	It("parses the embedded default with vault-path substitution", func() {
		perms, err := config.LoadDefaultPermissions("/vault")
		Expect(err).NotTo(HaveOccurred())
		Expect(perms).NotTo(BeNil())
		Expect(perms.Version).To(Equal(1))
		Expect(perms.Tools["bash"].Deny).To(ContainElement("/vault/.obsidian/**"))
		Expect(perms.Tools["read"].Allow).To(ContainElement("/vault/**"))
		Expect(perms.Tools["write"].Deny).To(ContainElement("/vault/.obsidian/**"))
		Expect(perms.Tools["edit"].Allow).To(ContainElement("/vault/**"))
		Expect(perms.Tools["multiedit"].Allow).To(ContainElement("/vault/**"))
		Expect(perms.Tools["apply_patch"].Allow).To(ContainElement("/vault/**"))
	})
})
