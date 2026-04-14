package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// compressionState holds per-scenario state for the @micro-compaction
// feature. It is created fresh in each scenario via Before hook and
// released at scenario end.
type compressionState struct {
	threshold   int
	hotTailSize int
	storageDir  string
	sessionID   string

	splitter *flowctx.HotColdSplitter
	input    []provider.Message
	snapshot []provider.Message
	result   flowctx.SplitResult
}

// RegisterCompressionSteps wires the @micro-compaction feature to the
// production HotColdSplitter API. No mocks: the steps drive the real
// splitter against a per-scenario temp directory.
//
// Expected:
//   - ctx is a non-nil godog.ScenarioContext.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers Given/When/Then steps and a Before-hook that allocates
//     scenario-scoped state under a t.TempDir-style directory.
func RegisterCompressionSteps(ctx *godog.ScenarioContext) {
	state := &compressionState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.threshold = 0
		state.hotTailSize = 0
		state.input = nil
		state.snapshot = nil
		state.result = flowctx.SplitResult{}

		dir, err := os.MkdirTemp("", "compression-bdd-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		state.storageDir = dir
		state.sessionID = "bdd-session"
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.splitter != nil {
			state.splitter.Stop()
			state.splitter = nil
		}
		if state.storageDir != "" {
			_ = os.RemoveAll(state.storageDir)
			state.storageDir = ""
		}
		return c, nil
	})

	ctx.Step(
		`^the L1 micro-compaction layer is configured with a (\d+)-token threshold and a (\d+)-message hot tail$`,
		func(threshold, hotTail int) error {
			state.threshold = threshold
			state.hotTailSize = hotTail
			return nil
		},
	)

	ctx.Step(`^I have appended a sequence of (\d+) small assistant messages$`, func(n int) error {
		state.input = make([]provider.Message, 0, n)
		for i := range n {
			state.input = append(state.input, provider.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("small-%d", i),
			})
		}
		state.snapshot = cloneMessages(state.input)
		return nil
	})

	ctx.Step(`^I have appended a sequence of (\d+) large assistant messages$`, func(n int) error {
		state.input = make([]provider.Message, 0, n)
		for i := range n {
			state.input = append(state.input, provider.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("large-%d %s", i, strings.Repeat("word ", 80)),
			})
		}
		state.snapshot = cloneMessages(state.input)
		return nil
	})

	ctx.Step(`^I have appended a parallel fan-out group followed by (\d+) large solo messages$`, func(n int) error {
		state.input = []provider.Message{
			{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{
					{ID: "bdd-A", Name: "read"},
					{ID: "bdd-B", Name: "read"},
				},
				Content: "calling " + strings.Repeat("p ", 60),
			},
			{Role: "tool", Content: "A " + strings.Repeat("a ", 60), ToolCalls: []provider.ToolCall{{ID: "bdd-A"}}},
			{Role: "tool", Content: "B " + strings.Repeat("b ", 60), ToolCalls: []provider.ToolCall{{ID: "bdd-B"}}},
		}
		for i := range n {
			state.input = append(state.input, provider.Message{
				Role:    "user",
				Content: fmt.Sprintf("solo-%d %s", i, strings.Repeat("s ", 60)),
			})
		}
		state.snapshot = cloneMessages(state.input)
		return nil
	})

	ctx.Step(`^the splitter runs$`, func() error {
		return runSplitter(state, state.hotTailSize)
	})

	ctx.Step(`^the splitter runs with hot tail size (\d+)$`, func(hotTail int) error {
		return runSplitter(state, hotTail)
	})

	ctx.Step(`^no placeholder messages are emitted$`, func() error {
		for _, m := range state.result.HotMessages {
			if strings.HasPrefix(m.Content, "[compacted: ") {
				return fmt.Errorf("unexpected placeholder: %q", m.Content)
			}
		}
		return nil
	})

	ctx.Step(`^at least (\d+) placeholder message is emitted$`, func(n int) error {
		count := 0
		for _, m := range state.result.HotMessages {
			if strings.HasPrefix(m.Content, "[compacted: ") {
				count++
			}
		}
		if count < n {
			return fmt.Errorf("expected at least %d placeholders, got %d", n, count)
		}
		return nil
	})

	ctx.Step(`^the canonical recall view is unchanged$`, func() error {
		if len(state.input) != len(state.snapshot) {
			return fmt.Errorf("len(input)=%d len(snapshot)=%d", len(state.input), len(state.snapshot))
		}
		for i := range state.input {
			if !messagesEqual(state.input[i], state.snapshot[i]) {
				return fmt.Errorf("input[%d] mutated by Split()", i)
			}
		}
		return nil
	})

	ctx.Step(`^the spill directory contains at least (\d+) atomic JSON payload$`, func(n int) error {
		spillDir := filepath.Join(state.storageDir, state.sessionID)
		entries, err := os.ReadDir(spillDir)
		if err != nil {
			return fmt.Errorf("reading spill dir: %w", err)
		}
		jsonCount, tmpCount := 0, 0
		for _, e := range entries {
			switch {
			case strings.HasSuffix(e.Name(), ".json"):
				jsonCount++
			case strings.HasSuffix(e.Name(), ".tmp"):
				tmpCount++
			}
		}
		if jsonCount < n {
			return fmt.Errorf("expected >=%d json payloads, got %d", n, jsonCount)
		}
		if tmpCount != 0 {
			return fmt.Errorf("expected zero .tmp files (atomic rename); found %d", tmpCount)
		}
		return nil
	})

	ctx.Step(`^the resulting window contains no orphan tool-result messages$`, func() error {
		declared := make(map[string]bool)
		for i, m := range state.result.HotMessages {
			if m.Role == "assistant" {
				declared = make(map[string]bool, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					declared[tc.ID] = true
				}
				continue
			}
			if m.Role == "tool" {
				if len(m.ToolCalls) != 1 {
					return fmt.Errorf("tool message %d has %d ids", i, len(m.ToolCalls))
				}
				id := m.ToolCalls[0].ID
				if !declared[id] {
					return fmt.Errorf("orphan tool message %d (id=%s)", i, id)
				}
				delete(declared, id)
			}
		}
		return nil
	})

	ctx.Step(`^every spilled tool-group payload contains every message of its group$`, func() error {
		spillDir := filepath.Join(state.storageDir, state.sessionID)
		entries, err := os.ReadDir(spillDir)
		if err != nil {
			return fmt.Errorf("reading spill dir: %w", err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(spillDir, e.Name()))
			if readErr != nil {
				return readErr
			}
			var payload flowctx.CompactedUnit
			if jsonErr := json.Unmarshal(data, &payload); jsonErr != nil {
				return fmt.Errorf("unmarshal %s: %w", e.Name(), jsonErr)
			}
			if payload.Kind != flowctx.UnitToolGroup {
				continue
			}
			if len(payload.Messages) < 2 {
				return fmt.Errorf("tool-group payload %s has %d messages (expected >=2)", e.Name(), len(payload.Messages))
			}
			if payload.Messages[0].Role != "assistant" {
				return fmt.Errorf("tool-group payload %s does not lead with assistant", e.Name())
			}
			expected := 1 + len(payload.Messages[0].ToolCalls)
			if len(payload.Messages) != expected {
				return fmt.Errorf("tool-group payload %s: %d messages, expected %d (1 assistant + %d results)",
					e.Name(), len(payload.Messages), expected, len(payload.Messages[0].ToolCalls))
			}
		}
		return nil
	})
}

// runSplitter constructs a fresh splitter with the requested hot tail
// size, runs it, drains the persist worker, and stores the result.
//
// Expected:
//   - state is a non-nil compressionState with threshold and storageDir set.
//   - hotTail is the message-level hot tail size the splitter should use.
//
// Returns:
//   - nil on success, or an error when construction or Split() fails.
//
// Side effects:
//   - Spawns and then stops the persist worker goroutine.
//   - Writes payload JSON files under state.storageDir/state.sessionID/.
//   - Replaces state.splitter and state.result.
func runSplitter(state *compressionState, hotTail int) error {
	if state.splitter != nil {
		state.splitter.Stop()
		state.splitter = nil
	}
	compactor := flowctx.NewDefaultMessageCompactor(state.threshold)
	state.splitter = flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
		Compactor:   compactor,
		HotTailSize: hotTail,
		StorageDir:  state.storageDir,
		SessionID:   state.sessionID,
	})
	if state.splitter == nil {
		return errors.New("splitter construction failed")
	}
	state.splitter.StartPersistWorker(context.Background())
	state.result = state.splitter.Split(state.input)
	state.splitter.Stop()
	state.splitter = nil
	return nil
}

// cloneMessages returns an element-copy of in. Used to snapshot inputs
// before Split() so the view-only invariant can be asserted afterwards.
//
// Expected:
//   - in may be nil or any slice of provider.Message.
//
// Returns:
//   - A new slice of length len(in), whose elements are copied from in.
//
// Side effects:
//   - None.
func cloneMessages(in []provider.Message) []provider.Message {
	out := make([]provider.Message, len(in))
	copy(out, in)
	return out
}

// messagesEqual compares two provider.Message values by the fields the
// view-only invariant cares about (Role, Content, Thinking, ModelID, and
// each tool call's ID and Name). Arguments are not byte-compared because
// the canonical transcript never round-trips through JSON here.
//
// Expected:
//   - a and b are the messages to compare.
//
// Returns:
//   - true when every compared field is identical.
//   - false otherwise.
//
// Side effects:
//   - None.
func messagesEqual(a, b provider.Message) bool {
	if a.Role != b.Role || a.Content != b.Content || a.Thinking != b.Thinking || a.ModelID != b.ModelID {
		return false
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if a.ToolCalls[i].ID != b.ToolCalls[i].ID || a.ToolCalls[i].Name != b.ToolCalls[i].Name {
			return false
		}
	}
	return true
}

// silence unused testing import (used only when running -v locally).
var _ = testing.T{}
