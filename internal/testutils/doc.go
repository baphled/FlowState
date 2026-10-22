// Package testutils provides shared testing utilities for FlowState, with a focus on
// golden file recording and replay for provider e2e tests.
//
// This package implements a record-once/replay-forever strategy to minimise calls to
// external LLM providers during testing:
//   - Golden files are committed to git as the source of truth
//   - Tests replay chunks from golden files by default (zero provider calls)
//   - Recording mode (opt-in) makes ONE live call and caches the result
//   - sync.Once ensures no multiple live calls ever occur
//
// Typical usage:
//
//	recorder := NewGoldenRecorder("testdata/my_response.golden.json", myProvider)
//	chunks, err := recorder.Load(context.Background(), chatRequest)
//	// chunks come from golden file if it exists, or one live call (then cached)
//
// Package testutils manages the following types:
//   - GoldenRecorder: Loads from golden files, falls back to one live provider call
//   - GoldenPlayer: Deserialises golden files into provider.StreamChunk
//   - ReplayProvider: Provider implementation that emits pre-recorded chunks
//   - goldenChunk/goldenRecording: JSON serialisation types
package testutils
