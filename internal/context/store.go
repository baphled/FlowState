package context

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/google/uuid"
)

type MessageStore interface {
	Append(msg provider.Message)
	GetRange(start, end int) []provider.Message
	GetRecent(n int) []provider.Message
	Count() int
	AllMessages() []provider.Message
}

type SearchResult struct {
	MessageID string
	Score     float64
	Message   provider.Message
}

type EmbeddingStore interface {
	StoreEmbedding(msgID string, vector []float64, model string, dimensions int)
	Search(query []float64, topK int) []SearchResult
}

type storedMessage struct {
	ID       string           `json:"id"`
	Message  provider.Message `json:"message"`
	Embedded bool             `json:"embedded"`
}

type embeddingEntry struct {
	MsgID      string    `json:"msg_id"`
	Vector     []float64 `json:"vector"`
	Model      string    `json:"model"`
	Dimensions int       `json:"dimensions"`
}

type persistedStore struct {
	Messages   []storedMessage  `json:"messages"`
	Embeddings []embeddingEntry `json:"embeddings"`
	Model      string           `json:"model"`
}

type FileContextStore struct {
	path       string
	messages   []storedMessage
	embeddings []embeddingEntry
	mu         sync.RWMutex
	maxSize    int
	model      string
}

const defaultMaxSize = 1000

func NewFileContextStore(path, embeddingModel string) (*FileContextStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating directory: %w", err)
	}

	store := &FileContextStore{
		path:       path,
		messages:   make([]storedMessage, 0),
		embeddings: make([]embeddingEntry, 0),
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
		s.messages = make([]storedMessage, 0)
	}

	if persisted.Model == s.model {
		s.embeddings = persisted.Embeddings
		if s.embeddings == nil {
			s.embeddings = make([]embeddingEntry, 0)
		}
	} else {
		s.embeddings = make([]embeddingEntry, 0)
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
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

func (s *FileContextStore) Append(msg provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sm := storedMessage{
		ID:       uuid.New().String(),
		Message:  msg,
		Embedded: false,
	}

	s.messages = append(s.messages, sm)

	if len(s.messages) > s.maxSize {
		s.messages = s.messages[1:]
	}

	_ = s.persist()
}

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

func (s *FileContextStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

func (s *FileContextStore) AllMessages() []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]provider.Message, len(s.messages))
	for i, sm := range s.messages {
		result[i] = sm.Message
	}
	return result
}

func (s *FileContextStore) GetMessageID(index int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if index < 0 || index >= len(s.messages) {
		return ""
	}
	return s.messages[index].ID
}

func (s *FileContextStore) StoreEmbedding(msgID string, vector []float64, model string, dimensions int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.messages {
		if s.messages[i].ID == msgID {
			s.messages[i].Embedded = true
			break
		}
	}

	entry := embeddingEntry{
		MsgID:      msgID,
		Vector:     vector,
		Model:      model,
		Dimensions: dimensions,
	}
	s.embeddings = append(s.embeddings, entry)

	_ = s.persist()
}

func (s *FileContextStore) Search(query []float64, topK int) []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.embeddings) == 0 {
		return []SearchResult{}
	}

	msgByID := make(map[string]storedMessage)
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
