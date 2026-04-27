package factstore

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// Service is the engine-facing facade over a FactExtractor + FactStore
// pair. One Service per engine; per-session state lives entirely
// inside the underlying store.
type Service struct {
	store     FactStore
	extractor FactExtractor
	cfg       Config
}

// NewService wires an extractor and a store under a single Config.
//
// Expected:
//   - store is non-nil; nil disables persistence.
//   - extractor is non-nil; nil disables ingestion (Recall still
//     reads any pre-existing JSONL).
//
// Returns:
//   - A configured *Service; never nil.
func NewService(store FactStore, extractor FactExtractor, cfg Config) *Service {
	ApplyDefaults(&cfg)
	return &Service{store: store, extractor: extractor, cfg: cfg}
}

// IngestSession runs the extractor over msgs and appends every
// resulting Fact under sessionID. Idempotent: rerunning on the same
// msgs yields no duplicate JSONL lines (the store dedups by ID).
//
// Expected:
//   - sessionID is non-empty.
//   - msgs is the session's full message history.
//
// Returns:
//   - A non-nil error only when extraction OR persistence fails.
func (s *Service) IngestSession(ctx context.Context, sessionID string, msgs []provider.Message) error {
	if s == nil || s.store == nil || s.extractor == nil {
		return nil
	}
	facts, err := s.extractor.Extract(ctx, sessionID, msgs)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		return nil
	}
	return s.store.Append(ctx, sessionID, facts...)
}

// Recall returns the top-K query-relevant facts for sessionID. When
// topK<=0 the configured default (cfg.RecallTopK) is used so the
// engine wire-in can pass 0 and rely on the configured surface.
func (s *Service) Recall(ctx context.Context, sessionID string, query string, topK int) ([]Fact, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	if topK <= 0 {
		topK = s.cfg.RecallTopK
	}
	return s.store.Recall(ctx, sessionID, query, topK)
}

// List exposes the full per-session Fact list. Used by tests; the
// engine wire-in only ever calls Recall.
func (s *Service) List(ctx context.Context, sessionID string) ([]Fact, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.List(ctx, sessionID)
}

// Config returns the resolved Config (defaults applied).
func (s *Service) Config() Config {
	if s == nil {
		return DefaultConfig()
	}
	return s.cfg
}
