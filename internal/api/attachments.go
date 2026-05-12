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
	MediaType        string `json:"mediaType"`
	SizeBytes        int64  `json:"sizeBytes"`
	OriginalFilename string `json:"originalFilename,omitempty"`
}

// attachmentsUploadResponse is the wire shape for the upload endpoint.
type attachmentsUploadResponse struct {
	Attachments []attachmentResponse `json:"attachments"`
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

	// Verify the session exists. Mirrors handleSessionMessage's "session
	// not found" 404 so the surface shape stays consistent.
	if _, err := s.sessionManager.SnapshotSession(sessionID); err != nil {
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
	if len(files) > session.MaxAttachmentsPerRequest {
		http.Error(
			w,
			fmt.Sprintf("too many files (max %d per request)",
				session.MaxAttachmentsPerRequest),
			http.StatusBadRequest,
		)
		return
	}

	store := s.sessionManager.AttachmentStore()
	results := make([]attachmentResponse, 0, len(files))

	for _, fh := range files {
		if fh.Size > session.MaxAttachmentFileBytes {
			http.Error(
				w,
				fmt.Sprintf("file %q exceeds per-file size limit",
					fh.Filename),
				http.StatusRequestEntityTooLarge,
			)
			return
		}

		f, err := fh.Open()
		if err != nil {
			http.Error(w, "could not read uploaded file", http.StatusBadRequest)
			return
		}
		// Cap the per-file read at the per-file budget — defends against
		// a malformed multipart header that lies about Size.
		data, err := io.ReadAll(io.LimitReader(f, session.MaxAttachmentFileBytes+1))
		_ = f.Close()
		if err != nil {
			http.Error(w, "could not read uploaded file", http.StatusBadRequest)
			return
		}
		if int64(len(data)) > session.MaxAttachmentFileBytes {
			http.Error(
				w,
				fmt.Sprintf("file %q exceeds per-file size limit",
					fh.Filename),
				http.StatusRequestEntityTooLarge,
			)
			return
		}

		// Content-sniff to defeat renamed-extension attacks. The wire
		// Content-Type header is advisory only; the bytes are the
		// authoritative source.
		mediaType, ok := session.DetectImageMediaType(data)
		if !ok {
			http.Error(
				w,
				fmt.Sprintf("file %q is not an allowed image type", fh.Filename),
				http.StatusUnsupportedMediaType,
			)
			return
		}

		res, err := store.Put(sessionID, mediaType, data, fh.Filename)
		if err != nil {
			writeAttachmentStoreError(w, err)
			return
		}
		results = append(results, attachmentResponse{
			ID:               res.Record.ID,
			MediaType:        res.Record.MediaType,
			SizeBytes:        res.Record.SizeBytes,
			OriginalFilename: res.Record.OriginalFilename,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(attachmentsUploadResponse{Attachments: results})
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
