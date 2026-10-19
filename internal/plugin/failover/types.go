package failover

// ProviderModel represents a provider/model pair for failover health
// tracking. A comparable struct key avoids string-join collisions
// between provider and model ids.
type ProviderModel struct {
	Provider string
	Model    string
}
