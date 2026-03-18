package context

// GetStoredMessages returns a copy of all stored messages with their metadata.
//
// Returns:
//   - A slice containing copies of all StoredMessage entries.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetStoredMessages() []StoredMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]StoredMessage, len(s.messages))
	copy(result, s.messages)
	return result
}

// GetEmbeddings returns a copy of all stored embedding entries.
//
// Returns:
//   - A slice containing copies of all EmbeddingEntry values.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetEmbeddings() []EmbeddingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]EmbeddingEntry, len(s.embeddings))
	copy(result, s.embeddings)
	return result
}

// GetModel returns the embedding model name used by this store.
//
// Returns:
//   - The embedding model string configured for this store.
//
// Side effects:
//   - None.
func (s *FileContextStore) GetModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

// LoadFromSession restores messages and embeddings from a saved session.
//
// Expected:
//   - messages is the list of stored messages to restore.
//   - embeddings is the list of embedding entries to restore.
//   - model is the embedding model used in the saved session.
//
// Side effects:
//   - Replaces the current messages and embeddings with the provided data.
//   - Discards embeddings if the model does not match the store's configured model.
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
