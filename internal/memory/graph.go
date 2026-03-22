package memory

import "strings"

// Graph holds a collection of entities and their relations.
//
// Expected:
//   - Initialised via NewGraph.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None (methods have individual side effects).
type Graph struct {
	entities  []Entity
	relations []Relation
}

// NewGraph creates an empty knowledge graph.
//
// Expected:
//   - No preconditions.
//
// Returns:
//   - *Graph with empty entity and relation slices.
//
// Side effects:
//   - None.
func NewGraph() *Graph {
	return &Graph{}
}

// CreateEntities adds new entities to the graph, avoiding duplicates by name.
//
// Entities whose Name already exists in the graph are silently skipped.
//
// Expected:
//   - newEntities contains valid Entity values.
//
// Returns:
//   - []Entity containing only the entities that were actually added.
//
// Side effects:
//   - Appends non-duplicate entities to g.entities.
func (g *Graph) CreateEntities(newEntities []Entity) []Entity {
	added := []Entity{}
	for _, ne := range newEntities {
		if !g.entityExists(ne.Name) {
			g.entities = append(g.entities, ne)
			added = append(added, ne)
		}
	}
	return added
}

// CreateRelations adds new relations to the graph, avoiding duplicates and requiring valid entities.
//
// Relations referencing non-existent entities are silently skipped.
// Duplicate relations (matching From, To, and RelationType) are also skipped.
//
// Expected:
//   - Referenced entities exist in the graph.
//
// Returns:
//   - []Relation containing only the relations that were actually added.
//
// Side effects:
//   - Appends non-duplicate relations to g.relations.
func (g *Graph) CreateRelations(newRelations []Relation) []Relation {
	added := []Relation{}
	for _, nr := range newRelations {
		if !g.entityExists(nr.From) || !g.entityExists(nr.To) {
			continue
		}
		if !g.relationExists(nr) {
			g.relations = append(g.relations, nr)
			added = append(added, nr)
		}
	}
	return added
}

// AddObservations appends unique observations to an entity by name.
//
// Duplicate observations already present on the entity are skipped.
//
// Expected:
//   - An entity with the given name exists in the graph.
//
// Returns:
//   - nil on success.
//   - ErrEntityNotFound if no entity matches entityName.
//
// Side effects:
//   - Appends new observations to the matching entity.
func (g *Graph) AddObservations(entityName string, observations []string) error {
	for i, e := range g.entities {
		if e.Name == entityName {
			g.entities[i].Observations = appendUniqueStrings(e.Observations, observations)
			return nil
		}
	}
	return ErrEntityNotFound
}

// DeleteEntities removes entities by name and cascades deletion to associated relations.
//
// Any relation where From or To matches a deleted entity name is also removed.
//
// Expected:
//   - names contains entity names to delete.
//
// Side effects:
//   - Removes matching entities from g.entities.
//   - Removes relations involving deleted entities from g.relations.
func (g *Graph) DeleteEntities(names []string) {
	nameSet := toStringSet(names)
	filtered := g.entities[:0]
	for _, e := range g.entities {
		if !nameSet[e.Name] {
			filtered = append(filtered, e)
		}
	}
	g.entities = filtered
	g.removeRelationsInvolving(nameSet)
}

// DeleteObservations removes specific observations from an entity.
//
// Observations not found on the entity are silently ignored.
//
// Expected:
//   - An entity with the given name exists in the graph.
//
// Returns:
//   - nil on success.
//   - ErrEntityNotFound if no entity matches entityName.
//
// Side effects:
//   - Filters out matching observations from the entity.
func (g *Graph) DeleteObservations(entityName string, observations []string) error {
	for i, e := range g.entities {
		if e.Name == entityName {
			g.entities[i].Observations = filterOutStrings(e.Observations, observations)
			return nil
		}
	}
	return ErrEntityNotFound
}

// DeleteRelations removes relations matching all three fields (From, To, RelationType).
//
// Relations not found in the graph are silently ignored.
//
// Expected:
//   - relations contains Relation values to remove.
//
// Side effects:
//   - Removes matching relations from g.relations.
func (g *Graph) DeleteRelations(relations []Relation) {
	for _, target := range relations {
		g.removeSingleRelation(target)
	}
}

// ReadGraph returns the complete knowledge graph containing all entities and relations.
//
// Returns:
//   - KnowledgeGraph with current entities and relations.
//
// Side effects:
//   - None.
func (g *Graph) ReadGraph() KnowledgeGraph {
	return KnowledgeGraph{Entities: g.entities, Relations: g.relations}
}

// SearchNodes finds entities by case-insensitive substring match across name, type, and observations.
//
// An entity matches if the query appears as a substring in its Name, EntityType,
// or any of its Observations.
//
// Expected:
//   - query is a non-empty search string.
//
// Returns:
//   - []Entity containing all matching entities.
//
// Side effects:
//   - None.
func (g *Graph) SearchNodes(query string) []Entity {
	lower := strings.ToLower(query)
	results := []Entity{}
	for _, e := range g.entities {
		if g.entityMatchesQuery(e, lower) {
			results = append(results, e)
		}
	}
	return results
}

// OpenNodes returns requested entities and relations where both endpoints are in the requested set.
//
// Only relations where both From and To are in the requested names are included.
//
// Expected:
//   - names contains entity names to retrieve.
//
// Returns:
//   - []Entity matching the requested names.
//   - []Relation where both From and To are in the requested set.
//
// Side effects:
//   - None.
func (g *Graph) OpenNodes(names []string) ([]Entity, []Relation) {
	nameSet := toStringSet(names)
	entities := []Entity{}
	for _, e := range g.entities {
		if nameSet[e.Name] {
			entities = append(entities, e)
		}
	}
	relations := []Relation{}
	for _, r := range g.relations {
		if nameSet[r.From] && nameSet[r.To] {
			relations = append(relations, r)
		}
	}
	return entities, relations
}

// entityExists checks whether an entity with the given name exists in the graph.
//
// Expected:
//   - name is a non-empty string.
//
// Returns:
//   - true if an entity with the given name exists.
//
// Side effects:
//   - None.
func (g *Graph) entityExists(name string) bool {
	for _, e := range g.entities {
		if e.Name == name {
			return true
		}
	}
	return false
}

// relationExists checks whether a relation matching all three fields exists in the graph.
//
// Expected:
//   - r contains valid From, To, and RelationType fields.
//
// Returns:
//   - true if a matching relation exists.
//
// Side effects:
//   - None.
func (g *Graph) relationExists(r Relation) bool {
	for _, rel := range g.relations {
		if rel.From == r.From && rel.To == r.To && rel.RelationType == r.RelationType {
			return true
		}
	}
	return false
}

// removeRelationsInvolving filters out relations where From or To is in the name set.
//
// Expected:
//   - nameSet contains entity names to match against.
//
// Side effects:
//   - Removes matching relations from g.relations.
func (g *Graph) removeRelationsInvolving(nameSet map[string]bool) {
	filtered := g.relations[:0]
	for _, r := range g.relations {
		if !nameSet[r.From] && !nameSet[r.To] {
			filtered = append(filtered, r)
		}
	}
	g.relations = filtered
}

// removeSingleRelation removes the first relation matching all three fields of target.
//
// Expected:
//   - target contains valid From, To, and RelationType fields.
//
// Side effects:
//   - Removes the first matching relation from g.relations.
func (g *Graph) removeSingleRelation(target Relation) {
	for i, r := range g.relations {
		if r.From == target.From && r.To == target.To && r.RelationType == target.RelationType {
			g.relations = append(g.relations[:i], g.relations[i+1:]...)
			return
		}
	}
}

// entityMatchesQuery checks if any field of the entity contains the lowercase query substring.
//
// Expected:
//   - lowerQuery is already lowercased.
//
// Returns:
//   - true if the query matches any entity field.
//
// Side effects:
//   - None.
func (g *Graph) entityMatchesQuery(e Entity, lowerQuery string) bool {
	if strings.Contains(strings.ToLower(e.Name), lowerQuery) {
		return true
	}
	if strings.Contains(strings.ToLower(e.EntityType), lowerQuery) {
		return true
	}
	for _, obs := range e.Observations {
		if strings.Contains(strings.ToLower(obs), lowerQuery) {
			return true
		}
	}
	return false
}

// appendUniqueStrings appends items from additions to existing, skipping duplicates.
//
// Expected:
//   - existing and additions are valid string slices.
//
// Returns:
//   - The existing slice with unique additions appended.
//
// Side effects:
//   - None (returns a new or extended slice).
func appendUniqueStrings(existing, additions []string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, s := range existing {
		seen[s] = struct{}{}
	}
	for _, s := range additions {
		if _, exists := seen[s]; !exists {
			existing = append(existing, s)
			seen[s] = struct{}{}
		}
	}
	return existing
}

// filterOutStrings returns source with all items in removals excluded.
//
// Expected:
//   - source and removals are valid string slices.
//
// Returns:
//   - A new slice containing source items not in removals.
//
// Side effects:
//   - None.
func filterOutStrings(source, removals []string) []string {
	removeSet := make(map[string]struct{}, len(removals))
	for _, s := range removals {
		removeSet[s] = struct{}{}
	}
	filtered := []string{}
	for _, s := range source {
		if _, remove := removeSet[s]; !remove {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// toStringSet converts a string slice to a map for O(1) lookups.
//
// Expected:
//   - items is a valid string slice.
//
// Returns:
//   - map[string]bool with each item set to true.
//
// Side effects:
//   - None.
func toStringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// ErrEntityNotFound is returned when an entity lookup fails.
var ErrEntityNotFound = constantError("entity not found")

// constantError is a string-based error type for package-level sentinel errors.
type constantError string

// Error returns the error message.
//
// Returns:
//   - The error string.
//
// Side effects:
//   - None.
func (e constantError) Error() string { return string(e) }
