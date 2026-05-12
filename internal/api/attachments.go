// Package api attachment handlers.
//
// This file owns the HTTP surface for chat attachments. PR1 scope per
// plan "Chat Attachments Backend (May 2026)" §6 task-03:
//
//   - POST /api/v1/sessions/{id}/attachments (multipart/form-data)
//
// PR2 task-07 will add:
//
//   - GET /api/v1/sessions/{id}/attachments/{aid}
//
// The handlers ride the same path-param session-scope gate as
// internal/api/server.go:827-871 (handleSessionMessage). Auth Track v1
// will retrofit RequireSession middleware — until then, the
// bearer-by-session_id mirror is the temporary state (plan R3).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// maxUploadRequestBytes caps the entire multipart body. Sized to allow
// the maximum legal request (10 × 5 MB = 50 MB) plus a generous
// envelope for multipart framing overhead.
const maxUploadRequestBytes = 64 * 1024 * 1024

// maxMultipartFormMemoryBytes is the in-memory threshold for
// multipart parsing; anything bigger spills to a temp file. 32 MB is
// a comfortable budget for the typical PR1 workload (a 5 MB image
// payload sits well under).
const maxMultipartFormMemoryBytes = 32 * 1024 * 1024

// attachmentResponse is the wire shape for a single uploaded
// attachment. Stable JSON tags so the frontend can map by field name.
type attachmentResponse struct {
	ID               string `json:"id"`
	Kind             string `json:"kind,omitempty"`
	MediaType        string `json:"mediaType"`
	SizeBytes        int64  `json:"sizeBytes"`
	OriginalFilename string `json:"originalFilename,omitempty"`
}

// attachmentsUploadResponse is the wire shape for the upload endpoint.
type attachmentsUploadResponse struct {
	Attachments []attachmentResponse `json:"attachments"`
}

// attachmentErrorBody is the structured JSON error envelope for the
// upload endpoint per plan §7a "Cap Precedence Order". Every
// rejection path emits {"error": "<code>", "message": "<human>"}
// so the frontend can map on `error` for UX branching without parsing
// human text.
type attachmentErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Cap-precedence error codes per plan §7a. Stable wire-string values
// — never re-spell.
const (
	errCodeMediaTypeNotAllowed     = "media_type_not_allowed"
	errCodeFileTooLarge            = "file_too_large"
	errCodeTooManyAttachments      = "too_many_attachments"
	errCodeSessionBudgetExhausted  = "session_budget_exhausted"
	errCodeRequestTooLarge         = "request_too_large"
	errCodeProviderDoesNotSupport  = "provider_does_not_support_pdf"
)

// writeAttachmentError emits the structured JSON error envelope.
func writeAttachmentError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(attachmentErrorBody{
		Error:   code,
		Message: message,
	})
}

// stagedFile is the per-file state collected during the pre-Put scan.
// The handler reads bytes + sniffs media type for every file up front
// so the cap-precedence ladder evaluates against fully-typed data
// before any side effect.
type stagedFile struct {
	filename  string
	data      []byte
	mediaType string
	kind      string
	ext       string
}

// handleUploadAttachments accepts a multipart/form-data POST under the
// session-scope path and stores each `files` part via the manager's
// AttachmentStore. The response mirrors what the frontend will
// thread onto the subsequent POST /messages call as `attachmentIds`.
//
// Status codes:
//   - 200 OK: at least one file uploaded; body has the per-file results.
//   - 400 Bad Request: missing or malformed multipart body, or >10 files.
//   - 413 Request Entity Too Large: a file exceeds the per-file cap,
//     or the cumulative session cap is exceeded after the sweep.
//   - 415 Unsupported Media Type: at least one file's sniffed media
//     type is not on the PR1 allow-list.
//   - 501 Not Implemented: session manager not configured.
//
// Expected:
//   - Request path parameter `id` identifies the session.
//   - Request body is multipart/form-data with field name `files`,
//     repeatable up to MaxAttachmentsPerRequest times.
//
// Side effects:
//   - Writes each file atomically into the session's attachments
//     directory and updates the on-disk index.
//
// Per memory feedback_close_latent_surfaces_too: this handler also
// content-sniffs to defeat a renamed-extension attack — net/http
// DetectContentType is the source of truth, the wire Content-Type
// header is only used to short-circuit obviously-wrong uploads
// before reading the bytes.
func (s *Server) handleUploadAttachments(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	// Verify the session exists AND snapshot the active model id so
	// step 6 of the cap-precedence ladder can compare against
	// "anthropic". SnapshotSession is preferred over GetSession per
	// plan §6 task-15: the value-type snapshot is lock-safe past the
	// manager's RLock boundary; passing the live *Session pointer
	// would leak a reference out of the manager's mutex (memory
	// project_flowstate_engine_boundary spirit).
	snap, err := s.sessionManager.SnapshotSession(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Cap the total request body BEFORE parsing the multipart envelope.
	// http.MaxBytesReader returns *http.MaxBytesError on overflow, which
	// ParseMultipartForm propagates — we map that to 413 below.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)

	if err := r.ParseMultipartForm(maxMultipartFormMemoryBytes); err != nil {
		writeAttachmentParseError(w, err)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "no files in request", http.StatusBadRequest)
		return
	}

	store := s.sessionManager.AttachmentStore()

	// Cap-precedence ladder per plan §7a. Stage 1+2 are per-file
	// (MIME sniff + per-file size cap); stages 3-6 are aggregate
	// (count caps, per-session budget, per-request total,
	// provider-compatibility). The handler shorts on the FIRST step
	// that fires in precedence order, so e.g. a `.txt` upload on an
	// Ollama session returns 415/media_type_not_allowed (step 1)
	// before step 6 sees the provider mismatch.
	//
	// We read every file fully before any Put so we never publish a
	// partial multipart on a later-stage rejection.

	staged := make([]stagedFile, 0, len(files))
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeAttachmentError(w, http.StatusBadRequest,
				"invalid_multipart", "could not read uploaded file")
			return
		}
		// Bound each file read at the larger of the two per-file caps
		// (PDFs) +1 byte. Any file that exceeds even the PDF ceiling
		// is rejected by stage 2 below; this just prevents a malicious
		// multipart from streaming forever past the lie in its
		// Content-Length header.
		readCap := session.MaxPDFFileSize
		if readCap < session.MaxAttachmentFileBytes {
			readCap = session.MaxAttachmentFileBytes
		}
		data, err := io.ReadAll(io.LimitReader(f, readCap+1))
		_ = f.Close()
		if err != nil {
			writeAttachmentError(w, http.StatusBadRequest,
				"invalid_multipart", "could not read uploaded file")
			return
		}

		// Stage 1 — MIME-type allow-list (sniffed bytes). 415 with
		// error="media_type_not_allowed".
		kind, mediaType, ext, sniffErr := session.DetectMediaType(data)
		if sniffErr != nil {
			writeAttachmentError(w, http.StatusUnsupportedMediaType,
				errCodeMediaTypeNotAllowed,
				fmt.Sprintf("file %q is not an allowed media type", fh.Filename))
			return
		}

		// Stage 2 — per-file size cap (kind-aware). 413 with
		// error="file_too_large".
		var perFileCap int64
		switch kind {
		case session.AttachmentKindDocument:
			perFileCap = session.MaxPDFFileSize
		default:
			perFileCap = session.MaxAttachmentFileBytes
		}
		if int64(len(data)) > perFileCap {
			writeAttachmentError(w, http.StatusRequestEntityTooLarge,
				errCodeFileTooLarge,
				fmt.Sprintf("file %q exceeds per-file size limit", fh.Filename))
			return
		}

		staged = append(staged, stagedFile{
			filename:  fh.Filename,
			data:      data,
			mediaType: mediaType,
			kind:      kind,
			ext:       ext,
		})
	}

	// Stage 3 — per-message file count. Images: 10 per multipart;
	// documents: MaxPDFsPerMessage (5). 400 with
	// error="too_many_attachments".
	imageCount, pdfCount := 0, 0
	for _, sf := range staged {
		switch sf.kind {
		case session.AttachmentKindDocument:
			pdfCount++
		default:
			imageCount++
		}
	}
	if imageCount > session.MaxAttachmentsPerRequest {
		writeAttachmentError(w, http.StatusBadRequest,
			errCodeTooManyAttachments,
			fmt.Sprintf("too many image attachments (max %d per request)",
				session.MaxAttachmentsPerRequest))
		return
	}
	if pdfCount > session.MaxPDFsPerMessage {
		writeAttachmentError(w, http.StatusBadRequest,
			errCodeTooManyAttachments,
			fmt.Sprintf("too many PDF attachments (max %d per message)",
				session.MaxPDFsPerMessage))
		return
	}

	// Stage 4 — per-session per-kind budget. 413 with
	// error="session_budget_exhausted". Document budget is checked
	// against the staged PDF sum (the Put-side sweep is the secondary
	// defence; this is the deterministic gate the frontend can map).
	stagedImageBytes, stagedDocumentBytes := int64(0), int64(0)
	for _, sf := range staged {
		switch sf.kind {
		case session.AttachmentKindDocument:
			stagedDocumentBytes += int64(len(sf.data))
		default:
			stagedImageBytes += int64(len(sf.data))
		}
	}
	if stagedImageBytes > 0 &&
		store.ImageBytesUsed(sessionID)+stagedImageBytes > session.MaxAttachmentSessionBytes {
		writeAttachmentError(w, http.StatusRequestEntityTooLarge,
			errCodeSessionBudgetExhausted,
			"image session budget exhausted")
		return
	}
	if stagedDocumentBytes > 0 &&
		store.DocumentBytesUsed(sessionID)+stagedDocumentBytes > session.MaxDocumentBudgetPerSession {
		writeAttachmentError(w, http.StatusRequestEntityTooLarge,
			errCodeSessionBudgetExhausted,
			"document session budget exhausted")
		return
	}

	// Stage 5 — per-request total. 413 with error="request_too_large".
	// 25 MB mirrors the engine-side TotalAttachmentBytes ceiling so the
	// upload-time gate fires before the wire-time gate.
	totalStaged := stagedImageBytes + stagedDocumentBytes
	if totalStaged > provider.MaxAttachmentRequestBytes() {
		writeAttachmentError(w, http.StatusRequestEntityTooLarge,
			errCodeRequestTooLarge,
			fmt.Sprintf("request body exceeds %d bytes",
				provider.MaxAttachmentRequestBytes()))
		return
	}

	// Stage 6 — provider compatibility (Anthropic gate). PDFs are
	// only natively supported by Anthropic Chat in PR4; sessions
	// bound to a non-Anthropic provider reject PDF uploads at the
	// gate with 415 / error="provider_does_not_support_pdf".
	// Image uploads remain unaffected — only PDFs gate on provider.
	if pdfCount > 0 && snap.CurrentProviderID != "anthropic" {
		writeAttachmentError(w, http.StatusUnsupportedMediaType,
			errCodeProviderDoesNotSupport,
			"PDF attachments require an Anthropic model; switch model or remove the PDF")
		return
	}

	// All gates passed — persist via the store.
	results := make([]attachmentResponse, 0, len(staged))
	for _, sf := range staged {
		res, putErr := store.Put(sessionID, sf.mediaType, sf.data, sf.filename)
		if putErr != nil {
			// Storage-layer rejection (race condition between gate
			// and Put — e.g. concurrent Put pushed the session over
			// budget). Map back to a structured error.
			writeAttachmentStoreErrorStructured(w, putErr)
			return
		}
		results = append(results, attachmentResponse{
			ID:               res.Record.ID,
			Kind:             res.Record.Kind,
			MediaType:        res.Record.MediaType,
			SizeBytes:        res.Record.SizeBytes,
			OriginalFilename: res.Record.OriginalFilename,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(attachmentsUploadResponse{Attachments: results})
}

// writeAttachmentStoreErrorStructured maps storage-layer errors to the
// structured JSON envelope. Used post-gate to cover races where Put
// rejects an upload that passed the upfront cap-precedence ladder.
func writeAttachmentStoreErrorStructured(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrAttachmentTooLarge):
		writeAttachmentError(w, http.StatusRequestEntityTooLarge,
			errCodeFileTooLarge, err.Error())
	case errors.Is(err, session.ErrAttachmentUnsupportedType):
		writeAttachmentError(w, http.StatusUnsupportedMediaType,
			errCodeMediaTypeNotAllowed, err.Error())
	case errors.Is(err, session.ErrAttachmentSessionCap):
		writeAttachmentError(w, http.StatusRequestEntityTooLarge,
			errCodeSessionBudgetExhausted, err.Error())
	default:
		writeAttachmentError(w, http.StatusInternalServerError,
			"internal_error", "attachment storage error")
	}
}

// handleGetAttachment streams the raw bytes for a single attachment
// inside a session. PR2 task-07 (plan "Chat Attachments Backend
// (May 2026)" §6 task-07). Rides the same path-param session-scope
// gate as `internal/api/server.go:832-888 (handleSessionMessage)`
// until Auth Track v1 lands; the `{id}` path param is the auth scope.
//
// Status codes:
//   - 200 OK: bytes streamed; Content-Type from stored media type;
//     Cache-Control private, max-age=300 (content-hash names are
//     stable per id, so this is safe).
//   - 404 Not Found: either the session does not exist, the
//     attachment id is unknown in that session, OR the id exists in
//     a DIFFERENT session. The handler must not even probe the other
//     session — surfacing "exists somewhere else" would leak side-
//     channel evidence of cross-session attachment names and confirm
//     the cross-session injection vector that task-08's MarkdownRenderer
//     allow-list closes (plan R9).
//   - 501 Not Implemented: session manager not configured.
//
// Expected:
//   - Request path parameters `id` (session) and `aid` (attachment).
//
// Side effects:
//   - Reads the attachment bytes from disk via the session's
//     AttachmentStore. No write paths.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}

	sessionID := r.PathValue("id")
	attachmentID := r.PathValue("aid")
	if sessionID == "" || attachmentID == "" {
		// Treat empty path params as 404 rather than 400 — the route
		// shape guarantees both are present in production; an empty
		// value here means a malformed test or a path-traversal probe.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Verify the session exists. Mirrors the upload handler's 404
	// shape so a probe for a non-existent session looks identical to
	// a probe for an unknown attachment id inside an existing
	// session — no enumeration side channel.
	if _, err := s.sessionManager.SnapshotSession(sessionID); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// CRITICAL (plan R9, AC §6 task-07): only the path-scoped
	// session's store is consulted. We do NOT fall through to a
	// global lookup — the `{id}` path is the auth scope until Auth
	// Track v1 lands. A cross-session probe sees only "not found",
	// not "exists elsewhere".
	store := s.sessionManager.AttachmentStore()
	rec, data, err := store.Get(sessionID, attachmentID)
	if err != nil {
		// Any error path (ErrAttachmentNotFound, transient FS error,
		// permission issue) collapses to 404. Leaks neither media
		// type nor existence-elsewhere.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Cache: content-hashed names make bytes-per-id immutable, so a
	// 5-minute browser cache is safe and reduces re-renders on
	// session reload. `private` because the attachment is bound to a
	// single user's session — no shared proxy caching.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Type", rec.MediaType)
	_, _ = w.Write(data)
}

// writeAttachmentParseError maps multipart parse failures to HTTP codes.
func writeAttachmentParseError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid multipart body", http.StatusBadRequest)
}

// writeAttachmentStoreError maps storage-layer errors to HTTP codes.
func writeAttachmentStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrAttachmentTooLarge):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, session.ErrAttachmentUnsupportedType):
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
	case errors.Is(err, session.ErrAttachmentSessionCap):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	default:
		http.Error(w, "attachment storage error", http.StatusInternalServerError)
	}
}
