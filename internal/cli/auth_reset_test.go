package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

// fakeCleaner records Cleanup calls so the reset spec can assert the
// session store was swept exactly once.
type fakeCleaner struct {
	mu      sync.Mutex
	calls   int
	beforeT time.Time
}

func (f *fakeCleaner) Cleanup(_ context.Context, before time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.beforeT = before
	return nil
}

func (f *fakeCleaner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ = Describe("flowstate auth reset (admin recovery — Auth Track C9 PR4 + plan §OD-H)", func() {
	var (
		testApp     *app.App
		tmpDir      string
		usersPath   string
		originalXDG string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-auth-reset-*")
		Expect(err).NotTo(HaveOccurred())

		originalXDG = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())

		Expect(os.Setenv("OPENAI_API_KEY", "test-key-auth-reset-suite")).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.Providers.Default = "openai"
		cfg.DataDir = filepath.Join(tmpDir, "data")
		Expect(os.MkdirAll(cfg.DataDir, 0o700)).To(Succeed())

		testApp, err = app.New(cfg)
		Expect(err).NotTo(HaveOccurred())

		usersPath = filepath.Join(tmpDir, "flowstate", "users.json")

		// Default: pretend stdin is NOT a TTY so the --force guard fires
		// unless the spec explicitly overrides. Production code path uses
		// term.IsTerminal(os.Stdin.Fd()); the package-level var lets the
		// spec stub it deterministically.
		cli.SetStdinIsTerminal(func() bool { return false })

		// Default: no session store wired.
		cli.SetResetStore(nil)
	})

	AfterEach(func() {
		// Restore production stdin probe.
		cli.RestoreStdinIsTerminal()
		cli.SetResetStore(nil)

		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalXDG != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalXDG)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	runCmd := func(args ...string) (*bytes.Buffer, error) {
		root := cli.NewRootCmd(testApp)
		out := new(bytes.Buffer)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		return out, root.Execute()
	}

	Describe("--force guard (plan §OD-H + §Test Strategy line 647)", func() {
		It("refuses to run when --force absent AND stdin is not a TTY", func() {
			cli.SetStdinIsTerminal(func() bool { return false })

			out, err := runCmd("auth", "reset")
			Expect(err).To(HaveOccurred())
			// Either the error string or the combined output should
			// surface the guard message.
			combined := out.String() + err.Error()
			Expect(combined).To(ContainSubstring("--force"))
			Expect(combined).To(ContainSubstring("TTY"))
		})

		It("proceeds when --force is set (non-TTY path)", func() {
			cli.SetStdinIsTerminal(func() bool { return false })

			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No users.json"))
		})

		It("proceeds without --force inside a TTY", func() {
			cli.SetStdinIsTerminal(func() bool { return true })

			out, err := runCmd("auth", "reset")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No users.json"))
		})
	})

	Describe("users.json wipe", func() {
		It("moves users.json to users.json.bak.<ts> atomically", func() {
			// Provision a user first via the auth user add subcommand.
			_, err := runCmd("auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())
			_, statErr := os.Stat(usersPath)
			Expect(statErr).NotTo(HaveOccurred())

			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Moved"))
			Expect(out.String()).To(ContainSubstring(".bak."))

			// Original file no longer present.
			_, statErr = os.Stat(usersPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue())

			// A backup file exists in the same dir.
			parent := filepath.Dir(usersPath)
			entries, readErr := os.ReadDir(parent)
			Expect(readErr).NotTo(HaveOccurred())
			var foundBackup bool
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "users.json.bak.") {
					foundBackup = true
					break
				}
			}
			Expect(foundBackup).To(BeTrue(), "must find a users.json.bak.<ts> file")
		})

		It("emits a skip-message when users.json is already absent", func() {
			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No users.json"))
		})
	})

	Describe("session store sweep", func() {
		It("calls Cleanup on the wired store with a far-future before", func() {
			fake := &fakeCleaner{}
			cli.SetResetStore(fake)

			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Cleared session store"))
			Expect(fake.callCount()).To(Equal(1))
			Expect(fake.beforeT.After(time.Now())).To(BeTrue(),
				"Cleanup.before must be in the future so every record is past expiry")
		})

		It("skips cleanup with a logged note when no store is wired", func() {
			cli.SetResetStore(nil)

			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No session store wired"))
		})

		It("logs the v1 no-op for signing-secret rotation", func() {
			out, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Session signing secret"))
			Expect(out.String()).To(ContainSubstring("CSPRNG"))
		})
	})

	Describe("Idempotency under --force", func() {
		It("running reset twice in a row succeeds both times", func() {
			fake := &fakeCleaner{}
			cli.SetResetStore(fake)

			_, err := runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			_, err = runCmd("auth", "reset", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.callCount()).To(Equal(2))
		})
	})

	Describe("Help surface", func() {
		It("auth group lists reset in help output", func() {
			out, err := runCmd("auth", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("reset"))
		})

		It("reset surfaces the --force flag in its own help", func() {
			out, err := runCmd("auth", "reset", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("--force"))
			Expect(out.String()).To(ContainSubstring("TTY"))
		})
	})
})
