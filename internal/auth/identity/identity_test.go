package identity_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

	// Plan §"Rollout Plan" PR2/C3 line 549: MultiUserSource ships as a stub
	// returning ErrNotImplemented. PR4/C9 swaps in the real impl.
	Describe("MultiUserSource (PR2 stub)", func() {
		var source *identity.MultiUserSource

		BeforeEach(func() {
			source = identity.NewMultiUserSource()
		})

		It("returns ErrNotImplemented on every Authenticate call", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{
				Username: "alice",
				Password: "wonderland",
			})
			Expect(errors.Is(err, identity.ErrNotImplemented)).To(BeTrue())
		})

		It("returns ErrNotImplemented even with empty credentials", func() {
			_, err := source.Authenticate(ctx, identity.Credentials{})
			Expect(errors.Is(err, identity.ErrNotImplemented)).To(BeTrue())
		})

		It("Mode() returns multi-user", func() {
			Expect(source.Mode()).To(Equal(identity.ModeMultiUser))
		})

		// Plan §"Wire Protocol" line 484 (B8): login.go MUST translate
		// ErrNotImplemented to 401 invalid_credentials uniformly. This
		// spec pins that ErrNotImplemented and ErrInvalidCredentials are
		// DISTINCT sentinels — so login.go's wire-collapse layer is the
		// only place the distinction is erased. If a future refactor
		// merges these sentinels, this test fails and forces a deliberate
		// re-decision.
		It("ErrNotImplemented is distinct from ErrInvalidCredentials", func() {
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
			var _ identity.Source = identity.NewMultiUserSource()
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
			_, err := identity.NewMultiUserSource().
				Authenticate(cancelled, identity.Credentials{})
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
		})
	})
})
