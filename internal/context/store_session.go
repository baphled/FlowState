package context

// GetStoredMessages returns a copy of all stored messages.
func (s *FileContextStore) GetStoredMessages() []StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredMessage, len(s.messages))
	copy(result, s.messages)
	return result
}

// GetEmbeddings returns a copy of all stored embeddings.
func (s *FileContextStore) GetEmbeddings() []EmbeddingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]EmbeddingEntry, len(s.embeddings))
	copy(result, s.embeddings)
	return result
}

// GetModel returns the embedding model used by this store.
func (s *FileContextStore) GetModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

// LoadFromSession restores messages and embeddings from a saved session.
func (s *FileContextStore) LoadFromSession(messages []StoredMessage, embeddings []EmbeddingEntry, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]StoredMessage, len(messages))
	copy(s.messages, messages)

	if model == s.model {
		s.embeddings = make([]EmbeddingEntry, len(embeddings))
		copy(s.embeddings, embeddings)
	} else {
		s.embeddings = make([]EmbeddingEntry, 0)
	}
}
