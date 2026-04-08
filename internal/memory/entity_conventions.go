package memory

// EntityType represents the allowed types of entities in the knowledge graph.
type EntityType string

const (
	EntityTypeAgent   EntityType = "Agent"
	EntityTypeProject EntityType = "Project"
	EntityTypeConcept EntityType = "Concept"
	EntityTypeTool    EntityType = "Tool"
)

var allowedEntityTypes = map[EntityType]struct{}{
	EntityTypeAgent:   {},
	EntityTypeProject: {},
	EntityTypeConcept: {},
	EntityTypeTool:    {},
}

func ValidateEntityType(t string) bool {
	_, ok := allowedEntityTypes[EntityType(t)]
	return ok
}

// RelationType represents the allowed types of relations in the knowledge graph.
type RelationType string

const (
	RelationTypeUses       RelationType = "uses"
	RelationTypeImplements RelationType = "implements"
	RelationTypeRelatedTo  RelationType = "related_to"
	RelationTypeDependsOn  RelationType = "depends_on"
	RelationTypeCreatedBy  RelationType = "created_by"
)

var allowedRelationTypes = map[RelationType]struct{}{
	RelationTypeUses:       {},
	RelationTypeImplements: {},
	RelationTypeRelatedTo:  {},
	RelationTypeDependsOn:  {},
	RelationTypeCreatedBy:  {},
}

func ValidateRelationType(t string) bool {
	_, ok := allowedRelationTypes[RelationType(t)]
	return ok
}

// ObservationTag represents the allowed tags for observations in the knowledge graph.
type ObservationTag string

const (
	ObservationTagDiscovery   ObservationTag = "DISCOVERY"
	ObservationTagChange      ObservationTag = "CHANGE"
	ObservationTagImplication ObservationTag = "IMPLICATION"
	ObservationTagBehavior    ObservationTag = "BEHAVIOR"
	ObservationTagCapability  ObservationTag = "CAPABILITY"
	ObservationTagLimitation  ObservationTag = "LIMITATION"
)

var allowedObservationTags = map[ObservationTag]struct{}{
	ObservationTagDiscovery:   {},
	ObservationTagChange:      {},
	ObservationTagImplication: {},
	ObservationTagBehavior:    {},
	ObservationTagCapability:  {},
	ObservationTagLimitation:  {},
}

func ValidateObservationTag(t string) bool {
	_, ok := allowedObservationTags[ObservationTag(t)]
	return ok
}
