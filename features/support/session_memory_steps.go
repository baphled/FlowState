package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// sessionMemoryState holds per-scenario state for @session-memory
// scenarios. One instance is reused across scenarios with a Before
// hook that resets every field so state never leaks.
type sessionMemoryState struct {
	tempDir string

	store    *recall.SessionMemoryStore
	reloaded *recall.SessionMemoryStore

	retrieved []recall.KnowledgeEntry

	extractor    *recall.KnowledgeExtractor
	extractorErr error
}

// RegisterSessionMemorySteps wires the @session-memory scenarios to
// the real SessionMemoryStore and KnowledgeExtractor. The steps are
// deliberately thin: each Given sets up state, each When exercises the
// production API, and each Then asserts an observable outcome.
//
// Expected:
//   - ctx is a non-nil godog.ScenarioContext.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers Given/When/Then steps and Before/After hooks that
//     allocate and release a temp directory for the memory store.
func RegisterSessionMemorySteps(ctx *godog.ScenarioContext) {
	state := &sessionMemoryState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "session-memory-bdd-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		state.tempDir = dir
		state.store = recall.NewSessionMemoryStore(dir)
		state.reloaded = nil
		state.retrieved = nil
		state.extractor = nil
		state.extractorErr = nil
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.tempDir != "" {
			_ = os.RemoveAll(state.tempDir)
			state.tempDir = ""
		}
		return c, nil
	})

	ctx.Step(`^a session memory store is seeded with (\d+) facts and (\d+) preference$`, func(facts, prefs int) error {
		for i := range facts {
			state.store.AddEntry(recall.KnowledgeEntry{
				ID:        fmt.Sprintf("f%d", i),
				Type:      "fact",
				Content:   fmt.Sprintf("fact entry %d", i),
				Relevance: 0.8,
			})
		}
		for i := range prefs {
			state.store.AddEntry(recall.KnowledgeEntry{
				ID:        fmt.Sprintf("p%d", i),
				Type:      "preference",
				Content:   fmt.Sprintf("pref entry %d", i),
				Relevance: 0.6,
			})
		}
		return nil
	})

	ctx.Step(`^the store is saved and reloaded into a fresh instance$`, func() error {
		if err := state.store.Save("bdd-roundtrip"); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		state.reloaded = recall.NewSessionMemoryStore(state.tempDir)
		if err := state.reloaded.Load("bdd-roundtrip"); err != nil {
			return fmt.Errorf("load: %w", err)
		}
		return nil
	})

	ctx.Step(`^the reloaded store exposes every original entry$`, func() error {
		orig := state.store.Entries()
		got := state.reloaded.Entries()
		if len(orig) != len(got) {
			return fmt.Errorf("reloaded len = %d; want %d", len(got), len(orig))
		}
		for i := range orig {
			if orig[i].ID != got[i].ID || orig[i].Content != got[i].Content {
				return fmt.Errorf("entry %d drifted after roundtrip: %+v vs %+v", i, got[i], orig[i])
			}
		}
		return nil
	})

	ctx.Step(`^a session memory store with mixed-type entries$`, func() error {
		state.store.AddEntry(recall.KnowledgeEntry{Type: "fact", Content: "high fact", Relevance: 0.9})
		state.store.AddEntry(recall.KnowledgeEntry{Type: "fact", Content: "mid fact", Relevance: 0.6})
		state.store.AddEntry(recall.KnowledgeEntry{Type: "fact", Content: "low fact", Relevance: 0.2}) // filtered out
		state.store.AddEntry(recall.KnowledgeEntry{Type: "convention", Content: "conv", Relevance: 0.7})
		return nil
	})

	ctx.Step(`^I retrieve up to (\d+) fact entries$`, func(limit int) error {
		state.retrieved = state.store.Retrieve("fact", limit)
		return nil
	})

	ctx.Step(`^the retrieved list is sorted by relevance descending$`, func() error {
		for i := 1; i < len(state.retrieved); i++ {
			if state.retrieved[i-1].Relevance < state.retrieved[i].Relevance {
				return fmt.Errorf("retrieved[%d].Relevance %f < retrieved[%d].Relevance %f; want descending",
					i-1, state.retrieved[i-1].Relevance, i, state.retrieved[i].Relevance)
			}
		}
		return nil
	})

	ctx.Step(`^no retrieved entry has relevance below (\d+\.\d+)$`, func(floor float64) error {
		for i, e := range state.retrieved {
			if e.Relevance < floor {
				return fmt.Errorf("retrieved[%d].Relevance = %f; want >= %f", i, e.Relevance, floor)
			}
		}
		return nil
	})

	ctx.Step(`^a knowledge extractor backed by a scripted provider returning (\d+) entries$`, func(n int) error {
		entries := make([]recall.KnowledgeEntry, 0, n)
		for i := range n {
			entries = append(entries, recall.KnowledgeEntry{
				ID:        fmt.Sprintf("ke%d", i),
				Type:      "fact",
				Content:   fmt.Sprintf("extracted entry %d", i),
				Relevance: 0.5,
			})
		}
		body, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("marshal scripted response: %w", err)
		}
		prov := &sessionMemoryScriptedProvider{
			response: provider.ChatResponse{Message: provider.Message{Content: string(body)}},
		}
		state.extractor = recall.NewKnowledgeExtractor(prov, state.store, "bdd-extract")
		return nil
	})

	ctx.Step(`^the extractor runs twice on the same transcript$`, func() error {
		msgs := []provider.Message{
			{Role: "user", Content: "what's the convention?"},
			{Role: "assistant", Content: "use snake_case"},
		}
		if err := state.extractor.Extract(context.Background(), msgs); err != nil {
			state.extractorErr = err
			return fmt.Errorf("first extract: %w", err)
		}
		if err := state.extractor.Extract(context.Background(), msgs); err != nil {
			state.extractorErr = err
			return fmt.Errorf("second extract: %w", err)
		}
		return nil
	})

	ctx.Step(`^the session memory store holds (\d+) unique entries$`, func(want int) error {
		if state.extractorErr != nil {
			return fmt.Errorf("extractor error: %w", state.extractorErr)
		}
		got := len(state.store.Entries())
		if got != want {
			return fmt.Errorf("store entries = %d; want %d", got, want)
		}
		return nil
	})
}

// sessionMemoryScriptedProvider is a provider.Provider double for the
// knowledge-extractor scenarios. Only Chat matters; the other methods
// are stubbed to satisfy the interface.
type sessionMemoryScriptedProvider struct {
	response provider.ChatResponse
	err      error
}

// Name returns a stable identifier for the stub.
//
// Expected:
//   - None.
//
// Returns:
//   - The literal string "bdd-session-memory-stub".
//
// Side effects:
//   - None.
func (p *sessionMemoryScriptedProvider) Name() string { return "bdd-session-memory-stub" }

// Stream returns a closed channel — the stub never streams.
//
// Expected:
//   - None.
//
// Returns:
//   - A closed chan provider.StreamChunk and a nil error.
//
// Side effects:
//   - None.
func (p *sessionMemoryScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

// Chat returns the scripted response (or error).
//
// Expected:
//   - ctx and req are ignored; the stub is fully scripted.
//
// Returns:
//   - The preconfigured response on success.
//   - The preconfigured error when p.err is non-nil.
//
// Side effects:
//   - None.
func (p *sessionMemoryScriptedProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	if p.err != nil {
		return provider.ChatResponse{}, p.err
	}
	return p.response, nil
}

// Embed is unused; returns zero values.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and nil error.
//
// Side effects:
//   - None.
func (p *sessionMemoryScriptedProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models is unused; returns zero values.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and nil error.
//
// Side effects:
//   - None.
func (p *sessionMemoryScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// suppress "imported and not used" for errors package (used by sibling
// steps file; kept here so future step additions can reuse it).
var _ = errors.New
