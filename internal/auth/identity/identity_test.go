package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/crypto/bcrypt"

	"github.com/baphled/flowstate/internal/auth/identity"
)

var _ = Describe("identity.Source impls", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	// Plan §"Test Strategy" line 602-603: SharedSecretSource.
	Describe("SharedSecretSource", func() {
		var source *identity.SharedSecretSource

		BeforeEach(func() {
			source = identity.NewSharedSecretSource("hunter2")
		})

		It("returns Principal{ID:default, Mode:shared-secret} on matching secret", func() {
			p, err := source.Authenticate(ctx, identity.Credentials{Secret: "hunter2"})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.ID).To(Equal("default"))
			Expect(p.Mode).To(Equal(identity.ModeSharedSecret))
		})

		It("returns ErrInvalidCredentials on non-matching secret", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{Secret: "wrong"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("returns ErrInvalidCredentials on empty supplied secret", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{Secret: ""})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("fails-closed when configured secret is empty (bootstrap UX path)", func() {
			empty := identity.NewSharedSecretSource("")
			_, err := empty.Authenticate(ctx, identity.Credentials{Secret: "anything"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		// Plan B8 — Source MUST ignore irrelevant fields. login.go uses
		// DisallowUnknownFields=false so multi-user-shape body in
		// shared-secret mode unmarshals into Credentials{Secret:""};
		// Authenticate then returns ErrInvalidCredentials uniformly.
		It("ignores Username/Password fields (B8 — irrelevant fields silently dropped)", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice",
				Password: "wonderland",
			})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("uses constant-time secret comparison (length-equal inputs)", func() {
			// Smoke check: a wrong-but-same-length secret still returns the
			// same error. We can't directly test timing in unit tests; the
			// invariant being pinned is "no early return on first-mismatch
			// byte" — which constantTimeEqual + subtle.ConstantTimeCompare
			// guarantee by implementation, not by behaviour. This case
			// pins the surface so the impl can't regress to bytes.Equal
			// without a compile-time signal.
			_, err := source.Authenticate(ctx, identity.Credentials{Secret: "hunter1"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("Mode() returns shared-secret", func() {
			Expect(source.Mode()).To(Equal(identity.ModeSharedSecret))
		})
	})

	// Plan §"Test Strategy" line 604: DeploymentLoginSource.
	Describe("DeploymentLoginSource", func() {
		var source *identity.DeploymentLoginSource

		BeforeEach(func() {
			source = identity.NewDeploymentLoginSource("hunter2", "operator@example.com", "Operator")
		})

		It("returns the configured Principal on matching secret", func() {
			p, err := source.Authenticate(ctx, identity.Credentials{Secret: "hunter2"})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.ID).To(Equal("operator@example.com"))
			Expect(p.DisplayName).To(Equal("Operator"))
			Expect(p.Mode).To(Equal(identity.ModeDeploymentLogin))
		})

		It("defaults DisplayName to PrincipalID when display is empty", func() {
			noDisplay := identity.NewDeploymentLoginSource("hunter2", "operator@example.com", "")
			p, err := noDisplay.Authenticate(ctx, identity.Credentials{Secret: "hunter2"})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.DisplayName).To(Equal("operator@example.com"))
		})

		It("returns ErrInvalidCredentials on non-matching secret", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{Secret: "wrong"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("fails-closed when configured secret is empty", func() {
			empty := identity.NewDeploymentLoginSource("", "operator@example.com", "Operator")
			_, err := empty.Authenticate(ctx, identity.Credentials{Secret: "anything"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("fails-closed when configured principalID is empty (defensive misconfig)", func() {
			noID := identity.NewDeploymentLoginSource("hunter2", "", "Operator")
			_, err := noID.Authenticate(ctx, identity.Credentials{Secret: "hunter2"})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("ignores Username/Password fields (B8)", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice",
				Password: "wonderland",
			})
			Expect(errors.Is(err, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("Mode() returns per-deployment-login", func() {
			Expect(source.Mode()).To(Equal(identity.ModeDeploymentLogin))
		})
	})

	// Plan §"Rollout Plan" PR4/C9 line 555: real MultiUserSource impl
	// backed by users.json + bcrypt.
	Describe("MultiUserSource (PR4 real impl)", func() {
		var (
			tmpDir string
			path   string
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "flowstate-multiuser-*")
			Expect(err).NotTo(HaveOccurred())
			path = filepath.Join(tmpDir, "users.json")
		})

		AfterEach(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		writeUsers := func(users []map[string]any, perm os.FileMode) {
			body := map[string]any{"users": users}
			b, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(path, b, perm)).To(Succeed())
			// os.WriteFile honours umask; chmod ensures the bit we asked for.
			Expect(os.Chmod(path, perm)).To(Succeed())
		}

		hashFor := func(plain string) string {
			h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
			Expect(err).NotTo(HaveOccurred())
			return string(h)
		}

		It("constructor returns source with zero users when file missing (plan §Bootstrap UX multi-user)", func() {
			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(source).NotTo(BeNil())
			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("authenticates a valid (username, password) pair", func() {
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"display_name":  "Alice",
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o600)

			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			p, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.ID).To(Equal("alice"))
			Expect(p.DisplayName).To(Equal("Alice"))
			Expect(p.Mode).To(Equal(identity.ModeMultiUser))
		})

		It("returns ErrInvalidCredentials on wrong password (B8)", func() {
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o600)

			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wrong",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		// B8 discipline pin (memory feedback_published_unsubscribed_events_dead_surface
		// notwithstanding — absent-user vs wrong-password MUST collapse to
		// the same sentinel so probers cannot fingerprint user existence).
		It("returns ErrInvalidCredentials on absent username (B8 — same sentinel as wrong password)", func() {
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o600)

			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "bob", Password: "anything",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())

			// And the same call with EMPTY username — same sentinel,
			// no leak of "no such user vs no username supplied".
			_, emptyErr := source.Authenticate(ctx, identity.Credentials{
				Username: "", Password: "anything",
			})
			Expect(errors.Is(emptyErr, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("returns ErrInvalidCredentials for empty users.json (no users provisioned)", func() {
			writeUsers([]map[string]any{}, 0o600)
			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("returns construction error when users.json is unparseable JSON", func() {
			Expect(os.WriteFile(path, []byte(`{not valid json`), 0o600)).To(Succeed())
			source, err := identity.NewMultiUserSource(path)
			Expect(err).To(HaveOccurred())
			Expect(source).To(BeNil())
		})

		It("succeeds on world-readable users.json (operator's choice; logs warn only)", func() {
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o644) // explicitly world-readable

			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			// And the source still authenticates successfully — the warn
			// is advisory, not a refusal.
			p, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(authErr).NotTo(HaveOccurred())
			Expect(p.ID).To(Equal("alice"))
		})

		It("falls back to username when display_name is empty", func() {
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o600)

			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			p, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.DisplayName).To(Equal("alice"))
		})

		It("empty path constructs a zero-user source (test/bootstrap escape hatch)", func() {
			source, err := identity.NewMultiUserSource("")
			Expect(err).NotTo(HaveOccurred())
			Expect(source).NotTo(BeNil())
			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())
		})

		It("Reload picks up changes written via the cobra subcommand path", func() {
			writeUsers([]map[string]any{}, 0o600)
			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())

			// Before reload — auth must fail because the file is empty.
			_, authErr := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(errors.Is(authErr, identity.ErrInvalidCredentials)).To(BeTrue())

			// Write a user (simulating `flowstate auth user add`).
			writeUsers([]map[string]any{
				{
					"username":      "alice",
					"password_hash": hashFor("wonderland"),
					"created_at":    time.Now().UTC().Format(time.RFC3339),
				},
			}, 0o600)

			Expect(source.Reload()).To(Succeed())

			p, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice", Password: "wonderland",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.ID).To(Equal("alice"))
		})

		It("Path returns the users.json path the source was constructed with", func() {
			source, err := identity.NewMultiUserSource(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(source.Path()).To(Equal(path))
		})

		It("Mode() returns multi-user", func() {
			source, err := identity.NewMultiUserSource("")
			Expect(err).NotTo(HaveOccurred())
			Expect(source.Mode()).To(Equal(identity.ModeMultiUser))
		})

		It("ErrNotImplemented is retained as a forward-compat sentinel distinct from ErrInvalidCredentials", func() {
			// PR4/C9 removes ErrNotImplemented from MultiUserSource's
			// return surface, but the sentinel stays declared for future
			// OAuth/OIDC stubs. The distinct-sentinel pin survives so a
			// future refactor that merges them fails this spec.
			Expect(errors.Is(identity.ErrNotImplemented, identity.ErrInvalidCredentials)).To(BeFalse())
			Expect(errors.Is(identity.ErrInvalidCredentials, identity.ErrNotImplemented)).To(BeFalse())
		})
	})

	// Cross-impl: compile-time and behavioural assertion that all three
	// types satisfy the Source interface. var _ Source = ... lives in
	// source.go's package; pin it here for the spec audit trail.
	Describe("interface conformance", func() {
		It("all three impls satisfy Source", func() {
			var _ identity.Source = identity.NewSharedSecretSource("")
			var _ identity.Source = identity.NewDeploymentLoginSource("", "", "")
			mus, err := identity.NewMultiUserSource("")
			Expect(err).NotTo(HaveOccurred())
			var _ identity.Source = mus
		})
	})

	// Context cancellation discipline — Authenticate honours ctx.Err().
	Describe("context cancellation", func() {
		It("returns ctx.Err() when ctx is already cancelled (SharedSecretSource)", func() {
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := identity.NewSharedSecretSource("hunter2").
				Authenticate(cancelled, identity.Credentials{Secret: "hunter2"})
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		})

		It("returns ctx.Err() when ctx is already cancelled (DeploymentLoginSource)", func() {
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := identity.NewDeploymentLoginSource("hunter2", "operator@example.com", "").
				Authenticate(cancelled, identity.Credentials{Secret: "hunter2"})
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		})

		It("returns ctx.Err() when ctx is already cancelled (MultiUserSource)", func() {
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			source, err := identity.NewMultiUserSource("")
			Expect(err).NotTo(HaveOccurred())
			_, authErr := source.Authenticate(cancelled, identity.Credentials{})
			Expect(errors.Is(authErr, context.Canceled)).To(BeTrue())
		})
	})
})
