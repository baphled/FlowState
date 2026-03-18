package context

func (s *FileContextStore) GetStoredMessages() []storedMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]storedMessage, len(s.messages))
	copy(result, s.messages)
	return result
}

func (s *FileContextStore) GetEmbeddings() []embeddingEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]embeddingEntry, len(s.embeddings))
	copy(result, s.embeddings)
	return result
}

func (s *FileContextStore) GetModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

func (s *FileContextStore) LoadFromSession(messages []storedMessage, embeddings []embeddingEntry, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]storedMessage, len(messages))
	copy(s.messages, messages)

	if model == s.model {
		s.embeddings = make([]embeddingEntry, len(embeddings))
		copy(s.embeddings, embeddings)
	} else {
		s.embeddings = make([]embeddingEntry, 0)
	}
}
