package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/memory"
)

// MemoryStepDefinitions holds state for memory server BDD step definitions.
type MemoryStepDefinitions struct {
	graph      *memory.Graph
	store      *memory.JSONLStore
	tmpDir     string
	storePath  string
	lastErr    error
	lastSearch []memory.Entity
	lastEntity []memory.Entity
}

// RegisterMemorySteps registers memory-specific step definitions.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//
// Side effects:
//   - Registers memory step definitions with the godog context.
//   - Cleans up temporary directories after each scenario.
func RegisterMemorySteps(ctx *godog.ScenarioContext) {
	m := &MemoryStepDefinitions{}

	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		m.graph = nil
		m.store = nil
		m.lastErr = nil
		m.lastSearch = nil
		m.lastEntity = nil
		return bctx, nil
	})

	ctx.After(func(bctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if m.tmpDir != "" {
			os.RemoveAll(m.tmpDir)
			m.tmpDir = ""
		}
		return bctx, nil
	})

	ctx.Step(`^the memory server is running$`, m.theMemoryServerIsRunning)
	ctx.Step(`^I create an entity named "([^"]*)" with description "([^"]*)"$`, m.iCreateAnEntityNamedWithDescription)
	ctx.Step(`^I should be able to retrieve the entity "([^"]*)"$`, m.iShouldBeAbleToRetrieveTheEntity)
	ctx.Step(`^the entity details should include "([^"]*)"$`, m.theEntityDetailsShouldInclude)
	ctx.Step(`^the memory server contains entities "([^"]*)", "([^"]*)", and "([^"]*)"$`, m.theMemoryServerContainsEntitiesAnd)
	ctx.Step(`^I search for entities with the query "([^"]*)"$`, m.iSearchForEntitiesWithTheQuery)
	ctx.Step(`^I should see "([^"]*)" in the search results$`, m.iShouldSeeInTheSearchResults)
	ctx.Step(`^I should not see "([^"]*)" or "([^"]*)" in the search results$`, m.iShouldNotSeeOrInTheSearchResults)
	ctx.Step(`^the entity "([^"]*)" exists in the memory server$`, m.theEntityExistsInTheMemoryServer)
	ctx.Step(`^I add the observation "([^"]*)" to "([^"]*)"$`, m.iAddTheObservationTo)
	ctx.Step(`^the entity "([^"]*)" should include the observation "([^"]*)"$`, m.theEntityShouldIncludeTheObservation)
	ctx.Step(`^the entity "([^"]*)" exists and is related to "([^"]*)"$`, m.theEntityExistsAndIsRelatedTo)
	ctx.Step(`^I delete the entity "([^"]*)"$`, m.iDeleteTheEntity)
	ctx.Step(`^the entity "([^"]*)" should no longer exist in the memory server$`, m.theEntityShouldNoLongerExistInTheMemoryServer)
	ctx.Step(`^any relations involving "([^"]*)" should be removed$`, m.anyRelationsInvolvingShouldBeRemoved)
	ctx.Step(`^I attempt to retrieve the entity "([^"]*)"$`, m.iAttemptToRetrieveTheEntity)
	ctx.Step(`^I should receive a not found error message$`, m.iShouldReceiveANotFoundErrorMessage)
	ctx.Step(`^I restart the memory server$`, m.iRestartTheMemoryServer)
	ctx.Step(`^the entity "([^"]*)" should still exist after restart$`, m.theEntityShouldStillExistAfterRestart)
}

func (m *MemoryStepDefinitions) ensureTmpDir() error {
	if m.tmpDir != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "memory-bdd-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	m.tmpDir = dir
	m.storePath = filepath.Join(dir, "test.jsonl")
	return nil
}

func (m *MemoryStepDefinitions) ensureGraphAndStore() error {
	if m.graph != nil {
		return nil
	}
	if err := m.ensureTmpDir(); err != nil {
		return err
	}
	m.graph = memory.NewGraph()
	m.store = memory.NewJSONLStore(m.storePath)
	return nil
}

// theMemoryServerIsRunning initialises an in-memory graph and JSONL store.
//
// Returns:
//   - nil on success, error otherwise.
//
// Side effects:
//   - Sets m.graph and m.store.
func (m *MemoryStepDefinitions) theMemoryServerIsRunning() error {
	return m.ensureGraphAndStore()
}

// iCreateAnEntityNamedWithDescription creates an entity in the graph.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Adds entity to m.graph.
func (m *MemoryStepDefinitions) iCreateAnEntityNamedWithDescription(name, description string) error {
	if m.graph == nil {
		return errors.New("graph not initialized")
	}
	m.graph.CreateEntities([]memory.Entity{
		{Name: name, EntityType: "general", Observations: []string{description}},
	})
	return nil
}

// iShouldBeAbleToRetrieveTheEntity retrieves an entity by name and verifies it exists.
//
// Returns:
//   - nil if found, error otherwise.
//
// Side effects:
//   - Sets m.lastEntity.
func (m *MemoryStepDefinitions) iShouldBeAbleToRetrieveTheEntity(name string) error {
	entities, _ := m.graph.OpenNodes([]string{name})
	if len(entities) == 0 {
		return fmt.Errorf("entity %q not found in graph", name)
	}
	m.lastEntity = entities
	return nil
}

// theEntityDetailsShouldInclude verifies the retrieved entity contains the expected detail.
//
// Returns:
//   - nil if detail found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) theEntityDetailsShouldInclude(detail string) error {
	for _, e := range m.lastEntity {
		for _, obs := range e.Observations {
			if strings.Contains(obs, detail) {
				return nil
			}
		}
	}
	return fmt.Errorf("expected detail %q not found in entity observations", detail)
}

// theMemoryServerContainsEntitiesAnd creates three named entities in the graph.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises graph and adds three entities.
func (m *MemoryStepDefinitions) theMemoryServerContainsEntitiesAnd(name1, name2, name3 string) error {
	if err := m.ensureGraphAndStore(); err != nil {
		return err
	}
	m.graph.CreateEntities([]memory.Entity{
		{Name: name1, EntityType: "concept", Observations: []string{}},
		{Name: name2, EntityType: "concept", Observations: []string{}},
		{Name: name3, EntityType: "concept", Observations: []string{}},
	})
	return nil
}

// iSearchForEntitiesWithTheQuery searches the graph by query.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets m.lastSearch.
func (m *MemoryStepDefinitions) iSearchForEntitiesWithTheQuery(query string) error {
	if m.graph == nil {
		return errors.New("graph not initialized")
	}
	m.lastSearch = m.graph.SearchNodes(query)
	return nil
}

// iShouldSeeInTheSearchResults verifies the search results contain the named entity.
//
// Returns:
//   - nil if found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) iShouldSeeInTheSearchResults(name string) error {
	for _, e := range m.lastSearch {
		if e.Name == name {
			return nil
		}
	}
	return fmt.Errorf("expected %q in search results, but not found", name)
}

// iShouldNotSeeOrInTheSearchResults verifies the search results exclude two named entities.
//
// Returns:
//   - nil if neither found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) iShouldNotSeeOrInTheSearchResults(name1, name2 string) error {
	for _, e := range m.lastSearch {
		if e.Name == name1 || e.Name == name2 {
			return fmt.Errorf("expected %q and %q to be absent from search results, but found %q", name1, name2, e.Name)
		}
	}
	return nil
}

// theEntityExistsInTheMemoryServer creates a named entity in the graph.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises graph and adds the entity.
func (m *MemoryStepDefinitions) theEntityExistsInTheMemoryServer(name string) error {
	if err := m.ensureGraphAndStore(); err != nil {
		return err
	}
	m.graph.CreateEntities([]memory.Entity{
		{Name: name, EntityType: "general", Observations: []string{}},
	})
	return nil
}

// iAddTheObservationTo adds an observation to an existing entity.
//
// Returns:
//   - nil on success, error if entity not found.
//
// Side effects:
//   - Modifies entity observations in m.graph.
func (m *MemoryStepDefinitions) iAddTheObservationTo(observation, entityName string) error {
	if m.graph == nil {
		return errors.New("graph not initialized")
	}
	return m.graph.AddObservations(entityName, []string{observation})
}

// theEntityShouldIncludeTheObservation verifies the entity contains the expected observation.
//
// Returns:
//   - nil if found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) theEntityShouldIncludeTheObservation(entityName, observation string) error {
	entities, _ := m.graph.OpenNodes([]string{entityName})
	if len(entities) == 0 {
		return fmt.Errorf("entity %q not found", entityName)
	}
	for _, obs := range entities[0].Observations {
		if obs == observation {
			return nil
		}
	}
	return fmt.Errorf("observation %q not found on entity %q", observation, entityName)
}

// theEntityExistsAndIsRelatedTo creates two entities and a relation between them.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Initialises graph and adds entities and relation.
func (m *MemoryStepDefinitions) theEntityExistsAndIsRelatedTo(entity1, entity2 string) error {
	if err := m.ensureGraphAndStore(); err != nil {
		return err
	}
	m.graph.CreateEntities([]memory.Entity{
		{Name: entity1, EntityType: "general", Observations: []string{}},
		{Name: entity2, EntityType: "general", Observations: []string{}},
	})
	m.graph.CreateRelations([]memory.Relation{
		{From: entity1, To: entity2, RelationType: "related_to"},
	})
	return nil
}

// iDeleteTheEntity deletes an entity from the graph.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Removes entity and cascading relations from m.graph.
func (m *MemoryStepDefinitions) iDeleteTheEntity(name string) error {
	if m.graph == nil {
		return errors.New("graph not initialized")
	}
	m.graph.DeleteEntities([]string{name})
	return nil
}

// theEntityShouldNoLongerExistInTheMemoryServer verifies the entity was deleted.
//
// Returns:
//   - nil if absent, error if still exists.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) theEntityShouldNoLongerExistInTheMemoryServer(name string) error {
	entities, _ := m.graph.OpenNodes([]string{name})
	if len(entities) > 0 {
		return fmt.Errorf("entity %q should not exist, but was found", name)
	}
	return nil
}

// anyRelationsInvolvingShouldBeRemoved verifies no relations reference the deleted entity.
//
// Returns:
//   - nil if no relations found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) anyRelationsInvolvingShouldBeRemoved(name string) error {
	kg := m.graph.ReadGraph()
	for _, r := range kg.Relations {
		if r.From == name || r.To == name {
			return fmt.Errorf("relation %s -> %s still exists involving %q", r.From, r.To, name)
		}
	}
	return nil
}

// iAttemptToRetrieveTheEntity tries to find an entity and stores the error state.
//
// Returns:
//   - nil (always; error is stored in m.lastErr).
//
// Side effects:
//   - Sets m.lastErr.
func (m *MemoryStepDefinitions) iAttemptToRetrieveTheEntity(name string) error {
	if m.graph == nil {
		return errors.New("graph not initialized")
	}
	entities, _ := m.graph.OpenNodes([]string{name})
	if len(entities) == 0 {
		m.lastErr = errors.New("entity not found")
	} else {
		m.lastErr = nil
	}
	return nil
}

// iShouldReceiveANotFoundErrorMessage verifies a not-found error was recorded.
//
// Returns:
//   - nil if error present, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) iShouldReceiveANotFoundErrorMessage() error {
	if m.lastErr == nil {
		return errors.New("expected a not found error, but got nil")
	}
	if !strings.Contains(m.lastErr.Error(), "not found") {
		return fmt.Errorf("expected 'not found' in error, got: %q", m.lastErr.Error())
	}
	return nil
}

// iRestartTheMemoryServer persists graph to disk then reloads from a fresh store.
//
// Returns:
//   - nil on success, error otherwise.
//
// Side effects:
//   - Saves graph to JSONL, creates a new graph, and loads data from disk.
func (m *MemoryStepDefinitions) iRestartTheMemoryServer() error {
	if m.graph == nil || m.store == nil {
		return errors.New("memory server not running")
	}
	kg := m.graph.ReadGraph()
	if err := m.store.Save(&kg); err != nil {
		return fmt.Errorf("saving graph before restart: %w", err)
	}

	m.graph = memory.NewGraph()
	m.store = memory.NewJSONLStore(m.storePath)

	loaded, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("loading graph after restart: %w", err)
	}

	m.graph.CreateEntities(loaded.Entities)
	m.graph.CreateRelations(loaded.Relations)
	return nil
}

// theEntityShouldStillExistAfterRestart verifies entity survived the restart.
//
// Returns:
//   - nil if found, error otherwise.
//
// Side effects:
//   - None.
func (m *MemoryStepDefinitions) theEntityShouldStillExistAfterRestart(name string) error {
	entities, _ := m.graph.OpenNodes([]string{name})
	if len(entities) == 0 {
		return fmt.Errorf("entity %q not found after restart", name)
	}
	return nil
}
