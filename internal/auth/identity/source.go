// Package identity defines the per-mode identity source interface used by the
// FlowState auth track. Each deployment mode (shared-secret, per-deployment-login,
// multi-user) selects exactly one Source implementation at boot via cfg.Auth.Mode.
//
// The Source interface lets internal/auth/login.go hand off credential
// validation without knowing which mode is active — that selection happens at
// wiring time. The package is consumer-agnostic: it imports only stdlib +
// internal/auth siblings, no HTTP-server-specific types.
//
// Plan reference: FlowState API Auth Track (May 2026) §"Deployment Modes &
// Auth Modes Matrix" lines 144-172 and §"Rollout Plan" PR2/C3 line 549.
package identity

import (
	"context"
	"crypto/subtle"
	"errors"
)

// ErrInvalidCredentials is returned by every Source.Authenticate impl when the
// supplied Credentials do not match a known principal. Callers (login.go) MUST
// translate this to a uniform `401 invalid_credentials` on the wire — see
// plan §"Wire Protocol" line 484 (B8 — mode-fingerprint defence).
//
// Sentinel; compare with errors.Is.
var ErrInvalidCredentials = errors.New("auth/identity: invalid credentials")

// ErrNotImplemented is returned by MultiUserSource in PR2/C3 (this commit).
// PR4/C9 replaces the stub with a real impl backed by users.json. Returning
// this from a stub keeps the wire-protocol invariant: login.go MUST collapse
// any Authenticate error to a uniform 401, so the wire response shape does
// not change between PR2 (stub) and PR4 (real impl).
//
// Sentinel; compare with errors.Is.
var ErrNotImplemented = errors.New("auth/identity: source not implemented in this build")

// Mode constants pin the three deployment modes. Centralising the strings
// here means callers never mistype a mode value; the AuthConfig wiring in
// PR3 reads these constants directly.
const (
	ModeSharedSecret       = "shared-secret"
	ModeDeploymentLogin    = "per-deployment-login"
	ModeMultiUser          = "multi-user"
)

// Principal is the post-authentication identity returned by Source.Authenticate.
// Opaque to the middleware layer — the SessionManager stamps PrincipalID onto
// the Record for later lookup via Manager.Authenticate(r).
//
// Plan reference: §"Deployment Modes" line 154-158 — three modes, three
// principal shapes, one wire surface.
type Principal struct {
	// ID is the identity-source-provided principal id. Shapes by mode:
	//   - shared-secret      → always "default"
	//   - per-deployment-login → operator-configured id (e.g. "operator@example.com")
	//   - multi-user         → username (PR4)
	ID string

	// DisplayName is the human-readable label rendered in the UI. Optional;
	// callers default to ID when empty.
	DisplayName string

	// Mode is one of the Mode* constants above. Stamped onto Record.Mode at
	// session mint; checked at RequireSession for mode-mismatch (round-5 B3
	// fold — operator flips auth.mode between deployments, old sessions
	// invalidated). Plan reference: §"What this plan delivers" line 124.
	Mode string
}

// Credentials carries the union of all mode-shaped login bodies. login.go's
// parseCredentials unmarshals the request body into this struct using
// DisallowUnknownFields=false (plan §"Wire Protocol" line 482 — B8 fix), so
// extra fields do NOT trigger a 400-with-field-name leak.
//
// Each Source.Authenticate impl reads only the fields relevant to its mode:
// SharedSecretSource + DeploymentLoginSource read Secret; MultiUserSource
// reads Username+Password. Unused fields are ignored.
type Credentials struct {
	Secret   string // shared-secret + per-deployment-login modes
	Username string // multi-user mode
	Password string // multi-user mode
}

// Source is the per-mode identity validator. One impl selected at boot;
// login.go threads it through HandleLogin.
//
// Implementations MUST:
//   - Return ErrInvalidCredentials on any auth failure (login.go maps to 401).
//   - Be safe for concurrent use across request goroutines.
//   - Honour ctx cancellation when the underlying lookup is non-trivial
//     (bcrypt compare, file IO). The two v1 impls here are cheap enough that
//     ctx is checked once at entry.
type Source interface {
	// Authenticate validates creds and returns the matching Principal on
	// success. Returns ErrInvalidCredentials on failure; MAY return
	// ErrNotImplemented (MultiUserSource stub).
	Authenticate(ctx context.Context, creds Credentials) (Principal, error)

	// Mode returns the deployment mode this Source implements. Used by
	// login.go to stamp Principal.Mode + Record.Mode consistently and by
	// the parseCredentials path to verify the request body matches the
	// active mode's shape.
	Mode() string
}

// SharedSecretSource is the dev-convenience mode (plan §"Deployment Modes"
// row 1). One configured secret; matching the secret returns
// Principal{ID:"default", Mode:"shared-secret"}.
//
// Constant-time compare is intentional: timing-attack defence on the
// shared secret. Empty Secret in cfg is treated as "no valid secret
// possible" — every Authenticate call returns ErrInvalidCredentials.
type SharedSecretSource struct {
	secret string
}

// NewSharedSecretSource constructs a SharedSecretSource bound to the
// configured secret. Pass cfg.Auth.SharedSecret.Secret here (plan
// §"Config shape" line 189-190).
func NewSharedSecretSource(secret string) *SharedSecretSource {
	return &SharedSecretSource{secret: secret}
}

// Authenticate compares creds.Secret with the configured secret in
// constant time. Returns Principal{ID:"default", Mode:"shared-secret"}
// on match; ErrInvalidCredentials otherwise.
//
// Empty configured secret means "no login possible in this mode" — every
// call returns ErrInvalidCredentials. This is the safe failure mode
// when bootstrap UX (plan line 700-703) hasn't been completed.
func (s *SharedSecretSource) Authenticate(ctx context.Context, creds Credentials) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if s.secret == "" {
		return Principal{}, ErrInvalidCredentials
	}
	if !constantTimeEqual(creds.Secret, s.secret) {
		return Principal{}, ErrInvalidCredentials
	}
	return Principal{
		ID:          "default",
		DisplayName: "default",
		Mode:        ModeSharedSecret,
	}, nil
}

// Mode returns ModeSharedSecret.
func (s *SharedSecretSource) Mode() string { return ModeSharedSecret }

// DeploymentLoginSource is the default online single-operator mode (plan
// §"Deployment Modes" row 2). One configured secret, plus a configured
// principal id and display name carried onto Principal/Record. The
// difference from SharedSecretSource is the deployment has an identity
// — the operator can put their email or org id in config.
type DeploymentLoginSource struct {
	secret      string
	principalID string
	displayName string
}

// NewDeploymentLoginSource constructs a DeploymentLoginSource. Pass
// cfg.Auth.DeploymentLogin.Secret/PrincipalID/(optional DisplayName) here
// (plan §"Config shape" line 192-195).
func NewDeploymentLoginSource(secret, principalID, displayName string) *DeploymentLoginSource {
	return &DeploymentLoginSource{
		secret:      secret,
		principalID: principalID,
		displayName: displayName,
	}
}

// Authenticate compares creds.Secret with the configured secret in
// constant time. Returns the configured Principal on match;
// ErrInvalidCredentials otherwise.
//
// PrincipalID is required by the constructor's caller — empty
// principal_id with a non-empty secret would let the deployer ship
// a credential with no identity. Defensive: empty principal_id ALSO
// returns ErrInvalidCredentials so a misconfig fails closed.
func (d *DeploymentLoginSource) Authenticate(ctx context.Context, creds Credentials) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if d.secret == "" || d.principalID == "" {
		return Principal{}, ErrInvalidCredentials
	}
	if !constantTimeEqual(creds.Secret, d.secret) {
		return Principal{}, ErrInvalidCredentials
	}
	display := d.displayName
	if display == "" {
		display = d.principalID
	}
	return Principal{
		ID:          d.principalID,
		DisplayName: display,
		Mode:        ModeDeploymentLogin,
	}, nil
}

// Mode returns ModeDeploymentLogin.
func (d *DeploymentLoginSource) Mode() string { return ModeDeploymentLogin }

// MultiUserSource is the PR2 stub for multi-user mode (plan §"Rollout Plan"
// PR2/C3 line 549 — "lands as a stub here (compile-clean, returns not
// implemented); real impl in PR4/C7"). The constructor compiles so PR3
// can wire it through setupRoutes; first Authenticate call returns
// ErrNotImplemented.
//
// login.go's B8 fix collapses this error to `401 invalid_credentials` on
// the wire, so the stub period is invisible to unauthenticated probers:
// a multi-user-mode server with the PR2 stub looks identical on the wire
// to one with the PR4 real impl returning ErrInvalidCredentials.
type MultiUserSource struct{}

// NewMultiUserSource constructs the PR2 stub. PR4/C9 replaces the impl
// body without changing this constructor signature; the wiring in
// setupRoutes stays unchanged across the v1→v1.x boundary.
func NewMultiUserSource() *MultiUserSource { return &MultiUserSource{} }

// Authenticate returns ErrNotImplemented. PR4/C9 swap-in.
//
// login.go MUST translate this to the same `401 invalid_credentials`
// shape as ErrInvalidCredentials — see plan line 484 (B8). The stub
// existing on the wire is invisible to probers.
func (*MultiUserSource) Authenticate(ctx context.Context, _ Credentials) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	return Principal{}, ErrNotImplemented
}

// Mode returns ModeMultiUser.
func (*MultiUserSource) Mode() string { return ModeMultiUser }

// constantTimeEqual is a constant-time string compare wrapping
// crypto/subtle.ConstantTimeCompare. Length-mismatch returns false
// without performing the byte-level compare — the leaked timing signal
// is "secret has a different length than supplied input", which is the
// standard accepted v1 trade-off (full length-tolerant constant-time
// compare would require allocating a max-length pad; not worth it given
// the secret is operator-configured, not user-input length-variable).
//
// Centralised in one named function so the spec can audit the
// timing-attack defence in one place.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
