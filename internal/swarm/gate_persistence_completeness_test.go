package swarm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/coordination"
)

// pcStore is a minimal in-memory coordination.Store for the
// persistence-completeness unit tests.
type pcStore struct {
	entries map[string][]byte
}

func newPCStore() *pcStore { return &pcStore{entries: map[string][]byte{}} }

func (s *pcStore) Get(key string) ([]byte, error) {
	v, ok := s.entries[key]
	if !ok {
		return nil, coordination.ErrKeyNotFound
	}
	return v, nil
}

func (s *pcStore) Set(key string, value []byte) error {
	s.entries[key] = []byte(string(value))
	return nil
}

func (s *pcStore) List(prefix string) ([]string, error) { return nil, nil }

func (s *pcStore) Delete(key string) error { delete(s.entries, key); return nil }

func (s *pcStore) Increment(key string) (int, error) { return 0, nil }

func (s *pcStore) Exists(key string) (bool, error) { _, ok := s.entries[key]; return ok, nil }

func pcGate() GateSpec {
	return GateSpec{
		Name: "pre-synthesis-persistence",
		Kind: PersistenceCompletenessGateKind,
		When: "post",
		Policy: map[string]any{
			"required_members":     []string{"tech-lead", "security-engineer"},
			"output_key":           "output",
			"mediation_reason_key": "persisted-by-coordinator",
		},
	}
}

func pcArgs(store coordination.Store) GateArgs {
	return GateArgs{SwarmID: "s", ChainPrefix: "dd", MemberID: "lead", CoordStore: store}
}

func TestPersistenceCompletenessPassesWhenAllMembersPersisted(t *testing.T) {
	store := newPCStore()
	_ = store.Set("dd/tech-lead/output", []byte(`{"verdict":"ok"}`))
	_ = store.Set("dd/security-engineer/output", []byte(`{"findings":[]}`))
	if err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), pcArgs(store)); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

func TestPersistenceCompletenessFailsOnMissingKey(t *testing.T) {
	store := newPCStore()
	_ = store.Set("dd/tech-lead/output", []byte(`{}`))
	err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), pcArgs(store))
	if err == nil {
		t.Fatal("expected failure when security-engineer key absent")
	}
	if !strings.Contains(err.Error(), "security-engineer") || !strings.Contains(err.Error(), "dd/security-engineer/output") {
		t.Fatalf("failure should name member and key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("failure should aggregate counts, got: %v", err)
	}
}

func TestPersistenceCompletenessFailsOnWhitespaceOnlyPayload(t *testing.T) {
	store := newPCStore()
	_ = store.Set("dd/tech-lead/output", []byte("  \n\t "))
	_ = store.Set("dd/security-engineer/output", []byte(`{}`))
	err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), pcArgs(store))
	if err == nil {
		t.Fatal("expected failure for whitespace-only payload")
	}
	if !strings.Contains(err.Error(), "tech-lead") {
		t.Fatalf("failure should name whitespace-only member, got: %v", err)
	}
}

func TestPersistenceCompletenessMediationRequiresNonEmptyReason(t *testing.T) {
	store := newPCStore()
	_ = store.Set("dd/tech-lead/output", []byte(`{}`))
	// Security-engineer absent but covered by coordinator annotation.
	ann, _ := json.Marshal(map[string]any{"reason": "session lacked coordination_store tool; coordinator persisted output verbatim", "by": "coordinator"})
	_ = store.Set("dd/coordinator/persisted-by-coordinator/security-engineer", ann)
	if err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), pcArgs(store)); err != nil {
		t.Fatalf("mediated member should pass, got: %v", err)
	}

	// Empty reason must NOT cover the member.
	empty, _ := json.Marshal(map[string]any{"reason": "   "})
	_ = store.Set("dd/coordinator/persisted-by-coordinator/security-engineer", empty)
	err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), pcArgs(store))
	if err == nil {
		t.Fatal("empty-reason annotation must fail")
	}
	if !strings.Contains(err.Error(), "empty reason") {
		t.Fatalf("failure should mention empty reason, got: %v", err)
	}
}

func TestPersistenceCompletenessFailsClosedWithoutCoordStore(t *testing.T) {
	err := NewPersistenceCompletenessRunner().Run(context.Background(), pcGate(), GateArgs{SwarmID: "s"})
	if err == nil {
		t.Fatal("nil coord store must fail closed")
	}
	if !strings.Contains(err.Error(), "coordination store unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPersistenceCompletenessRejectsEmptyRequiredMembers(t *testing.T) {
	gate := pcGate()
	gate.Policy = map[string]any{"required_members": []string{}}
	err := NewPersistenceCompletenessRunner().Run(context.Background(), gate, pcArgs(newPCStore()))
	if err == nil {
		t.Fatal("empty required_members is a manifest bug and must fail")
	}
	if !strings.Contains(err.Error(), "required_members") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePersistenceKeyMemberPlaceholder(t *testing.T) {
	policy := PersistenceCompletenessPolicy{OutputKey: "{member}/output"}
	got := resolvePersistenceKey(policy, GateArgs{ChainPrefix: "dd"}, "tech-lead")
	if got != "dd/tech-lead/output" {
		t.Fatalf("unexpected key: %q", got)
	}
}

func TestCountNonWhitespace(t *testing.T) {
	if got := countNonWhitespace([]byte(" a\tb\n ")); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	if got := countNonWhitespace(nil); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}
