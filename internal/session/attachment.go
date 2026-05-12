// Package session attachment storage layer.
//
// This file implements the per-session, content-hashed filesystem store
// for user-uploaded chat attachments. Image-only PR1 scope per plan
// "Chat Attachments Backend (May 2026)" §6 task-02.
//
// Layout on disk:
//
//	<sessionsDir>/
//	  <sessionID>.meta.json             # existing session metadata
//	  <sessionID>/
//	    attachments/
//	      <contentHash>.<ext>           # raw bytes, atomically written
//	      .index.json                   # metadata sidecar (atomic write)
//
// Content addressing: <contentHash> = lower-case SHA-256 of the file
// bytes. Ext is derived from the validated wire-level media type, not
// the original filename — a re-uploaded JPEG named "cat.png" still
// lands at <hash>.jpg, so the on-disk extension always matches the
// media type the model receives.
//
// Atomicity: the .index.json sidecar is written via
// internal/atomicwrite.File so a process kill mid-write never leaves
// a partial file visible. Per-attachment binary files are written via
// the same atomic-temp-rename pattern so the upload endpoint never
// publishes a partial file (memory feedback_atomicity_awareness_uneven).
//
// Caps (defence in depth — also enforced at the HTTP handler):
//   - 5 MB per file
//   - 10 attachments per upload request (handler-level)
//   - 50 MB per session cumulative; oldest non-referenced is swept
//     when a new upload would exceed the cap.
//
// Dedup: identical content within the same session yields the same
// <hash>.<ext> file; the metadata sidecar still records the
// OriginalFilename of the first upload (subsequent uploads return the
// existing attachment id, with the original metadata preserved).
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/atomicwrite"
)

// Attachment-related sentinel errors. Callers (the upload handler) map
// these to HTTP status codes — see internal/api/server.go.
var (
	// ErrAttachmentTooLarge fires when a single file exceeds the 5 MB
	// per-file cap.
	ErrAttachmentTooLarge = errors.New("attachment: file exceeds per-file size limit")
	// ErrAttachmentSessionCap fires when adding the attachment would
	// push the session over its cumulative byte budget even after
	// sweeping unreferenced entries.
	ErrAttachmentSessionCap = errors.New("attachment: session cumulative cap exceeded")
	// ErrAttachmentUnsupportedType fires when the validated media type
	// is not on the PR1 allow-list (image/jpeg, image/png, image/gif,
	// image/webp).
	ErrAttachmentUnsupportedType = errors.New("attachment: unsupported media type")
	// ErrAttachmentNotFound fires when a referenced id is not in the
	// session's index.
	ErrAttachmentNotFound = errors.New("attachment: not found")
)

// Attachment size and count caps. Plan §1 / §6 task-02.
const (
	// MaxAttachmentFileBytes is the per-file ceiling (5 MB).
	MaxAttachmentFileBytes int64 = 5 * 1024 * 1024
	// MaxAttachmentSessionBytes is the cumulative per-session ceiling
	// (50 MB). When the cap is exceeded, oldest non-referenced
	// entries are swept first; a true overflow returns
	// ErrAttachmentSessionCap.
	MaxAttachmentSessionBytes int64 = 50 * 1024 * 1024
	// MaxAttachmentsPerRequest is enforced at the upload handler; the
	// constant lives here so the API layer can import it without a
	// second source of truth.
	MaxAttachmentsPerRequest = 10
)

// attachmentIndexFile is the metadata sidecar filename inside each
// session's attachments directory.
const attachmentIndexFile = ".index.json"

// allowedImageMediaTypes is the PR1 allow-list (lower-case, no params).
// PR4 will extend this with application/pdf via a separate code path
// per plan §6 task-14 — image-only stays focused for PR1.
var allowedImageMediaTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// AttachmentRecord is the persisted metadata entry for a single uploaded
// file inside a session's attachments directory.
//
// Persisted alongside the binary file in .index.json. The struct is
// versionless on purpose (PR1) — the file shape is `{"entries": [...]}`
// and unknown fields are tolerated by the json decoder, so a future PR
// that adds fields will round-trip cleanly for callers running an older
// build.
//
// reserved is an in-memory-only atomic counter for the two-phase
// reference (S2) sweeper integration — it is NOT persisted. The sweeper
// only deletes entries with reserved == 0 AND ReferencedByMessageIDs
// empty AND uploaded_at older than the TTL. See task-06 in PR1's later
// commit (sweeper.go).
type AttachmentRecord struct {
	ID                     string    `json:"id"`
	MediaType              string    `json:"media_type"`
	SizeBytes              int64     `json:"size_bytes"`
	OriginalFilename       string    `json:"original_filename,omitempty"`
	ContentHash            string    `json:"content_hash"`
	Ext                    string    `json:"ext"`
	UploadedAt             time.Time `json:"uploaded_at"`
	ReferencedByMessageIDs []string  `json:"referenced_by_message_ids,omitempty"`

	// reserved is the in-memory atomic in-flight reference count. Zero
	// at rest; incremented by MarkReserved before a provider dispatch
	// and decremented by MarkReferenced / ReleaseReservation. Never
	// persisted to disk (process restart re-zeroes it; the cold-start
	// sweep then catches any genuinely orphaned entries).
	reserved atomic.Int32 `json:"-"`
}

// attachmentIndex is the on-disk shape of .index.json.
type attachmentIndex struct {
	Entries []*AttachmentRecord `json:"entries"`
}

// AttachmentStore is the per-manager handle into the on-disk attachment
// store. One instance per Manager; the manager owns its lifetime and
// embeds it (see Manager.attachments below).
//
// The store is in-memory caching with atomic-write persistence — each
// session's index is loaded lazily on first access and held under the
// store mutex until process exit or session delete. Mutations write the
// .index.json sidecar atomically before releasing the lock so a crash
// between in-memory and on-disk state is bounded to a single in-flight
// upload (the binary file is also atomically written, so partial-write
// scenarios cannot publish corrupted bytes to a later read).
//
// Thread-safety: every public method takes the store mutex. The mutex
// is fine-grained enough for the PR1 single-server case (one mutex per
// store, not per session); a future multi-server architecture would
// shard by sessionID.
type AttachmentStore struct {
	mu        sync.Mutex
	rootDir   string                                  // <sessionsDir>
	sessions  map[string]map[string]*AttachmentRecord // sessionID → id → record
	loaded    map[string]bool                         // sessionID → loaded
	allowList map[string]string                       // media type → ext
}

// NewAttachmentStore constructs a store rooted at sessionsDir. An empty
// rootDir disables persistence (Put returns ErrAttachmentSessionCap; the
// constructor still succeeds so the manager can hold a nil-safe
// reference and tests that never touch attachments don't need to
// configure a dir).
//
// Expected:
//   - rootDir is the session manager's sessionsDir; empty means
//     persistence disabled.
//
// Returns:
//   - A non-nil *AttachmentStore.
//
// Side effects:
//   - None at construction time. Per-session directories are created
//     lazily inside Put.
func NewAttachmentStore(rootDir string) *AttachmentStore {
	allowList := make(map[string]string, len(allowedImageMediaTypes))
	for k, v := range allowedImageMediaTypes {
		allowList[k] = v
	}
	return &AttachmentStore{
		rootDir:   rootDir,
		sessions:  make(map[string]map[string]*AttachmentRecord),
		loaded:    make(map[string]bool),
		allowList: allowList,
	}
}

// AttachmentPutResult is the outcome of an upload.
type AttachmentPutResult struct {
	Record    *AttachmentRecord
	Duplicate bool // true when the content hash already existed in the session
}

// Put atomically writes data into the session's attachments directory
// and returns the resulting record. Idempotent on content hash within a
// session — a second upload of the same bytes returns the existing id
// with Duplicate=true. The OriginalFilename of the FIRST upload is
// preserved (this is a feature, not a bug — the user's intent is "I want
// this image", not "I want this exact filename").
//
// Expected:
//   - sessionID is non-empty.
//   - mediaType is on the PR1 allow-list (image/jpeg, image/png,
//     image/gif, image/webp). Otherwise ErrAttachmentUnsupportedType.
//   - data is the raw file bytes. Length must be <= MaxAttachmentFileBytes.
//   - originalFilename is the upload's filename hint (may be empty).
//
// Returns:
//   - A non-nil AttachmentPutResult on success.
//   - ErrAttachmentUnsupportedType for off-allow-list media types.
//   - ErrAttachmentTooLarge when len(data) > MaxAttachmentFileBytes.
//   - ErrAttachmentSessionCap when the cumulative-cap sweep cannot
//     make room.
//   - Any os/filesystem error from the atomic-write or sidecar persist
//     path.
//
// Side effects:
//   - Creates <rootDir>/<sessionID>/attachments/ if absent.
//   - Writes <contentHash>.<ext> atomically.
//   - Re-writes .index.json atomically.
func (s *AttachmentStore) Put(sessionID, mediaType string, data []byte, originalFilename string) (AttachmentPutResult, error) {
	if s.rootDir == "" {
		return AttachmentPutResult{}, errors.New("attachment: store not configured (empty sessionsDir)")
	}
	if sessionID == "" {
		return AttachmentPutResult{}, errors.New("attachment: empty session id")
	}
	ext, ok := s.allowList[mediaType]
	if !ok {
		return AttachmentPutResult{}, ErrAttachmentUnsupportedType
	}
	if int64(len(data)) > MaxAttachmentFileBytes {
		return AttachmentPutResult{}, ErrAttachmentTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return AttachmentPutResult{}, err
	}

	hash := sha256.Sum256(data)
	contentHash := hex.EncodeToString(hash[:])

	// Dedup on content hash within session.
	if existing, ok := s.sessions[sessionID][contentHash]; ok {
		return AttachmentPutResult{Record: existing, Duplicate: true}, nil
	}

	// Cumulative cap with reactive sweep — drop oldest non-referenced
	// entries until adding this attachment fits. AC-02 in the plan.
	if err := s.makeRoomLocked(sessionID, int64(len(data))); err != nil {
		return AttachmentPutResult{}, err
	}

	rec := &AttachmentRecord{
		ID:               contentHash,
		MediaType:        mediaType,
		SizeBytes:        int64(len(data)),
		OriginalFilename: originalFilename,
		ContentHash:      contentHash,
		Ext:              ext,
		UploadedAt:       time.Now().UTC(),
	}

	dir, err := s.ensureSessionDirLocked(sessionID)
	if err != nil {
		return AttachmentPutResult{}, err
	}
	filePath := filepath.Join(dir, contentHash+ext)
	if err := atomicwrite.File(filePath, data, 0o600); err != nil {
		return AttachmentPutResult{}, fmt.Errorf("attachment: writing binary: %w", err)
	}

	s.sessions[sessionID][contentHash] = rec
	if err := s.persistIndexLocked(sessionID); err != nil {
		// Best-effort rollback so we never leak a binary without an
		// index entry pointing at it.
		delete(s.sessions[sessionID], contentHash)
		_ = os.Remove(filePath)
		return AttachmentPutResult{}, fmt.Errorf("attachment: persisting index: %w", err)
	}
	return AttachmentPutResult{Record: rec, Duplicate: false}, nil
}

// Get returns the record and binary bytes for the given attachment id
// inside the session. The id is the content hash (stable identifier).
//
// Expected:
//   - sessionID and attachmentID are non-empty.
//
// Returns:
//   - The record and the on-disk bytes on success.
//   - ErrAttachmentNotFound when the id is not in the session's index
//     or the binary is missing.
func (s *AttachmentStore) Get(sessionID, attachmentID string) (*AttachmentRecord, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return nil, nil, err
	}
	rec, ok := s.sessions[sessionID][attachmentID]
	if !ok {
		return nil, nil, ErrAttachmentNotFound
	}
	dir := s.sessionDirPath(sessionID)
	data, err := os.ReadFile(filepath.Join(dir, rec.ContentHash+rec.Ext))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrAttachmentNotFound
		}
		return nil, nil, fmt.Errorf("attachment: reading binary: %w", err)
	}
	return rec, data, nil
}

// List returns the records currently indexed for the session in
// upload-order (oldest first). Returns an empty slice when the session
// has no attachments or no on-disk state.
func (s *AttachmentStore) List(sessionID string) ([]*AttachmentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return nil, err
	}
	records := make([]*AttachmentRecord, 0, len(s.sessions[sessionID]))
	for _, rec := range s.sessions[sessionID] {
		records = append(records, rec)
	}
	sortRecordsByUploadedAt(records)
	return records, nil
}

// Resolve looks up a slice of attachment ids inside a session and
// returns the materialised []provider.Attachment slice (with raw bytes
// loaded from disk). This is the engine-seam entry point — the API
// handler does NOT call this; the session manager does, immediately
// before threading attachments onto the user message that ships to
// the engine.
//
// Unknown ids are returned via ErrAttachmentNotFound. The caller is
// responsible for translating the error to an HTTP status (400 with
// "attachment id not found").
//
// The bytes carried in the returned slice are short-lived — the per-
// provider translator base64-encodes them at request-build time and
// then the slice goes out of scope on turn completion. We deliberately
// do NOT cache decoded bytes on the in-memory record (memory cost
// would scale with session length).
func (s *AttachmentStore) Resolve(sessionID string, ids []string) ([]AttachmentMaterialised, error) {
	out := make([]AttachmentMaterialised, 0, len(ids))
	for _, id := range ids {
		rec, data, err := s.Get(sessionID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, AttachmentMaterialised{Record: rec, Data: data})
	}
	return out, nil
}

// AttachmentMaterialised pairs a record with its on-disk bytes. The
// engine-side adapter converts this into provider.Attachment (the
// engine-boundary type) before passing through the provider seam.
type AttachmentMaterialised struct {
	Record *AttachmentRecord
	Data   []byte
}

// RemoveSession deletes the entire <sessionID>/attachments tree from
// disk and clears the in-memory index. Idempotent and tolerant of a
// never-persisted session (no directory exists yet).
func (s *AttachmentStore) RemoveSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
	delete(s.loaded, sessionID)
	if s.rootDir == "" {
		return
	}
	// Remove the whole <sessionID>/ subdir — attachments lives one level
	// down, but the parent dir was created by us so we own it. The
	// session's flat .meta.json sidecar at <sessionsDir>/<sessionID>.meta.json
	// is removed separately by Manager.DeleteSession via removeSessionFiles.
	_ = os.RemoveAll(s.sessionDirParent(sessionID))
}

// MarkReserved increments the in-flight reservation counter for the
// given attachment id inside the session. Called BEFORE the provider
// dispatch fires so the sweeper sees a positive reservation and skips
// the entry even when its uploaded_at would otherwise qualify it as an
// orphan.
//
// Two-phase reference protocol (memory feedback_audit_plan_with_its_own_detector,
// plan §6 task-06 AC-06-Race-Two-Phase):
//
//   - MarkReserved fires pre-dispatch (HTTP message handler, before
//     calling sessionManager.SendMessage).
//   - MarkReferenced fires post-persist (after the user message hits
//     the message log; the attachment now has a real referrer).
//   - ReleaseReservation fires on failure paths (provider call failed
//     before the user message persisted, or the request was rejected
//     after MarkReserved).
//
// Idempotent on unknown ids — the call is silently a no-op so the
// handler doesn't have to round-trip errors for stale id refs that
// were swept between handler entry and dispatch.
func (s *AttachmentStore) MarkReserved(sessionID, attachmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return
	}
	if rec, ok := s.sessions[sessionID][attachmentID]; ok {
		rec.reserved.Add(1)
	}
}

// MarkReferenced decrements the in-flight counter and appends the
// message id to the record's permanent reference set. Called AFTER the
// user message is persisted to the session log. Idempotent on unknown
// ids (silent no-op) and on a no-op MarkReserved-then-MarkReferenced
// pair (decrement floors at zero via the atomic Int32 semantics; we
// guard against negative drift by only persisting when the message id
// is new).
func (s *AttachmentStore) MarkReferenced(sessionID, attachmentID, messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return
	}
	rec, ok := s.sessions[sessionID][attachmentID]
	if !ok {
		return
	}
	// Decrement reservation. Floor at zero to guard against a stray
	// MarkReferenced call without a matching MarkReserved.
	for {
		cur := rec.reserved.Load()
		if cur <= 0 {
			break
		}
		if rec.reserved.CompareAndSwap(cur, cur-1) {
			break
		}
	}
	// Idempotent append — don't grow the slice on a re-fire of the same
	// message id.
	if messageID == "" {
		return
	}
	for _, existing := range rec.ReferencedByMessageIDs {
		if existing == messageID {
			return
		}
	}
	rec.ReferencedByMessageIDs = append(rec.ReferencedByMessageIDs, messageID)
	_ = s.persistIndexLocked(sessionID)
}

// ReleaseReservation decrements the in-flight counter without
// persisting a permanent reference. Called on failure paths (the
// provider call did not produce a persisted user message).
func (s *AttachmentStore) ReleaseReservation(sessionID, attachmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoadedLocked(sessionID); err != nil {
		return
	}
	rec, ok := s.sessions[sessionID][attachmentID]
	if !ok {
		return
	}
	for {
		cur := rec.reserved.Load()
		if cur <= 0 {
			break
		}
		if rec.reserved.CompareAndSwap(cur, cur-1) {
			break
		}
	}
}

// SweepOrphans walks every loaded session and deletes records that are
// (a) older than ttl, (b) have zero reserved in-flight references, and
// (c) carry an empty ReferencedByMessageIDs set. Returns the number of
// records removed. Called from the sweeper goroutine.
//
// `now` is parameterised so tests can inject a fake clock.
func (s *AttachmentStore) SweepOrphans(now time.Time, ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for sessionID := range s.sessions {
		removed += s.sweepSessionLocked(sessionID, now, ttl)
	}
	return removed
}

// ColdStartSweep loads every session subdirectory under rootDir from
// disk and runs SweepOrphans against it. Designed for boot-time
// invocation (plan §6 task-06 AC-06-Y) so orphans that accumulated
// while the process was down are purged before the regular ticker
// activates.
//
// Idempotent and tolerant of a missing rootDir (returns zero with no
// error).
func (s *AttachmentStore) ColdStartSweep(now time.Time, ttl time.Duration) (int, error) {
	if s.rootDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Ensure each on-disk session is loaded so SweepOrphans sees it.
		s.mu.Lock()
		_ = s.ensureLoadedLocked(e.Name())
		s.mu.Unlock()
	}
	return s.SweepOrphans(now, ttl), nil
}

// ExtensionForMediaType returns the file extension the store uses for a
// given media type, or "" when the type is not on the allow-list.
// Exposed so the upload handler can build the on-the-wire response
// metadata without re-deriving from the wire string.
func (s *AttachmentStore) ExtensionForMediaType(mediaType string) string {
	return s.allowList[mediaType]
}

// IsAllowedMediaType reports whether the given media type is on the
// PR1 allow-list. Used by the upload handler to short-circuit content
// sniff failures before the storage path runs.
func (s *AttachmentStore) IsAllowedMediaType(mediaType string) bool {
	_, ok := s.allowList[mediaType]
	return ok
}

// AllowedMediaTypes returns a snapshot copy of the media-type allow-list
// (lower-case keys, "." prefixed extension values). Order is not
// guaranteed.
func (s *AttachmentStore) AllowedMediaTypes() map[string]string {
	out := make(map[string]string, len(s.allowList))
	for k, v := range s.allowList {
		out[k] = v
	}
	return out
}

// DetectImageMediaType cross-checks an upload's claimed content-type
// against the bytes by calling net/http.DetectContentType. Returns the
// authoritative media type (always lower-case, without the charset
// parameter) and ok=true when the detection matches the PR1 allow-list.
//
// Used by the upload handler so a renamed .exe-as-.png cannot slip
// past the allow-list — the bytes themselves are the source of truth.
// Plan §6 task-03 acceptance criteria.
//
// Expected:
//   - data is the file's leading bytes (DetectContentType reads the
//     first 512). Passing the full file is fine — the helper trims to
//     what DetectContentType actually needs internally.
//
// Returns:
//   - The detected media type and true when on the allow-list.
//   - Empty string and false when the bytes do not sniff to an allowed
//     image type.
func DetectImageMediaType(data []byte) (string, bool) {
	sniff := http.DetectContentType(data)
	// DetectContentType returns "image/png" for PNG, "image/jpeg" for
	// JPEG, "image/gif" for GIF; webp is returned as "image/webp" on
	// Go 1.17+ where the magic bytes (RIFF...WEBP) are recognised.
	if _, ok := allowedImageMediaTypes[sniff]; ok {
		return sniff, true
	}
	return "", false
}

// --- internal helpers ----------------------------------------------------

func (s *AttachmentStore) sessionDirParent(sessionID string) string {
	return filepath.Join(s.rootDir, sessionID)
}

func (s *AttachmentStore) sessionDirPath(sessionID string) string {
	return filepath.Join(s.rootDir, sessionID, "attachments")
}

func (s *AttachmentStore) ensureSessionDirLocked(sessionID string) (string, error) {
	dir := s.sessionDirPath(sessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("attachment: mkdir: %w", err)
	}
	return dir, nil
}

func (s *AttachmentStore) ensureLoadedLocked(sessionID string) error {
	if s.loaded[sessionID] {
		return nil
	}
	if s.sessions[sessionID] == nil {
		s.sessions[sessionID] = make(map[string]*AttachmentRecord)
	}
	indexPath := filepath.Join(s.sessionDirPath(sessionID), attachmentIndexFile)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.loaded[sessionID] = true
			return nil
		}
		return fmt.Errorf("attachment: reading index: %w", err)
	}
	var idx attachmentIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("attachment: parsing index: %w", err)
	}
	for _, rec := range idx.Entries {
		s.sessions[sessionID][rec.ID] = rec
	}
	s.loaded[sessionID] = true
	return nil
}

func (s *AttachmentStore) persistIndexLocked(sessionID string) error {
	if s.rootDir == "" {
		return errors.New("attachment: store not configured")
	}
	dir, err := s.ensureSessionDirLocked(sessionID)
	if err != nil {
		return err
	}
	records := make([]*AttachmentRecord, 0, len(s.sessions[sessionID]))
	for _, rec := range s.sessions[sessionID] {
		records = append(records, rec)
	}
	sortRecordsByUploadedAt(records)
	idx := attachmentIndex{Entries: records}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("attachment: marshalling index: %w", err)
	}
	return atomicwrite.File(filepath.Join(dir, attachmentIndexFile), data, 0o600)
}

// cumulativeBytesLocked sums sizes of records currently in the session.
func (s *AttachmentStore) cumulativeBytesLocked(sessionID string) int64 {
	var total int64
	for _, rec := range s.sessions[sessionID] {
		total += rec.SizeBytes
	}
	return total
}

// makeRoomLocked sweeps oldest unreferenced records until incoming
// bytes fit under the session cap, or returns ErrAttachmentSessionCap
// when the cap cannot be respected without destroying referenced data.
func (s *AttachmentStore) makeRoomLocked(sessionID string, incoming int64) error {
	if incoming > MaxAttachmentSessionBytes {
		// A single file bigger than the whole cap is impossible by the
		// per-file cap, but defend in depth.
		return ErrAttachmentSessionCap
	}
	cur := s.cumulativeBytesLocked(sessionID)
	if cur+incoming <= MaxAttachmentSessionBytes {
		return nil
	}
	// Sweep oldest-first non-referenced entries.
	records := make([]*AttachmentRecord, 0, len(s.sessions[sessionID]))
	for _, rec := range s.sessions[sessionID] {
		records = append(records, rec)
	}
	sortRecordsByUploadedAt(records)
	for _, rec := range records {
		if cur+incoming <= MaxAttachmentSessionBytes {
			break
		}
		if rec.reserved.Load() > 0 {
			continue
		}
		if len(rec.ReferencedByMessageIDs) > 0 {
			continue
		}
		// Drop it.
		s.removeRecordLocked(sessionID, rec)
		cur -= rec.SizeBytes
	}
	if cur+incoming > MaxAttachmentSessionBytes {
		return ErrAttachmentSessionCap
	}
	return nil
}

// removeRecordLocked deletes a record from the in-memory index and its
// binary file from disk. Persistence of the new index shape is the
// caller's responsibility (typically batched with another mutation).
func (s *AttachmentStore) removeRecordLocked(sessionID string, rec *AttachmentRecord) {
	delete(s.sessions[sessionID], rec.ID)
	if s.rootDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(s.sessionDirPath(sessionID), rec.ContentHash+rec.Ext))
}

// sweepSessionLocked applies the orphan policy to a single session.
// Returns the number of records removed. Caller must hold s.mu.
func (s *AttachmentStore) sweepSessionLocked(sessionID string, now time.Time, ttl time.Duration) int {
	records := make([]*AttachmentRecord, 0, len(s.sessions[sessionID]))
	for _, rec := range s.sessions[sessionID] {
		records = append(records, rec)
	}
	removed := 0
	for _, rec := range records {
		if rec.reserved.Load() > 0 {
			continue
		}
		if len(rec.ReferencedByMessageIDs) > 0 {
			continue
		}
		if now.Sub(rec.UploadedAt) < ttl {
			continue
		}
		s.removeRecordLocked(sessionID, rec)
		removed++
	}
	if removed > 0 {
		_ = s.persistIndexLocked(sessionID)
	}
	return removed
}

// sortRecordsByUploadedAt sorts oldest-first in place. Stable on ties.
func sortRecordsByUploadedAt(recs []*AttachmentRecord) {
	// Simple O(n^2) sort — fine for the n<=tens we expect per session
	// (a 50 MB cap with a 5 MB per-file cap gives n<=10 in practice).
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0 && recs[j-1].UploadedAt.After(recs[j].UploadedAt); j-- {
			recs[j-1], recs[j] = recs[j], recs[j-1]
		}
	}
}
