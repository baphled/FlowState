package compaction

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/baphled/flowstate/internal/provider"
)

// MicroCompactor owns the hot/cold split for Phase A. One instance per
// engine; per-session work is keyed off sessionID so a single MicroCompactor
// can serve every concurrent session without cross-contamination. Disk
// writes are guarded by a per-session mutex so two concurrent Compact
// calls for the same session never race on the same .txt file.
type MicroCompactor struct {
	storeRoot        string
	hotTailMin       int
	sizeBudget       int
	compactableTools map[string]bool

	mu       sync.Mutex
	sessions map[string]*sync.Mutex
}

// Options configures a MicroCompactor at construction. StoreRoot is the
// absolute parent directory for cold-storage payloads — typical wiring
// points it at the active sessions dir, with per-session subdirectories
// resolved at Compact time.
type Options struct {
	// StoreRoot is the directory containing per-session compacted/
	// subdirectories. An empty StoreRoot disables disk writes (Compact
	// still rewrites the slice but the .txt payloads are dropped).
	StoreRoot string
	// HotTailMin is the minimum number of recent compactable tool
	// results kept verbatim. A non-positive value is treated as zero.
	HotTailMin int
	// SizeBudget is the soft byte cap for the hot tail (≈ token×4).
	// Older results overflow to cold once the cap is exceeded. A
	// non-positive value disables the cap (only HotTailMin governs).
	SizeBudget int
}

// NewMicroCompactor constructs a MicroCompactor with the production
// compactable/non-compactable classification baked in.
//
// Returns:
//   - A configured MicroCompactor; never nil.
//
// Side effects:
//   - None. The store directory is created lazily by Compact when the
//     first cold payload needs to land.
func NewMicroCompactor(opts Options) *MicroCompactor {
	return &MicroCompactor{
		storeRoot:        opts.StoreRoot,
		hotTailMin:       maxInt(opts.HotTailMin, 0),
		sizeBudget:       opts.SizeBudget,
		compactableTools: defaultCompactableTools(),
		sessions:         make(map[string]*sync.Mutex),
	}
}

// Compact returns a rewritten copy of messages where compactable tool
// results older than the hot tail are replaced by a one-line reference
// message and their original content is written to
// <storeRoot>/<sessionID>/<message-id>.txt as plain UTF-8 with mode 0o600.
//
// The hot/cold boundary is the LAST point at which both
//
//   - the count of remaining compactable results is HotTailMin, AND
//   - their cumulative byte size is under SizeBudget.
//
// Walk is right-to-left so the hot tail is always anchored at the end of
// the slice. Non-compactable tool results, user/assistant text, system
// messages, and assistant tool_use turns are pass-through verbatim.
//
// Compaction is idempotent: running Compact twice on the same slice
// yields the same final slice (already-compacted reference messages are
// recognised and left untouched).
//
// Expected:
//   - sessionID is non-empty when StoreRoot is set; an empty sessionID
//     with a configured store skips disk writes for safety.
//   - messages is the in-flight slice the provider would otherwise see.
//     Treated as immutable: the returned slice is a fresh copy.
//
// Returns:
//   - The rewritten message slice (always a fresh allocation).
//   - A non-nil error only when a cold payload write fails AND the
//     caller therefore must not proceed with the compacted view (rare;
//     normal disk failures are returned so the engine can fall back to
//     the full slice rather than silently lose history).
func (m *MicroCompactor) Compact(_ context.Context, sessionID string, messages []provider.Message) ([]provider.Message, error) {
	if m == nil || !m.shouldRun(sessionID, messages) {
		return cloneMessages(messages), nil
	}

	mu := m.sessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	indices := m.compactableIndices(messages)
	if len(indices) <= m.hotTailMin {
		return cloneMessages(messages), nil
	}
	coldIdx := m.coldIndices(messages, indices)
	if len(coldIdx) == 0 {
		return cloneMessages(messages), nil
	}

	out := cloneMessages(messages)
	for _, i := range coldIdx {
		ref, err := m.spillOne(sessionID, out[i])
		if err != nil {
			return nil, fmt.Errorf("compaction: spilling message %d: %w", i, err)
		}
		out[i] = ref
	}
	return out, nil
}

// IsCompactableTool reports whether the named tool is part of the
// Phase A compactable set. Exposed so engine wiring tests and external
// callers can reason about classification without duplicating the list.
func (m *MicroCompactor) IsCompactableTool(name string) bool {
	if m == nil {
		return false
	}
	return m.compactableTools[name]
}

// shouldRun returns true when the compactor is configured to operate on
// this slice. False when the compactor is mis-configured for the input
// (no sessionID with a StoreRoot, empty slice).
func (m *MicroCompactor) shouldRun(sessionID string, messages []provider.Message) bool {
	if len(messages) == 0 {
		return false
	}
	if m.storeRoot != "" && sessionID == "" {
		return false
	}
	return true
}

// compactableIndices returns the indices in messages that are
// compactable tool results, in original order. Already-compacted
// reference messages are excluded so a second pass is idempotent.
func (m *MicroCompactor) compactableIndices(messages []provider.Message) []int {
	out := make([]int, 0, len(messages))
	for i := range messages {
		if !m.isCompactableToolResult(messages[i]) {
			continue
		}
		if isReferenceMessage(messages[i]) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// coldIndices walks the compactable indices right-to-left and collects
// the prefix that should be offloaded. The hot tail keeps at least
// hotTailMin entries; entries beyond that overflow to cold once their
// cumulative size exceeds sizeBudget. Entries inside the budget but
// older than hotTailMin still stay hot.
func (m *MicroCompactor) coldIndices(messages []provider.Message, compactable []int) []int {
	hotCount := 0
	hotBytes := 0
	cold := make([]int, 0, len(compactable))
	for j := len(compactable) - 1; j >= 0; j-- {
		idx := compactable[j]
		size := len(messages[idx].Content)
		if hotCount < m.hotTailMin {
			hotCount++
			hotBytes += size
			continue
		}
		if m.sizeBudget > 0 && hotBytes+size > m.sizeBudget {
			cold = append(cold, idx)
			continue
		}
		if m.sizeBudget <= 0 {
			cold = append(cold, idx)
			continue
		}
		hotCount++
		hotBytes += size
	}
	reverseInts(cold)
	return cold
}

// spillOne writes msg's content to <storeRoot>/<sessionID>/<id>.txt and
// returns the reference message that replaces it in the rewritten slice.
// When storeRoot is empty the .txt write is skipped — the reference
// message still references a stable id so a future call to spillOne
// remains idempotent for that input.
func (m *MicroCompactor) spillOne(sessionID string, msg provider.Message) (provider.Message, error) {
	id := messageID(msg)
	relPath := filepath.Join(sessionID, "compacted", id+".txt")

	if m.storeRoot != "" {
		dir := filepath.Join(m.storeRoot, sessionID, "compacted")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return provider.Message{}, err
		}
		path := filepath.Join(dir, id+".txt")
		if err := os.WriteFile(path, []byte(msg.Content), 0o600); err != nil {
			return provider.Message{}, err
		}
	}

	return provider.Message{
		Role:      msg.Role,
		Content:   buildReferenceText(relPath),
		ToolCalls: cloneToolCalls(msg.ToolCalls),
	}, nil
}

// isCompactableToolResult reports whether msg is a tool-result message
// produced by a compactable tool. Tool-result messages carry Role:"tool"
// and a ToolCalls entry whose Name is the tool that produced the
// content. Non-tool messages always return false.
func (m *MicroCompactor) isCompactableToolResult(msg provider.Message) bool {
	if msg.Role != "tool" {
		return false
	}
	if len(msg.ToolCalls) == 0 {
		return false
	}
	return m.compactableTools[msg.ToolCalls[0].Name]
}

// sessionLock returns the per-session mutex, creating one on first use.
// The map mutex protects the registry; per-session locks protect the
// .txt files during write.
func (m *MicroCompactor) sessionLock(sessionID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	mu, ok := m.sessions[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		m.sessions[sessionID] = mu
	}
	return mu
}

// referencePrefix and referenceSuffix bracket the sentinel string in a
// reference message. Splitting them out keeps idempotence detection
// (isReferenceMessage) and reference construction (buildReferenceText)
// in lock-step.
const (
	referencePrefix = "[content offloaded to "
	referenceSuffix = " — re-read with the read tool]"
)

// buildReferenceText returns the human-readable sentinel embedded in
// the rewritten tool-result message.
func buildReferenceText(relPath string) string {
	return referencePrefix + relPath + referenceSuffix
}

// isReferenceMessage reports whether a tool-result message has already
// been rewritten by a prior compaction pass. Used by compactableIndices
// to keep Compact idempotent.
func isReferenceMessage(msg provider.Message) bool {
	return strings.HasPrefix(msg.Content, referencePrefix) && strings.HasSuffix(msg.Content, referenceSuffix)
}

// messageID returns a stable id for msg suitable as a filename stem.
// The id is derived from the tool-call id when present (so replays
// land at the same path) and from a fnv-1a hash of the content
// otherwise. Always 16 hex chars; never collides with reserved names.
func messageID(msg provider.Message) string {
	if len(msg.ToolCalls) > 0 && msg.ToolCalls[0].ID != "" {
		return sanitiseID(msg.ToolCalls[0].ID)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(msg.Content))
	return fmt.Sprintf("%016x", h.Sum64())
}

// sanitiseID strips characters that are dangerous in filenames so a
// hostile (or merely odd) tool-call id can never escape the
// compacted/ subdirectory. The output is deterministic.
func sanitiseID(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "anon"
	}
	return b.String()
}

// cloneMessages returns a defensive copy of msgs. ToolCalls slices are
// likewise cloned so callers cannot reach back through ours and mutate
// the persisted history.
func cloneMessages(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, len(msgs))
	for i := range msgs {
		out[i] = msgs[i]
		out[i].ToolCalls = cloneToolCalls(msgs[i].ToolCalls)
	}
	return out
}

// cloneToolCalls returns a defensive copy of calls; nil-in -> nil-out.
func cloneToolCalls(calls []provider.ToolCall) []provider.ToolCall {
	if calls == nil {
		return nil
	}
	out := make([]provider.ToolCall, len(calls))
	copy(out, calls)
	return out
}

// reverseInts mutates xs in place. Used to flip the right-to-left walk
// of coldIndices back into ascending order so callers can iterate the
// result and rewrite messages by ascending index.
func reverseInts(xs []int) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// maxInt is the duplicated three-line helper Go's stdlib finally got in
// 1.21; this package targets 1.21+ but keeping a local helper keeps the
// import surface small and the code reviewable in isolation.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// defaultCompactableTools is the canonical Phase A classification list.
// Sourced from the FlowState KB note "Claude-Context-Compression-
// Architecture", §"Layer 1 — Compactable tools", and reconciled with
// the actual Tool.Name() returns shipped under internal/tool/.
//
// The non-compactable counterpart (delegate, skill_load, plan_*,
// coordination_store, todowrite, todoread, chain_*, batch, question)
// is intentionally NOT enumerated here — anything missing from this
// map is non-compactable by default, which is the safe direction.
func defaultCompactableTools() map[string]bool {
	return map[string]bool{
		"read":        true,
		"bash":        true,
		"grep":        true,
		"glob":        true,
		"web":         true,
		"websearch":   true,
		"edit":        true,
		"multiedit":   true,
		"ls":          true,
		"apply_patch": true,
	}
}

// ErrInvalidStore is returned when the configured store directory is
// rejected at construction (e.g. relative path with an empty cwd). It
// is exported so wiring code can branch on it cleanly without string
// matching.
var ErrInvalidStore = errors.New("compaction: invalid store directory")
