package memory

// EntityType represents the allowed types of entities in the knowledge graph.
type EntityType string

// Entity type values used in the knowledge graph.
const (
	EntityTypeAgent   EntityType = "Agent"
	EntityTypeProject EntityType = "Project"
	EntityTypeConcept EntityType = "Concept"
	EntityTypeTool    EntityType = "Tool"
)

// allowedEntityTypes lists the valid entity types.
var allowedEntityTypes = map[EntityType]struct{}{
	EntityTypeAgent:   {},
	EntityTypeProject: {},
	EntityTypeConcept: {},
	EntityTypeTool:    {},
}

// ValidateEntityType reports whether t is a valid entity type.
//
// Expected:
//   - t contains the entity type to validate.
//
// Returns:
//   - True when t is a recognised entity type.
//   - False otherwise.
//
// Side effects:
//   - None.
func ValidateEntityType(t string) bool {
	_, ok := allowedEntityTypes[EntityType(t)]
	return ok
}

// RelationType represents the allowed types of relations in the knowledge graph.
type RelationType string

// Relation type values used in the knowledge graph.
const (
	RelationTypeUses       RelationType = "uses"
	RelationTypeImplements RelationType = "implements"
	RelationTypeRelatedTo  RelationType = "related_to"
	RelationTypeDependsOn  RelationType = "depends_on"
	RelationTypeCreatedBy  RelationType = "created_by"
)

// allowedRelationTypes lists the valid relation types.
var allowedRelationTypes = map[RelationType]struct{}{
	RelationTypeUses:       {},
	RelationTypeImplements: {},
	RelationTypeRelatedTo:  {},
	RelationTypeDependsOn:  {},
	RelationTypeCreatedBy:  {},
}

// ValidateRelationType reports whether t is a valid relation type.
//
// Expected:
//   - t contains the relation type to validate.
//
// Returns:
//   - True when t is a recognised relation type.
//   - False otherwise.
//
// Side effects:
//   - None.
func ValidateRelationType(t string) bool {
	_, ok := allowedRelationTypes[RelationType(t)]
	return ok
}

// ObservationTag represents the allowed tags for observations in the knowledge graph.
type ObservationTag string

// Observation tag values used in the knowledge graph.
const (
	ObservationTagDiscovery   ObservationTag = "DISCOVERY"
	ObservationTagChange      ObservationTag = "CHANGE"
	ObservationTagImplication ObservationTag = "IMPLICATION"
	ObservationTagBehavior    ObservationTag = "BEHAVIOR"
	ObservationTagCapability  ObservationTag = "CAPABILITY"
	ObservationTagLimitation  ObservationTag = "LIMITATION"
)

// allowedObservationTags lists the valid observation tags.
var allowedObservationTags = map[ObservationTag]struct{}{
	ObservationTagDiscovery:   {},
	ObservationTagChange:      {},
	ObservationTagImplication: {},
	ObservationTagBehavior:    {},
	ObservationTagCapability:  {},
	ObservationTagLimitation:  {},
}

// ValidateObservationTag reports whether t is a valid observation tag.
//
// Expected:
//   - t contains the observation tag to validate.
//
// Returns:
//   - True when t is a recognised observation tag.
//   - False otherwise.
//
// Side effects:
//   - None.
func ValidateObservationTag(t string) bool {
	_, ok := allowedObservationTags[ObservationTag(t)]
	return ok
}
