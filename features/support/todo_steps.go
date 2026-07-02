//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"fmt"

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
	lastErr   error
}

// RegisterTodoSteps wires the todo_monotonic_clear feature steps onto the
// godog scenario context. Steps that depend on behaviour not yet
// implemented return godog.ErrPending until the corresponding GREEN step
// lands, which keeps the suite compiling under the e2e build tag while
// marking the scenarios as failing (RED) under Strict mode.
func RegisterTodoSteps(ctx *godog.ScenarioContext) {
	s := &todoSteps{}
	ctx.Step(`^the todo tools are enabled$`, s.todoToolsAreEnabled)
	ctx.Step(`^a session has a todo list with items:$`, s.sessionHasTodoList)
	ctx.Step(`^the agent clears the todo list$`, s.agentClearsTodoList)
	ctx.Step(`^the agent updates todo at index (\d+) to status "([^"]*)"$`, s.agentUpdatesTodoStatus)
	ctx.Step(`^the stored todo list should be empty$`, s.storedTodoListShouldBeEmpty)
	ctx.Step(`^a fresh todowrite should create a new list$`, s.aFreshTodowriteShouldCreateANewList)
	ctx.Step(`^the update should be rejected with "([^"]*)"$`, s.theUpdateShouldBeRejectedWith)
	ctx.Step(`^the todo list should be unchanged$`, s.theTodoListShouldBeUnchanged)
	ctx.Step(`^todo at index (\d+) should have status "([^"]*)"$`, s.todoAtIndexShouldHaveStatus)
}

// todoToolsAreEnabled is the Background step. It resets per-scenario state
// so the seeded list and captured error never leak between scenarios.
func (s *todoSteps) todoToolsAreEnabled() error {
	s.store = nil
	s.writeTool = nil
	s.sessionID = ""
	s.lastErr = nil
	return nil
}

// sessionHasTodoList seeds a fresh per-session store with the items in the
// Gherkin data table, using the real todowrite tool so the seed runs
// through the same creation path as production.
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
	return err
}

func (s *todoSteps) agentClearsTodoList() error {
	clearTool := todotool.NewClear(s.store)
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessionID)
	_, err := clearTool.Execute(ctx, tool.Input{
		Name:      "todo_clear",
		Arguments: map[string]interface{}{},
	})
	s.lastErr = err
	return err
}

func (s *todoSteps) agentUpdatesTodoStatus(idx int, status string) error {
	return godog.ErrPending
}

func (s *todoSteps) storedTodoListShouldBeEmpty() error {
	if got := s.store.Get(s.sessionID); len(got) != 0 {
		return fmt.Errorf("expected an empty todo list, got %d items", len(got))
	}
	return nil
}

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

func (s *todoSteps) theUpdateShouldBeRejectedWith(substr string) error {
	return godog.ErrPending
}

func (s *todoSteps) theTodoListShouldBeUnchanged() error {
	return godog.ErrPending
}

func (s *todoSteps) todoAtIndexShouldHaveStatus(idx int, status string) error {
	return godog.ErrPending
}
