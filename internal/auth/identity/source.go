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
// Auth Modes Matrix" lines 144-172 and §"Rollout Plan" PR2/C3 line 549 +
// PR4/C9 line 555.
package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is returned by every Source.Authenticate impl when the
// supplied Credentials do not match a known principal. Callers (login.go) MUST
// translate this to a uniform `401 invalid_credentials` on the wire — see
// plan §"Wire Protocol" line 484 (B8 — mode-fingerprint defence).
//
// B8 discipline (PR4/C9): MultiUserSource returns this sentinel for BOTH
// absent username AND wrong-password cases. The wire response shape is
// byte-identical so probers cannot fingerprint user existence.
//
// Sentinel; compare with errors.Is.
var ErrInvalidCredentials = errors.New("auth/identity: invalid credentials")

// ErrNotImplemented is retained as a sentinel for forward compatibility with
// future Source impls (e.g. OAuth/OIDC v2 stubs). MultiUserSource no longer
// returns it as of PR4/C9; SharedSecretSource and DeploymentLoginSource never
// did. login.go's B8 wire-collapse still translates this to the same 401
// shape as ErrInvalidCredentials.
//
// Sentinel; compare with errors.Is.
var ErrNotImplemented = errors.New("auth/identity: source not implemented in this build")

// Mode constants pin the three deployment modes. Centralising the strings
// here means callers never mistype a mode value; the AuthConfig wiring in
// PR3 reads these constants directly.
const (
	ModeSharedSecret    = "shared-secret"
	ModeDeploymentLogin = "per-deployment-login"
	ModeMultiUser       = "multi-user"
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
//     (bcrypt compare, file IO). bcrypt compare in MultiUserSource is
//     ~50-100ms at cost 12; ctx is checked at entry.
type Source interface {
	// Authenticate validates creds and returns the matching Principal on
	// success. Returns ErrInvalidCredentials on any failure (including
	// absent users — B8 discipline; PR4/C9).
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

// MultiUserSource is the multi-user identity source (plan §"Deployment Modes"
// row 3 + §"Rollout Plan" PR4/C9 line 555). Reads bcrypt-hashed user records
// from a JSON file at ${XDG_CONFIG_HOME}/flowstate/users.json (the path is
// caller-controlled — see NewMultiUserSource).
//
// File format (plan §OD-F):
//
//	{
//	  "users": [
//	    {
//	      "username": "alice",
//	      "password_hash": "$2a$12$...",
//	      "display_name": "Alice Operator",
//	      "created_at": "2026-05-13T12:00:00Z"
//	    }
//	  ]
//	}
//
// Atomic-write discipline: writes via the cobra subcommands route through
// internal/atomicwrite.File — temp+rename+fsync so a crash mid-write never
// truncates users.json.
//
// B8 discipline (plan §"Wire Protocol" line 484):
//
//	Authenticate returns ErrInvalidCredentials for BOTH absent username AND
//	wrong-password cases. login.go's wire-collapse layer keeps the response
//	byte-identical so probers cannot fingerprint user existence.
//
// Empty users.json (missing file OR zero users) is valid bootstrap state
// per plan §"Bootstrap UX" multi-user — server starts, every login fails
// closed, slog.Warn surfaces the misconfig to the operator. The cobra
// `flowstate auth user add <name>` command provisions users out-of-band
// (plan §OD-G — no self-signup endpoint).
type MultiUserSource struct {
	path string

	mu    sync.RWMutex
	users map[string]userEntry // username → entry
}

// userEntry is the in-memory shape of one row in users.json. JSON tags
// match the on-disk format so the cobra commands and the auth path share
// one source of truth.
type userEntry struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	DisplayName  string    `json:"display_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// usersFile is the on-disk container shape. A nested array under "users"
// reserves room for future top-level fields (e.g. file-format version,
// last-rotated timestamp) without an on-disk migration.
type usersFile struct {
	Users []userEntry `json:"users"`
}

// NewMultiUserSource constructs a MultiUserSource bound to path. The file
// is loaded once at construction time and cached in memory; subsequent
// Authenticate calls hit the cache, not the disk.
//
// Behaviour:
//   - path == ""                  → ErrInvalidCredentials on every call.
//     Use this for tests / bootstrap where users.json is intentionally
//     absent (matches B8 discipline — no leak of "no path configured" vs
//     "user not found").
//   - file does not exist         → constructor returns OK with zero users.
//     Server starts; every login fails closed. Plan §"Bootstrap UX" multi-user.
//   - file world-readable (>0600) → slog.Warn surfaced but constructor
//     succeeds. Operator's choice; not fatal.
//   - file unparseable JSON       → constructor returns error so the
//     operator sees the misconfig at boot, not at first login.
//
// Concurrent-safe for Authenticate. Provisioning (add / remove via the
// cobra commands) writes via atomicwrite.File; in-process the source
// re-reads via Reload (PR4/C9).
func NewMultiUserSource(path string) (*MultiUserSource, error) {
	m := &MultiUserSource{
		path:  path,
		users: map[string]userEntry{},
	}
	if path == "" {
		return m, nil
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// load reads m.path and replaces the in-memory cache. Missing file is OK
// (zero users). Unparseable file is an error.
//
// World-readable check: stat the file; if perm bits beyond 0o600, log a
// slog.Warn (advisory — the operator may have chosen to share the file).
func (m *MultiUserSource) load() error {
	info, statErr := os.Stat(m.path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			m.mu.Lock()
			m.users = map[string]userEntry{}
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("auth/identity: stat users file %q: %w", m.path, statErr)
	}

	// 0o600 = read+write for owner only. Anything broader is a misconfig
	// signal — log but proceed.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		slog.Warn("multi_user_users_json_world_readable",
			"path", m.path,
			"perm", fmt.Sprintf("%#o", perm),
		)
	}

	data, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("auth/identity: read users file %q: %w", m.path, err)
	}

	// Empty file is treated as zero users (matches missing-file behaviour).
	if len(data) == 0 {
		m.mu.Lock()
		m.users = map[string]userEntry{}
		m.mu.Unlock()
		return nil
	}

	var parsed usersFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("auth/identity: parse users file %q: %w", m.path, err)
	}

	next := make(map[string]userEntry, len(parsed.Users))
	for _, u := range parsed.Users {
		if u.Username == "" {
			continue
		}
		next[u.Username] = u
	}

	m.mu.Lock()
	m.users = next
	m.mu.Unlock()
	return nil
}

// Reload re-reads users.json from disk. Cobra subcommands (`auth user add`,
// `auth user remove`) call this after an atomic-write so the server
// (post-PR5 default-on) sees the change without a restart. In-process
// callers can also invoke it on SIGHUP.
//
// Returns the same errors as the constructor's load step. Concurrent with
// Authenticate via m.mu.
func (m *MultiUserSource) Reload() error {
	if m.path == "" {
		return nil
	}
	return m.load()
}

// Path returns the users.json path the source was constructed with.
// Cobra subcommands use this to locate the file when the source is
// already constructed (e.g. when sharing one source between the serve
// process and an admin command via Reload).
func (m *MultiUserSource) Path() string { return m.path }

// Authenticate validates creds.Username + creds.Password against the
// cached users.json.
//
// Error contract (B8 discipline):
//   - ctx cancelled                  → ctx.Err()
//   - username == "" OR not found    → ErrInvalidCredentials
//   - bcrypt.CompareHashAndPassword
//     returns ErrMismatchedHash...   → ErrInvalidCredentials
//   - bcrypt returns any other error → ErrInvalidCredentials
//     (logged via slog.Warn so the operator sees corrupt-hash signals)
//   - success                        → Principal{ID:username, ...}
//
// The same sentinel for absent-user and wrong-password is non-negotiable
// (memory feedback_published_unsubscribed_events_dead_surface notwithstanding
// — this is the B8 fingerprint defence). Distinguishing the two would
// leak user-existence to probers.
func (m *MultiUserSource) Authenticate(ctx context.Context, creds Credentials) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if creds.Username == "" {
		return Principal{}, ErrInvalidCredentials
	}

	m.mu.RLock()
	entry, found := m.users[creds.Username]
	m.mu.RUnlock()
	if !found {
		// B8 — same sentinel as wrong-password below.
		return Principal{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword(
		[]byte(entry.PasswordHash),
		[]byte(creds.Password),
	); err != nil {
		if !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			// Corrupt hash, version-too-new, etc. Log so the operator sees
			// the misconfig; still collapse to invalid_credentials on the
			// wire (B8).
			slog.Warn("multi_user_bcrypt_compare_failed",
				"username", creds.Username,
				"err", err.Error(),
			)
		}
		return Principal{}, ErrInvalidCredentials
	}

	display := entry.DisplayName
	if display == "" {
		display = entry.Username
	}
	return Principal{
		ID:          entry.Username,
		DisplayName: display,
		Mode:        ModeMultiUser,
	}, nil
}

// Mode returns ModeMultiUser.
func (m *MultiUserSource) Mode() string { return ModeMultiUser }

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
