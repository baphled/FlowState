package factstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fact is a durable, single-sentence claim extracted from session text.
// Embeddings are deferred to Phase C; the rank signal in Phase B is
// keyword overlap on Text with a recency tie-breaker on CreatedAt.
type Fact struct {
	ID              string    `json:"id"`
	Text            string    `json:"text"`
	SourceMessageID string    `json:"source_message_id"`
	SessionID       string    `json:"session_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// FactStore persists Facts per session and recalls them by query.
//
// Append is idempotent on (sessionID, fact.ID): repeated Append calls
// for the same id are silently dropped so replay-safe extractors do
// not double-write. List rereads from disk so process restarts do not
// lose state.
type FactStore interface {
	// Append writes facts under sessionID. Existing IDs are skipped.
	Append(ctx context.Context, sessionID string, facts ...Fact) error
	// List returns every persisted fact for sessionID in disk order.
	// An unknown session yields an empty slice and a nil error.
	List(ctx context.Context, sessionID string) ([]Fact, error)
	// Recall ranks the session's facts by query overlap and returns
	// the top-K most relevant. topK<=0 returns nil.
	Recall(ctx context.Context, sessionID string, query string, topK int) ([]Fact, error)
	// Path is the absolute on-disk path of the session's JSONL file.
	Path(sessionID string) string
}

// FileFactStore is the JSONL-backed FactStore. One instance per process
// is sufficient: per-session work is keyed off sessionID and disk
// writes are guarded by a per-session mutex so concurrent Append
// calls for the same session never interleave inside the JSONL line.
type FileFactStore struct {
	root string

	mu       sync.Mutex
	sessions map[string]*sync.Mutex
}

// NewFileFactStore constructs a FileFactStore rooted at root. The
// directory is created lazily by Append on first write.
//
// Expected:
//   - root is the absolute parent directory under which per-session
//     facts.jsonl files live (typically the active sessions dir). An
//     empty root disables writes — Append still succeeds but no file
//     is produced; useful for tests that exercise extraction without
//     touching disk.
//
// Returns:
//   - A configured *FileFactStore; never nil.
func NewFileFactStore(root string) *FileFactStore {
	return &FileFactStore{
		root:     root,
		sessions: make(map[string]*sync.Mutex),
	}
}

// Append persists facts for sessionID. IDs that already appear in the
// existing JSONL are skipped so replay-safe extractors do not produce
// duplicate entries. The file is created on first call.
//
// Expected:
//   - sessionID is non-empty.
//   - Each fact's Text is non-empty; empty-text facts are dropped.
//
// Returns:
//   - A non-nil error only when the underlying disk write fails.
//
// Side effects:
//   - Writes one JSON object per accepted fact, terminated by '\n'.
//   - File mode 0o600; parent dir 0o700.
func (s *FileFactStore) Append(_ context.Context, sessionID string, facts ...Fact) error {
	if sessionID == "" || len(facts) == 0 {
		return nil
	}

	mu := s.sessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	existing, err := s.readIDs(sessionID)
	if err != nil {
		return fmt.Errorf("factstore: reading existing ids: %w", err)
	}

	if s.root == "" {
		return nil
	}

	dir := filepath.Join(s.root, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("factstore: mkdir: %w", err)
	}

	path := filepath.Join(dir, "facts.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("factstore: open: %w", err)
	}
	defer f.Close()

	for _, fact := range facts {
		if !isValidFact(fact) {
			continue
		}
		fact = stampFact(fact)
		if _, dup := existing[fact.ID]; dup {
			continue
		}
		existing[fact.ID] = struct{}{}
		line, err := json.Marshal(fact)
		if err != nil {
			return fmt.Errorf("factstore: marshal: %w", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("factstore: write: %w", err)
		}
	}
	return nil
}

// List returns the session's persisted facts in disk order. An unknown
// session yields an empty slice (not an error) so the engine can call
// List on every build without first checking existence.
func (s *FileFactStore) List(_ context.Context, sessionID string) ([]Fact, error) {
	if sessionID == "" || s.root == "" {
		return nil, nil
	}
	path := filepath.Join(s.root, sessionID, "facts.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("factstore: open: %w", err)
	}
	defer f.Close()

	out := make([]Fact, 0, 16)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var fact Fact
		if err := json.Unmarshal([]byte(line), &fact); err != nil {
			continue
		}
		out = append(out, fact)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("factstore: scan: %w", err)
	}
	return out, nil
}

// Recall returns the top-K facts for sessionID ranked by overlap with
// query. topK<=0 returns nil so callers can guard the inclusion site
// with a single `if len(hits) == 0` check.
func (s *FileFactStore) Recall(ctx context.Context, sessionID string, query string, topK int) ([]Fact, error) {
	if topK <= 0 {
		return nil, nil
	}
	all, err := s.List(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return rankByOverlap(all, query, topK), nil
}

// Path returns the absolute path of the session's facts.jsonl. Useful
// for tests that assert persistence and for telemetry that exposes
// where a session's facts live on disk.
func (s *FileFactStore) Path(sessionID string) string {
	if sessionID == "" || s.root == "" {
		return ""
	}
	return filepath.Join(s.root, sessionID, "facts.jsonl")
}

// readIDs returns the set of fact IDs already present in the session's
// JSONL. Missing files yield an empty set with a nil error.
func (s *FileFactStore) readIDs(sessionID string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if s.root == "" {
		return out, nil
	}
	path := filepath.Join(s.root, sessionID, "facts.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var fact Fact
		if err := json.Unmarshal([]byte(line), &fact); err != nil {
			continue
		}
		if fact.ID != "" {
			out[fact.ID] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// sessionLock returns the per-session mutex, creating one on first use.
func (s *FileFactStore) sessionLock(sessionID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	mu, ok := s.sessions[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		s.sessions[sessionID] = mu
	}
	return mu
}

// isValidFact reports whether fact carries enough content to be worth
// persisting. Empty Text is the only disqualifier today; the rest of
// the fields self-heal in stampFact.
func isValidFact(fact Fact) bool {
	return strings.TrimSpace(fact.Text) != ""
}

// stampFact fills the auto-derived fields (ID, CreatedAt) when the
// caller has not set them. ID is content-derived so dedup is natural
// across replays; CreatedAt defaults to time.Now() so recency
// tie-breaking has a usable signal.
func stampFact(fact Fact) Fact {
	fact.Text = strings.TrimSpace(fact.Text)
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = time.Now().UTC()
	}
	if fact.ID == "" {
		fact.ID = factID(fact)
	}
	return fact
}

// factID returns a stable id derived from Text and SourceMessageID.
// 16 hex chars; never collides with itself for identical inputs.
func factID(fact Fact) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fact.Text))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(fact.SourceMessageID))
	return fmt.Sprintf("%016x", h.Sum64())
}

// rankByOverlap scores each fact by the keyword-overlap formula
// |query ∩ fact| / sqrt(max(1, |fact|)) with a recency tie-breaker
// (newer wins). Returns the top-K in descending score order.
func rankByOverlap(facts []Fact, query string, topK int) []Fact {
	if len(facts) == 0 || topK <= 0 {
		return nil
	}
	qTokens := tokenise(query)
	if len(qTokens) == 0 {
		// Empty query: degrade to "most recent K".
		sorted := append([]Fact(nil), facts...)
		sort.SliceStable(sorted, func(i, j int) bool {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		})
		if topK > len(sorted) {
			topK = len(sorted)
		}
		return sorted[:topK]
	}

	scored := make([]scoredFact, 0, len(facts))
	for _, f := range facts {
		fTokens := tokenise(f.Text)
		if len(fTokens) == 0 {
			continue
		}
		overlap := overlapCount(qTokens, fTokens)
		score := float64(overlap) / sqrtAtLeastOne(len(fTokens))
		scored = append(scored, scoredFact{fact: f, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].fact.CreatedAt.After(scored[j].fact.CreatedAt)
	})
	if topK > len(scored) {
		topK = len(scored)
	}
	out := make([]Fact, topK)
	for i := 0; i < topK; i++ {
		out[i] = scored[i].fact
	}
	return out
}

// scoredFact pairs a Fact with its overlap score for stable sorting.
type scoredFact struct {
	fact  Fact
	score float64
}
