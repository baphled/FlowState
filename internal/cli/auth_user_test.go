package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/crypto/bcrypt"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

// usersFileShape mirrors the on-disk JSON for assertion purposes. Re-declared
// here (rather than imported from internal/cli) to keep the test file in the
// _test external package — matches the existing auth_test.go pattern.
type usersFileShape struct {
	Users []struct {
		Username     string    `json:"username"`
		PasswordHash string    `json:"password_hash"`
		DisplayName  string    `json:"display_name,omitempty"`
		CreatedAt    time.Time `json:"created_at"`
	} `json:"users"`
}

var _ = Describe("flowstate auth user (multi-user provisioning — Auth Track C9 PR4)", func() {
	var (
		testApp     *app.App
		tmpDir      string
		usersPath   string
		originalXDG string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-auth-user-*")
		Expect(err).NotTo(HaveOccurred())

		originalXDG = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())

		// app.New requires a default-provider key; mirror auth_test.go.
		Expect(os.Setenv("OPENAI_API_KEY", "test-key-auth-user-suite")).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.Providers.Default = "openai"
		cfg.DataDir = filepath.Join(tmpDir, "data")
		Expect(os.MkdirAll(cfg.DataDir, 0o700)).To(Succeed())

		testApp, err = app.New(cfg)
		Expect(err).NotTo(HaveOccurred())

		usersPath = filepath.Join(tmpDir, "flowstate", "users.json")
	})

	AfterEach(func() {
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalXDG != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalXDG)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	readUsers := func() usersFileShape {
		raw, err := os.ReadFile(usersPath)
		Expect(err).NotTo(HaveOccurred())
		var parsed usersFileShape
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
		return parsed
	}

	runCmd := func(stdin string, args ...string) (*bytes.Buffer, error) {
		root := cli.NewRootCmd(testApp)
		out := new(bytes.Buffer)
		root.SetOut(out)
		root.SetErr(out)
		if stdin != "" {
			root.SetIn(strings.NewReader(stdin))
		}
		root.SetArgs(args)
		err := root.Execute()
		return out, err
	}

	Describe("auth user add", func() {
		It("hashes the password with bcrypt and writes users.json atomically (plan §Test Strategy line 643)", func() {
			out, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`Added user "alice"`))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1))
			Expect(users.Users[0].Username).To(Equal("alice"))
			Expect(users.Users[0].PasswordHash).NotTo(BeEmpty())
			Expect(strings.HasPrefix(users.Users[0].PasswordHash, "$2")).To(BeTrue(),
				"password_hash must be a bcrypt blob")
			Expect(users.Users[0].CreatedAt).NotTo(BeZero())

			// Hash verifies the password.
			Expect(bcrypt.CompareHashAndPassword(
				[]byte(users.Users[0].PasswordHash),
				[]byte("wonderland"),
			)).To(Succeed())

			// File permission discipline (plan §OD-F: 0600).
			info, statErr := os.Stat(usersPath)
			Expect(statErr).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})

		It("reads password from stdin when --password is omitted", func() {
			out, err := runCmd("hunter2\n", "auth", "user", "add", "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`Enter password for "bob":`))
			Expect(out.String()).To(ContainSubstring(`Added user "bob"`))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1))
			Expect(bcrypt.CompareHashAndPassword(
				[]byte(users.Users[0].PasswordHash),
				[]byte("hunter2"),
			)).To(Succeed())
		})

		It("rejects duplicate username without --force", func() {
			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())

			out, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland2")
			Expect(err).To(HaveOccurred())
			Expect(out.String() + err.Error()).To(ContainSubstring("already exists"))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1))
			// Hash still verifies original password (no clobber).
			Expect(bcrypt.CompareHashAndPassword(
				[]byte(users.Users[0].PasswordHash),
				[]byte("wonderland"),
			)).To(Succeed())
		})

		It("rotates the password hash on duplicate when --force is set", func() {
			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())

			out, err := runCmd("", "auth", "user", "add", "alice", "--password", "rotated", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`Rotated password for "alice"`))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1))
			// Old password no longer verifies.
			Expect(bcrypt.CompareHashAndPassword(
				[]byte(users.Users[0].PasswordHash),
				[]byte("wonderland"),
			)).NotTo(Succeed())
			// New password does.
			Expect(bcrypt.CompareHashAndPassword(
				[]byte(users.Users[0].PasswordHash),
				[]byte("rotated"),
			)).To(Succeed())
		})

		It("creates the parent directory when users.json is missing", func() {
			// app.New seeds ${XDG_CONFIG_HOME}/flowstate during bootstrap so
			// the parent dir typically already exists; rebuild a vacuum to
			// pin the auth-user-add path's own MkdirAll behaviour.
			Expect(os.RemoveAll(filepath.Dir(usersPath))).To(Succeed())
			_, statErr := os.Stat(filepath.Dir(usersPath))
			Expect(os.IsNotExist(statErr)).To(BeTrue())

			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())

			info, statErr := os.Stat(filepath.Dir(usersPath))
			Expect(statErr).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})

		It("stores display_name when supplied", func() {
			_, err := runCmd("", "auth", "user", "add", "alice",
				"--password", "wonderland",
				"--display-name", "Alice Operator")
			Expect(err).NotTo(HaveOccurred())

			users := readUsers()
			Expect(users.Users[0].DisplayName).To(Equal("Alice Operator"))
		})
	})

	Describe("auth user list", func() {
		It("emits a tab-separated row per user, redacting the password hash", func() {
			_, err := runCmd("", "auth", "user", "add", "alice",
				"--password", "wonderland",
				"--display-name", "Alice")
			Expect(err).NotTo(HaveOccurred())
			_, err = runCmd("", "auth", "user", "add", "bob", "--password", "hunter2")
			Expect(err).NotTo(HaveOccurred())

			out, err := runCmd("", "auth", "user", "list")
			Expect(err).NotTo(HaveOccurred())

			body := out.String()
			Expect(body).To(ContainSubstring("alice"))
			Expect(body).To(ContainSubstring("Alice"))
			Expect(body).To(ContainSubstring("bob"))
			// Password hashes MUST NEVER appear (plan §Test Strategy line 644).
			Expect(body).NotTo(ContainSubstring("$2"))
		})

		It("emits empty output when users.json is missing (no crash)", func() {
			out, err := runCmd("", "auth", "user", "list")
			Expect(err).NotTo(HaveOccurred())
			// Output may contain the parent command's residual; key
			// invariant is no error and no panic.
			Expect(out.String()).NotTo(ContainSubstring("$2"))
		})

		It("falls back to username when display_name is empty", func() {
			_, err := runCmd("", "auth", "user", "add", "carol", "--password", "p")
			Expect(err).NotTo(HaveOccurred())

			out, err := runCmd("", "auth", "user", "list")
			Expect(err).NotTo(HaveOccurred())
			// One line with carol appearing twice (username + display fallback).
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			Expect(lines).To(HaveLen(1))
			Expect(strings.Count(lines[0], "carol")).To(BeNumerically(">=", 2))
		})
	})

	Describe("auth user remove", func() {
		It("removes an existing user and rewrites users.json atomically", func() {
			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())
			_, err = runCmd("", "auth", "user", "add", "bob", "--password", "hunter2")
			Expect(err).NotTo(HaveOccurred())

			out, err := runCmd("", "auth", "user", "remove", "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`Removed user "alice"`))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1))
			Expect(users.Users[0].Username).To(Equal("bob"))

			info, _ := os.Stat(usersPath)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})

		It("returns an error when removing a non-existent user without --force", func() {
			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())

			_, err = runCmd("", "auth", "user", "remove", "ghost")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))

			users := readUsers()
			Expect(users.Users).To(HaveLen(1)) // alice survives
		})

		It("is idempotent on missing user under --force (plan §Test Strategy line 645)", func() {
			out, err := runCmd("", "auth", "user", "remove", "ghost", "--force")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("no-op"))
		})

		It("is idempotent on a populated file (remove twice without error under --force)", func() {
			_, err := runCmd("", "auth", "user", "add", "alice", "--password", "wonderland")
			Expect(err).NotTo(HaveOccurred())
			_, err = runCmd("", "auth", "user", "remove", "alice")
			Expect(err).NotTo(HaveOccurred())
			_, err = runCmd("", "auth", "user", "remove", "alice", "--force")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("auth user command group help", func() {
		It("registers add, list, remove subcommands", func() {
			out, err := runCmd("", "auth", "user", "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("add"))
			Expect(out.String()).To(ContainSubstring("list"))
			Expect(out.String()).To(ContainSubstring("remove"))
		})
	})
})
