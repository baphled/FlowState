package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/baphled/flowstate/internal/auth/identity"
	"github.com/baphled/flowstate/internal/auth/store"
)

// SessionConfig holds the cookie + lifetime configuration for the session
// manager. v1 sources this from cfg.Auth in the config layer; PR3 wires
// it through. Defaults per the plan §"Wire Protocol" lines 408-417.
type SessionConfig struct {
	// CookieName is the session cookie's Name attribute. Plan default
	// "flowstate_session".
	CookieName string

	// CookiePath is the cookie's Path attribute. Plan default "/api".
	CookiePath string

	// SecureCookies controls the Secure attribute. Defaults to true in
	// production (cfg.Auth.SecureCookies). cfg.Auth.SecureCookies = false
	// is only valid for HTTP dev (plan §"Wire Protocol" line 414, R2/R12).
	SecureCookies bool

	// Lifetime is the session's absolute expiry; cookie Max-Age and
	// Record.ExpiresAt are both stamped from this. Plan OD-B: 168h (7d).
	Lifetime time.Duration

	// Mode is the active deployment mode. Stamped onto Record.Mode at mint
	// so RequireSession can detect mode-mismatch (round-5 B3 fold).
	Mode string

	// Now is a clock injection point for deterministic tests. Production
	// callers leave it nil; SessionManager falls back to time.Now.
	Now func() time.Time
}

// DefaultSessionConfig returns the production defaults defined by the plan
// §"Wire Protocol" lines 408-417 and OD-B. Callers MUST set Mode after
// taking the defaults — there is no sensible Mode default.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		CookieName:    "flowstate_session",
		CookiePath:    "/api",
		SecureCookies: true,
		Lifetime:      168 * time.Hour, // OD-B: 7d
	}
}

// SessionManager mints, looks up, and revokes server-side session records.
//
// Design (plan §"What this plan delivers" line 120):
//
//   - Wraps github.com/alexedwards/scs/v2's SessionManager for cookie
//     attribute discipline (Set-Cookie/HttpOnly/Secure/SameSite/Path/
//     MaxAge), CSPRNG token generation, and ctx-threading.
//
//   - scs/v2's `Store` interface (Find/Commit/Delete on []byte blobs) is
//     adapted onto OUR store.Store (Record-shaped) via scsStoreAdapter
//     below. The adapter preserves the plan's load-bearing Record fields
//     (Mode + PrincipalID + CSRFToken + structured timestamps) so
//     RequireSession's mode-mismatch check (round-5 B3 fold) and the
//     CSRF wrapper's Record-bound check (PR2/C5) can read them
//     structurally rather than gob-round-tripping.
//
//   - Begin/End own cookie writes via scs's WriteSessionCookie semantics
//     (mirrored locally) so the composition order in RequireSession
//     stays in OUR control. We deliberately do NOT use scs's
//     LoadAndSave middleware — that would create a second middleware
//     layer competing with the plan's fixed Origin → Session → CSRF
//     ordering (plan §"What this plan delivers" line 124).
//
// SessionManager is safe for concurrent use across request goroutines.
type SessionManager struct {
	cfg   SessionConfig
	store store.Store
	scs   *scs.SessionManager
}

// NewSessionManager constructs a SessionManager bound to cfg and the
// configured store backend. cfg.Mode MUST be set by the caller (no default).
//
// Internally constructs an scs.SessionManager configured to match cfg's
// cookie attributes, with scs.Store wired through to OUR store.Store via
// scsStoreAdapter. The scs manager is the cookie attr + Set-Cookie engine;
// our store.Store is the authoritative Record source-of-truth.
func NewSessionManager(s store.Store, cfg SessionConfig) *SessionManager {
	sm := scs.New()
	sm.Lifetime = cfg.Lifetime
	sm.Cookie = scs.SessionCookie{
		Name:     cfg.CookieName,
		Path:     cfg.CookiePath,
		HttpOnly: true,
		Secure:   cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Persist:  true,
	}
	sm.Store = &scsStoreAdapter{inner: s, mode: cfg.Mode}

	return &SessionManager{cfg: cfg, store: s, scs: sm}
}

// ErrSessionExpired is returned by Authenticate when the cookie's Record
// has aged past its ExpiresAt. Wire surface returns 401 (plan §"Wire
// Protocol" line 537).
//
// Sentinel; compare with errors.Is.
var ErrSessionExpired = errors.New("auth: session expired")

// ErrSessionInvalid is returned by Authenticate when the cookie is missing,
// the token is not present in the store, or the lookup hits a backend
// error. Wire surface returns 401.
//
// Sentinel; compare with errors.Is.
var ErrSessionInvalid = errors.New("auth: session invalid")

// ErrSessionModeMismatch is returned by Authenticate when the looked-up
// Record's Mode does not match cfg.Mode. Operator flipped auth.mode
// between deployments and an old session survived (round-5 B3 fold).
//
// Wire surface returns 401 + slog.Warn("auth mode changed; session
// invalidated") — see middleware.go's RequireSession composition.
//
// Sentinel; compare with errors.Is.
var ErrSessionModeMismatch = errors.New("auth: session mode mismatch")

// Begin mints a fresh session for principal, persists the Record to the
// store, and writes the Set-Cookie header on w. Returns the opaque token
// (also written into the cookie value) and the freshly-minted CSRF token.
//
// Cookie attributes per plan §"Wire Protocol" lines 408-417:
//   - Name        = cfg.CookieName
//   - Path        = cfg.CookiePath
//   - HttpOnly    = true (hard-coded — never disabled)
//   - Secure      = cfg.SecureCookies
//   - SameSite    = Lax
//   - Max-Age     = cfg.Lifetime in seconds
//   - Value       = 256-bit random opaque token (base64 URL-safe; matches
//                   scs's RandomBytes(32) token-generation idiom)
//
// principal.Mode is stamped onto the Record so RequireSession's
// mode-mismatch check (round-5 B3) has the necessary anchor.
//
// Returns the session token (cookie value). The CSRF token lives on
// Record.CSRFToken in the store; login.go reads it back via the
// Record-shaped Get and writes it into the response body.
func (m *SessionManager) Begin(w http.ResponseWriter, r *http.Request, principal identity.Principal) (string, error) {
	token, err := mintToken()
	if err != nil {
		return "", err
	}
	csrfToken, err := mintToken()
	if err != nil {
		return "", err
	}

	now := m.now()
	rec := &store.Record{
		Token:       token,
		Mode:        principal.Mode,
		PrincipalID: principal.ID,
		CSRFToken:   csrfToken,
		CreatedAt:   now,
		ExpiresAt:   now.Add(m.cfg.Lifetime),
		Data: map[string]string{
			"display_name": principal.DisplayName,
		},
	}

	if err := m.store.Put(r.Context(), rec); err != nil {
		return "", err
	}

	// Mirror scs's WriteSessionCookie semantics — same attrs we configured
	// on m.scs.Cookie above, so the on-wire cookie matches scs's idiom
	// even though we don't route through scs.LoadAndSave.
	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    token,
		Path:     m.cfg.CookiePath,
		HttpOnly: true,
		Secure:   m.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.cfg.Lifetime.Seconds()),
	})

	return token, nil
}

// End revokes the session attached to r (if any) and writes a clear-cookie
// Set-Cookie header on w. Idempotent — a request with no cookie or a
// cookie referencing an already-deleted Record still returns nil.
//
// Cleared cookie attributes mirror Begin's write exactly except for
// MaxAge=-1 (per RFC 6265 §5.2.2 — instruct the browser to delete).
func (m *SessionManager) End(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(m.cfg.CookieName)
	if err == nil && cookie.Value != "" {
		// Best-effort delete; errors logged by the store but don't gate
		// the cookie clear. The 401 backstop on the next request closes
		// the gap if Delete fails.
		_ = m.store.Delete(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     m.cfg.CookiePath,
		HttpOnly: true,
		Secure:   m.cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	return nil
}

// Authenticate reads the session cookie from r and looks up the matching
// Record in the store. Returns the Record on success.
//
// Error contract:
//   - No cookie / empty value          → ErrSessionInvalid
//   - Store returns ErrSessionNotFound → ErrSessionInvalid
//   - Store returns any other error    → ErrSessionInvalid (wrapped)
//   - Record.ExpiresAt has passed      → ErrSessionExpired
//   - Record.Mode != cfg.Mode          → ErrSessionModeMismatch
//
// The expiry check here is defensive — the store's Get is REQUIRED to
// also return ErrSessionNotFound for expired records (store.go ladder
// row 3). This double-check pins the read-time invariant inside this
// package even if a future store impl regresses.
//
// Mode-mismatch check (round-5 B3): when an operator flips auth.mode
// between deployments, sessions minted under the old mode survive on
// disk. Returning ErrSessionModeMismatch here causes RequireSession to
// 401 + slog.Warn so the operator sees the flip and the client is
// invited to re-authenticate.
func (m *SessionManager) Authenticate(r *http.Request) (*store.Record, error) {
	cookie, err := r.Cookie(m.cfg.CookieName)
	if err != nil || cookie.Value == "" {
		return nil, ErrSessionInvalid
	}

	rec, err := m.store.Get(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return nil, ErrSessionInvalid
		}
		return nil, errors.Join(ErrSessionInvalid, err)
	}
	if rec == nil {
		return nil, ErrSessionInvalid
	}

	// Defensive expiry check — store ladder row 3 requires this be a
	// no-op on a conformant store, but pinning it here means a future
	// store regression doesn't silently accept stale records.
	if !rec.ExpiresAt.IsZero() && !m.now().Before(rec.ExpiresAt) {
		return nil, ErrSessionExpired
	}

	if m.cfg.Mode != "" && rec.Mode != m.cfg.Mode {
		return nil, ErrSessionModeMismatch
	}

	return rec, nil
}

// Revoke deletes the Record for token from the store. Used by the logout
// handler in addition to End — End calls Revoke internally. Exposed
// separately so administrative paths (PR4/C9's `flowstate auth reset`)
// can drop sessions without a fake request context.
func (m *SessionManager) Revoke(ctx context.Context, token string) error {
	return m.store.Delete(ctx, token)
}

// Config returns the SessionConfig the manager was constructed with.
// Used by CSRF and login.go to inherit Secure / Path attributes for the
// _csrf cookie (plan §"Wire Protocol" lines 422-431 — _csrf inherits
// Secure from the session cookie).
func (m *SessionManager) Config() SessionConfig {
	return m.cfg
}

// SCS returns the underlying scs.SessionManager. Exposed for callers that
// need scs-shaped middleware composition (e.g. ad-hoc handlers outside
// the plan's RequireSession path). Production callers use Begin/End/
// Authenticate; this getter is the escape hatch for tooling.
func (m *SessionManager) SCS() *scs.SessionManager {
	return m.scs
}

func (m *SessionManager) now() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}
	return time.Now()
}

// mintToken generates a 256-bit cryptographically-random opaque token
// encoded as URL-safe base64 (43 chars, no padding). Plan §"Wire Protocol"
// line 417 + §"CSRF token" §"Storage" line 429 — both the session token
// and the CSRF token use the same primitive.
//
// Returns the encoded token; errors only on rand.Read failure (which is
// effectively impossible on Linux but pinned defensively).
func mintToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// scsStoreAdapter bridges scs/v2's []byte-blob Store interface (Find /
// Commit / Delete) onto our Record-shaped store.Store. scs uses this
// adapter for any of its own ad-hoc session writes via LoadAndSave (not
// used in the plan's middleware composition, but pinned for forward
// compatibility — see SessionManager.SCS getter).
//
// scsBlob is the on-wire JSON shape we encode/decode when scs round-trips
// session payload through the adapter. JSON over gob because the adapter
// is a forward-compat hook; the load-bearing path is structured Record
// access via the underlying store directly.
type scsStoreAdapter struct {
	inner store.Store
	mode  string
}

// scsBlob is the JSON-serialised payload scs sees through this adapter.
// The adapter materialises this into Record fields on Commit and
// re-emits it on Find.
type scsBlob struct {
	PrincipalID string            `json:"principal_id,omitempty"`
	CSRFToken   string            `json:"csrf_token,omitempty"`
	Data        map[string]string `json:"data,omitempty"`
}

// Find returns the scs blob for token, or found=false on miss / expired
// record (per scs.Store contract — system errors only on err).
func (a *scsStoreAdapter) Find(token string) ([]byte, bool, error) {
	rec, err := a.inner.Get(context.Background(), token)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if rec == nil {
		return nil, false, nil
	}
	blob := scsBlob{
		PrincipalID: rec.PrincipalID,
		CSRFToken:   rec.CSRFToken,
		Data:        rec.Data,
	}
	b, err := json.Marshal(blob)
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Commit writes a Record for token with the supplied expiry. Mode is
// stamped from the adapter's configured cfg.Mode so cluster-mode B3
// checks survive scs-routed writes.
func (a *scsStoreAdapter) Commit(token string, b []byte, expiry time.Time) error {
	var blob scsBlob
	if err := json.Unmarshal(b, &blob); err != nil {
		return err
	}
	rec := &store.Record{
		Token:       token,
		Mode:        a.mode,
		PrincipalID: blob.PrincipalID,
		CSRFToken:   blob.CSRFToken,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiry,
		Data:        blob.Data,
	}
	return a.inner.Put(context.Background(), rec)
}

// Delete removes the Record for token. Idempotent (per scs.Store contract).
func (a *scsStoreAdapter) Delete(token string) error {
	return a.inner.Delete(context.Background(), token)
}
