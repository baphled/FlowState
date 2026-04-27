package swarm

// ManifestBuilder is a fluent assembly helper for swarm manifests, used
// by tests that need a manifest in 5 lines instead of a YAML literal.
// All methods return the builder for chaining; Build() returns the
// composed Manifest by value.
type ManifestBuilder struct {
	manifest Manifest
}

// NewManifestBuilder starts a fresh builder for a swarm with the given
// id. Schema version is pinned to "1.0.0" to match the production
// loader's expectation; override via WithSchemaVersion if needed.
func NewManifestBuilder(id string) *ManifestBuilder {
	return &ManifestBuilder{manifest: Manifest{
		SchemaVersion: SchemaVersionV1,
		ID:            id,
		Members:       []string{},
		Harness:       HarnessConfig{Gates: []GateSpec{}},
	}}
}

// WithSchemaVersion overrides the default schema_version pin.
func (b *ManifestBuilder) WithSchemaVersion(v string) *ManifestBuilder {
	b.manifest.SchemaVersion = v
	return b
}

// WithDescription sets the manifest description.
func (b *ManifestBuilder) WithDescription(desc string) *ManifestBuilder {
	b.manifest.Description = desc
	return b
}

// WithLead sets the lead agent id.
func (b *ManifestBuilder) WithLead(lead string) *ManifestBuilder {
	b.manifest.Lead = lead
	return b
}

// WithMember appends a member id to the manifest's members list.
func (b *ManifestBuilder) WithMember(member string) *ManifestBuilder {
	b.manifest.Members = append(b.manifest.Members, member)
	return b
}

// WithGate appends a GateSpec to the harness. when is one of the
// Lifecycle* constants; target is the member id (empty for swarm-
// scope gates).
func (b *ManifestBuilder) WithGate(name, kind, when, target string) *ManifestBuilder {
	b.manifest.Harness.Gates = append(b.manifest.Harness.Gates, GateSpec{
		Name: name, Kind: kind, When: when, Target: target,
	})
	return b
}

// WithChainPrefix sets the swarm context's chain_prefix.
func (b *ManifestBuilder) WithChainPrefix(prefix string) *ManifestBuilder {
	b.manifest.Context.ChainPrefix = prefix
	return b
}

// Build returns the composed Manifest by value. Subsequent calls to
// builder methods do not mutate the returned value.
func (b *ManifestBuilder) Build() Manifest {
	out := b.manifest
	out.Members = append([]string{}, b.manifest.Members...)
	out.Harness.Gates = append([]GateSpec{}, b.manifest.Harness.Gates...)
	return out
}
