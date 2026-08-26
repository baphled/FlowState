// Package recall — Layer 3 KnowledgeExtractor (T14).
//
// KnowledgeExtractor uses an LLM to distil a slice of conversation
// messages into []KnowledgeEntry records, then merges the result into a
// SessionMemoryStore with content-based deduplication and persists the
// store to disk.
//
// The extractor is designed to be fired from a goroutine so its
// contract is "best-effort with visible failure": transport and parse
// errors are wrapped and returned to the caller, which is expected to
// log them and continue. The store is not mutated when any step fails.
package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrKnowledgeExtractFailed is the sentinel returned when any step in
// Extract fails (prompt build, LLM call, parse). Callers typically
// errors.Is against it when classifying goroutine failures for logging.
var ErrKnowledgeExtractFailed = errors.New("recall: knowledge extraction failed")

// knowledgeExtractorSystemLines is the system prompt driving the LLM
// toward emitting JSON-only, schema-compliant output. Kept in a slice
// of short lines to stay under the project's 140-character line cap.
var knowledgeExtractorSystemLines = []string{
	"You are a knowledge distiller for an AI coding agent.",
	"Given a transcript, you extract durable facts, conventions, and preferences.",
	"You output a JSON array of KnowledgeEntry objects matching the user prompt schema.",
	"You output JSON only — no prose, no markdown fences, no trailing commentary.",
}

// knowledgeExtractorUserLines is the user-prompt template.  The actual
// transcript is appended by buildExtractionPrompt; this slice defines
// the schema the model must honour.
var knowledgeExtractorUserLines = []string{
	"Distil the following transcript into a JSON array of KnowledgeEntry objects.",
	"",
	"Each entry has these fields:",
	"- id (string): a short identifier, unique within the array.",
	"- type (string): one of \"fact\", \"convention\", \"preference\".",
	"- content (string): the distilled observation, phrased as a standalone statement.",
	"- extracted_at (string, RFC3339): leave the literal string \"PLACEHOLDER\";",
	"  the caller overwrites it.",
	"- relevance (number in [0,1]): heuristic score of how useful this will be",
	"  to a future turn.",
	"",
	"Return an empty array if the transcript contains nothing distillable.",
	"",
	"Transcript:",
}

// KnowledgeExtractor orchestrates Extract: it owns the LLM provider,
// the target store, and the session identifier used to name the output
// file inside the store's directory.
type KnowledgeExtractor struct {
	llm       provider.Provider
	store     *SessionMemoryStore
	sessionID string
	model     string
}

// NewKnowledgeExtractor binds an LLM provider and a store together.
//
// Expected:
//   - llm is a non-nil provider.Provider; Extract calls llm.Chat(...).
//   - store is a non-nil *SessionMemoryStore that receives merged
//     entries.
//   - sessionID is the identifier under which store.Save is called; it
//     must be filesystem-safe.
//
// Returns:
//   - A KnowledgeExtractor ready to run. Never nil.
//
// Side effects:
//   - None at construction time.
func NewKnowledgeExtractor(llm provider.Provider, store *SessionMemoryStore, sessionID string) *KnowledgeExtractor {
	return &KnowledgeExtractor{llm: llm, store: store, sessionID: sessionID}
}

// WithModel configures the chat model the extractor requests. Providers
// such as Ollama reject a ChatRequest with an empty Model field, so
// production wiring must set this — the zero value leaves the extractor
// in its legacy test-only mode where Chat is a no-op mock.
//
// Expected:
//   - model is a provider-specific identifier ("llama3.2", "gpt-4o-mini",
//     etc.). An empty string is accepted so callers can disable override.
//
// Returns:
//   - The receiver, to allow fluent chaining.
//
// Side effects:
//   - None.
func (e *KnowledgeExtractor) WithModel(model string) *KnowledgeExtractor {
	e.model = model
	return e
}

// Extract calls the LLM with an extraction prompt derived from msgs,
// parses the response as []KnowledgeEntry, merges each entry into the
// store (content-dedup), and saves the store.
//
// Expected:
//   - ctx carries cancellation/deadline for the LLM call.
//   - msgs is the slice of messages to distil. An empty slice is a
//     no-op: no LLM call, no store mutation.
//
// Returns:
//   - nil on success (including the empty-input no-op).
//   - A wrapped ErrKnowledgeExtractFailed on any failure. The wrap
//     carries the specific stage (chat, parse, save) for diagnostics.
//
// Side effects:
//   - One LLM call when msgs is non-empty.
//   - Mutates store entries and writes to disk on success.
//   - Nothing is mutated when any step fails.
func (e *KnowledgeExtractor) Extract(ctx context.Context, msgs []provider.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	prompt := buildExtractionPrompt(msgs)
	req := provider.ChatRequest{
		Model: e.model,
		Messages: []provider.Message{
			{Role: "system", Content: strings.Join(knowledgeExtractorSystemLines, "\n")},
			{Role: "user", Content: prompt},
		},
	}

	resp, err := e.llm.Chat(ctx, req)
	if err != nil {
		return fmt.Errorf("%w: chat: %w", ErrKnowledgeExtractFailed, err)
	}

	raw := strings.TrimSpace(resp.Message.Content)
	raw = stripKnowledgeJSONFences(raw)

	var entries []KnowledgeEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return fmt.Errorf("%w: parse: %w", ErrKnowledgeExtractFailed, err)
	}

	for _, entry := range entries {
		e.store.AddEntry(entry)
	}

	if err := e.store.Save(e.sessionID); err != nil {
		return fmt.Errorf("%w: save: %w", ErrKnowledgeExtractFailed, err)
	}
	return nil
}

// buildExtractionPrompt concatenates the user-prompt template with a
// human-readable rendering of each message. Identifier strings are not
// emitted; the extractor cares about semantic content, not tool ids.
//
// Expected:
//   - msgs is a non-empty slice.
//
// Returns:
//   - A single string ready for the user-message slot of a ChatRequest.
//
// Side effects:
//   - None.
func buildExtractionPrompt(msgs []provider.Message) string {
	var b strings.Builder
	for _, line := range knowledgeExtractorUserLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i, m := range msgs {
		role := m.Role
		if role == "" {
			role = "unknown"
		}
		fmt.Fprintf(&b, "[%d] %s: %s\n", i, role, m.Content)
	}
	return b.String()
}

// stripKnowledgeJSONFences removes a leading ```json / ``` fence and
// the matching trailing ``` when present. Mirrors
// context.stripJSONFences — duplicated here to avoid introducing a
// cross-package dependency for a trivial string operation.
//
// Expected:
//   - raw is the unprocessed LLM body (whitespace already trimmed by
//     the caller).
//
// Returns:
//   - The body with outer fences removed, or the input unchanged when
//     no leading fence is present.
//
// Side effects:
//   - None.
func stripKnowledgeJSONFences(raw string) string {
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	newline := strings.IndexByte(raw, '\n')
	if newline == -1 {
		return raw
	}
	body := strings.TrimSpace(raw[newline+1:])
	if strings.HasSuffix(body, "```") {
		body = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	return body
}
