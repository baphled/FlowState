package context

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/google/uuid"
)

// MessageStore defines operations for managing conversation messages.
type MessageStore interface {
	// Append adds a message to the store.
	Append(msg provider.Message)
	// GetRange returns messages between the given indices.
	GetRange(start, end int) []provider.Message
	// GetRecent returns the n most recent messages.
	GetRecent(n int) []provider.Message
	// Count returns the total number of messages.
	Count() int
	// AllMessages returns all messages in the store.
	AllMessages() []provider.Message
}

// SearchResult represents a search result with relevance score.
type SearchResult struct {
	MessageID string
	Score     float64
	Message   provider.Message
}

// EmbeddingStore provides operations for storing and searching embeddings.
type EmbeddingStore interface {
	// StoreEmbedding stores an embedding vector for a message.
	StoreEmbedding(msgID string, vector []float64, model string, dimensions int)
	// Search finds the most similar messages to the query vector.
	Search(query []float64, topK int) []SearchResult
}

// StoredMessage represents a message with its metadata for persistence.
type StoredMessage struct {
	ID       string           `json:"id"`
	Message  provider.Message `json:"message"`
	Embedded bool             `json:"embedded"`
}

// EmbeddingEntry represents an embedding vector with its metadata.
type EmbeddingEntry struct {
	MsgID      string    `json:"msg_id"`
	Vector     []float64 `json:"vector"`
	Model      string    `json:"model"`
	Dimensions int       `json:"dimensions"`
}

type persistedStore struct {
	Messages   []StoredMessage  `json:"messages"`
	Embeddings []EmbeddingEntry `json:"embeddings"`
	Model      string           `json:"model"`
}

// FileContextStore implements MessageStore and EmbeddingStore with file persistence.
type FileContextStore struct {
	path       string
	messages   []StoredMessage
	embeddings []EmbeddingEntry
	mu         sync.RWMutex
	maxSize    int
	model      string
}

const defaultMaxSize = 1000

// NewFileContextStore creates a new file-backed context store at the given path.
//
// Expected:
//   - path is a valid file path for persisting messages.
//   - embeddingModel is the name of the embedding model to use.
//
// Returns:
//   - A configured FileContextStore on success.
//   - An error if the directory cannot be created or existing data cannot be loaded.
//
// Side effects:
//   - Creates the parent directory if it does not exist.
//   - Reads existing data from the file if present.
func NewFileContextStore(path, embeddingModel string) (*FileContextStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating directory: %w", err)
	}

	store := &FileContextStore{
		path:       path,
		messages:   make([]StoredMessage, 0),
		embeddings: make([]EmbeddingEntry, 0),
		maxSize:    defaultMaxSize,
		model:      embeddingModel,
	}

	if err := store.load(); err != nil {
		return nil, fmt.Errorf("loading store: %w", err)
	}

	return store, nil
}

func (s *FileContextStore) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	var persisted persistedStore
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("unmarshalling: %w", err)
	}

	s.messages = persisted.Messages
	if s.messages == nil {
		s.messages = make([]StoredMessage, 0)
	}

	if persisted.Model == s.model {
		s.embeddings = persisted.Embeddings
		if s.embeddings == nil {
			s.embeddings = make([]EmbeddingEntry, 0)
		}
	} else {
		s.embeddings = make([]EmbeddingEntry, 0)
	}

	return nil
}

func (s *FileContextStore) persist() error {
	persisted := persistedStore{
		Messages:   s.messages,
		Embeddings: s.embeddings,
		Model:      s.model,
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// Append adds a message to the store and persists the updated state.
//
// Expected:
//   - msg is a valid provider.Message to store.
//
// Side effects:
//   - Persists the updated message list to disk.
//   - Evicts the oldest message if the store exceeds its maximum size.
func (s *FileContextStore) Append(msg provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sm := StoredMessage{
		ID:       uuid.New().String(),
		Message:  msg,
		Embedded: false,
	}

	s.messages = append(s.messages, sm)

	if len(s.messages) > s.maxSize {
		s.messages = s.messages[1:]
	}

	if err := s.persist(); err != nil {
		log.Printf("warning: %v", err)
	}
}

// GetRange returns messages between the given start and end indices.
//
// Expected:
//   - start is the inclusive lower bound index.
//   - end is the exclusive upper bound index.
//
// Returns:
//   - A slice of messages within the specified range.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetRange(start, end int) []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if start < 0 {
		start = 0
	}
	if end > len(s.messages) {
		end = len(s.messages)
	}
	if start >= end {
		return []provider.Message{}
	}

	result := make([]provider.Message, end-start)
	for i := start; i < end; i++ {
		result[i-start] = s.messages[i].Message
	}
	return result
}

// GetRecent returns the n most recent messages from the store.
//
// Expected:
//   - n is the number of recent messages to retrieve.
//
// Returns:
//   - A slice containing up to n most recent messages.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetRecent(n int) []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.messages)
	start := total - n
	if start < 0 {
		start = 0
	}

	result := make([]provider.Message, total-start)
	for i := start; i < total; i++ {
		result[i-start] = s.messages[i].Message
	}
	return result
}

// Count returns the total number of stored messages.
//
// Returns:
//   - The number of messages currently in the store.
//
// Side effects:
//   - None.
func (s *FileContextStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

// AllMessages returns a copy of all messages in the store.
//
// Returns:
//   - A slice containing all stored messages.
//
// Side effects:
//   - None.
func (s *FileContextStore) AllMessages() []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]provider.Message, len(s.messages))
	for i, sm := range s.messages {
		result[i] = sm.Message
	}
	return result
}

// GetMessageID returns the ID of the message at the given index.
//
// Expected:
//   - index is a zero-based position within the stored messages.
//
// Returns:
//   - The message ID, or an empty string if the index is out of bounds.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetMessageID(index int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if index < 0 || index >= len(s.messages) {
		return ""
	}
	return s.messages[index].ID
}

// StoreEmbedding stores an embedding vector for the specified message.
//
// Expected:
//   - msgID is the ID of an existing stored message.
//   - vector is a non-empty embedding vector.
//   - model is the name of the embedding model used.
//   - dimensions is the dimensionality of the vector.
//
// Side effects:
//   - Marks the message as embedded in the store.
//   - Persists the updated embeddings to disk.
func (s *FileContextStore) StoreEmbedding(msgID string, vector []float64, model string, dimensions int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.messages {
		if s.messages[i].ID == msgID {
			s.messages[i].Embedded = true
			break
		}
	}

	entry := EmbeddingEntry{
		MsgID:      msgID,
		Vector:     vector,
		Model:      model,
		Dimensions: dimensions,
	}
	s.embeddings = append(s.embeddings, entry)

	if err := s.persist(); err != nil {
		log.Printf("warning: %v", err)
	}
}

// Search finds the most similar messages to the query vector using cosine similarity.
//
// Expected:
//   - query is a non-empty embedding vector to compare against.
//   - topK is the maximum number of results to return.
//
// Returns:
//   - A slice of SearchResult sorted by descending similarity score.
//
// Side effects:
//   - None.
func (s *FileContextStore) Search(query []float64, topK int) []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.embeddings) == 0 {
		return []SearchResult{}
	}

	msgByID := make(map[string]StoredMessage)
	for _, sm := range s.messages {
		msgByID[sm.ID] = sm
	}

	var results []SearchResult
	for _, emb := range s.embeddings {
		sm, ok := msgByID[emb.MsgID]
		if !ok {
			continue
		}

		if sm.Message.Role == "tool" {
			continue
		}

		score := CosineSimilarity(query, emb.Vector)
		results = append(results, SearchResult{
			MessageID: emb.MsgID,
			Score:     score,
			Message:   sm.Message,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	return results
}

// CosineSimilarity computes the cosine similarity between two vectors.
//
// Expected:
//   - a and b are equal-length, non-empty float64 slices.
//
// Returns:
//   - A value between -1 and 1 representing the cosine similarity, or 0 if inputs are invalid.
//
// Side effects:
//   - None.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
