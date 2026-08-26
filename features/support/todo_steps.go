//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
)

// todoSteps holds per-scenario state for the todo monotonic-state and
// list-clearing feature. State is reset in the Background step so every
// scenario starts from an empty store and a clean error/result slot.
type todoSteps struct {
	store     *todotool.MemoryStore
	writeTool tool.Tool
	sessionID string
	seed      []todotool.Item
	lastErr   error
}

// RegisterTodoSteps wires the todo_monotonic_clear feature steps onto the
// godog scenario context. Steps that depend on behaviour not yet
// implemented return godog.ErrPending until the corresponding GREEN step
// lands, which keeps the suite compiling under the e2e build tag while
// marking the scenarios as failing (RED) under Strict mode.
//
// Expected: parameters for RegisterTodoSteps.
// Side effects: None.
func RegisterTodoSteps(ctx *godog.ScenarioContext) {
	s := &todoSteps{}
	ctx.Step(`^the todo tools are enabled$`, s.todoToolsAreEnabled)
	ctx.Step(`^a session has a todo list with items:$`, s.sessionHasTodoList)
	ctx.Step(`^the agent clears the todo list$`, s.agentClearsTodoList)
	ctx.Step(`^the agent updates todo at index (\d+) to status "([^"]*)"$`, s.agentUpdatesTodoStatus)
	ctx.Step(`^the stored todo list should be empty$`, s.storedTodoListShouldBeEmpty)
	ctx.Step(`^a fresh todowrite should create a new list$`, s.aFreshTodowriteShouldCreateANewList)
	ctx.Step(`^the update should be rejected with "([^"]*)"$`, s.theUpdateShouldBeRejectedWith)
	ctx.Step(`^the clear should be rejected$`, s.theClearShouldBeRejected)
	ctx.Step(`^the todo list should be unchanged$`, s.theTodoListShouldBeUnchanged)
	ctx.Step(`^todo at index (\d+) should have status "([^"]*)"$`, s.todoAtIndexShouldHaveStatus)
}

// todoToolsAreEnabled is the Background step. It resets per-scenario state
// so the seeded list and captured error never leak between scenarios.
//
// Returns: result of todoToolsAreEnabled.
// Side effects: None.
//
// Expected: parameters for todoToolsAreEnabled.
func (s *todoSteps) todoToolsAreEnabled() error {
	s.store = nil
	s.writeTool = nil
	s.sessionID = ""
	s.seed = nil
	s.lastErr = nil
	return nil
}

// sessionHasTodoList seeds a fresh per-session store with the items in the
// Gherkin data table, using the real todowrite tool so the seed runs
// through the same creation path as production.
//
// Expected: parameters for sessionHasTodoList.
// Returns: result of sessionHasTodoList.
// Side effects: None.
func (s *todoSteps) sessionHasTodoList(table *godog.Table) error {
	s.sessionID = "bdd-todo-session"
	s.store = todotool.NewMemoryStore()
	s.writeTool = todotool.New(s.store)

	items := make([]interface{}, 0, len(table.Rows)-1)
	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		items = append(items, map[string]interface{}{
			"content":  row.Cells[0].Value,
			"status":   row.Cells[1].Value,
			"priority": row.Cells[2].Value,
		})
	}

	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessionID)
	_, err := s.writeTool.Execute(ctx, tool.Input{
		Name:      "todowrite",
		Arguments: map[string]interface{}{"todos": items},
	})
	if err != nil {
		return err
	}
	s.seed = s.store.Get(s.sessionID)
	return nil
}

// agentClearsTodoList ...
//
// Returns: result of agentClearsTodoList.
//
// Side effects: None.
//
// Expected: parameters for agentClearsTodoList.
func (s *todoSteps) agentClearsTodoList() error {
	clearTool := todotool.NewClear(s.store)
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessionID)
	_, err := clearTool.Execute(ctx, tool.Input{
		Name:      "todo_clear",
		Arguments: map[string]interface{}{},
	})
	s.lastErr = err
	return nil
}

// agentUpdatesTodoStatus ...
//
// Expected: parameters for agentUpdatesTodoStatus.
//
// Returns: result of agentUpdatesTodoStatus.
//
// Side effects: None.
func (s *todoSteps) agentUpdatesTodoStatus(idx int, status string) error {
	updateTool := todotool.NewUpdate(s.store)
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessionID)
	_, err := updateTool.Execute(ctx, tool.Input{
		Name: "todo_update",
		Arguments: map[string]interface{}{
			"index":  float64(idx),
			"status": status,
		},
	})
	s.lastErr = err
	return nil
}

// storedTodoListShouldBeEmpty ...
//
// Returns: result of storedTodoListShouldBeEmpty.
//
// Side effects: None.
//
// Expected: parameters for storedTodoListShouldBeEmpty.
func (s *todoSteps) storedTodoListShouldBeEmpty() error {
	if got := s.store.Get(s.sessionID); len(got) != 0 {
		return fmt.Errorf("expected an empty todo list, got %d items", len(got))
	}
	return nil
}

// aFreshTodowriteShouldCreateANewList ...
//
// Returns: result of aFreshTodowriteShouldCreateANewList.
//
// Side effects: None.
//
// Expected: parameters for aFreshTodowriteShouldCreateANewList.
func (s *todoSteps) aFreshTodowriteShouldCreateANewList() error {
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessionID)
	_, err := s.writeTool.Execute(ctx, tool.Input{
		Name: "todowrite",
		Arguments: map[string]interface{}{
			"todos": []interface{}{
				map[string]interface{}{
					"content":  "fresh task",
					"status":   "pending",
					"priority": "medium",
				},
			},
		},
	})
	s.lastErr = err
	return err
}

// theUpdateShouldBeRejectedWith ...
//
// Expected: parameters for theUpdateShouldBeRejectedWith.
//
// Returns: result of theUpdateShouldBeRejectedWith.
//
// Side effects: None.
func (s *todoSteps) theUpdateShouldBeRejectedWith(substr string) error {
	if s.lastErr == nil {
		return fmt.Errorf("expected the todo update to be rejected, but it succeeded")
	}
	if !strings.Contains(s.lastErr.Error(), substr) {
		return fmt.Errorf("expected rejection message to contain %q, got %q", substr, s.lastErr.Error())
	}
	return nil
}

// theClearShouldBeRejected ...
//
// Returns: result of theClearShouldBeRejected.
//
// Side effects: None.
//
// Expected: parameters for theClearShouldBeRejected.
func (s *todoSteps) theClearShouldBeRejected() error {
	if s.lastErr == nil {
		return fmt.Errorf("expected the todo clear to be rejected, but it succeeded")
	}
	return nil
}

// theTodoListShouldBeUnchanged ...
//
// Returns: result of theTodoListShouldBeUnchanged.
//
// Side effects: None.
//
// Expected: parameters for theTodoListShouldBeUnchanged.
func (s *todoSteps) theTodoListShouldBeUnchanged() error {
	if got := s.store.Get(s.sessionID); !reflect.DeepEqual(got, s.seed) {
		return fmt.Errorf("expected the todo list to be unchanged from the seed, got %v", got)
	}
	return nil
}

// todoAtIndexShouldHaveStatus ...
//
// Expected: parameters for todoAtIndexShouldHaveStatus.
//
// Returns: result of todoAtIndexShouldHaveStatus.
//
// Side effects: None.
func (s *todoSteps) todoAtIndexShouldHaveStatus(idx int, status string) error {
	items := s.store.Get(s.sessionID)
	if idx < 0 || idx >= len(items) {
		return fmt.Errorf("index %d out of range: list has %d items", idx, len(items))
	}
	if items[idx].Status != status {
		return fmt.Errorf("expected todo at index %d to have status %q, got %q", idx, status, items[idx].Status)
	}
	return nil
}
