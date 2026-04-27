package slashcommand

// NewModelChoiceForTest exposes the unexported modelChoice payload so
// external test packages can construct the value the /model handler
// expects without leaking the type into the public surface.
//
// Expected:
//   - providerName and modelName mirror the live SetModelPreference
//     arguments.
//
// Returns:
//   - An opaque modelChoice value boxed as any.
//
// Side effects:
//   - None.
func NewModelChoiceForTest(providerName, modelName string) any {
	return modelChoice{Provider: providerName, Model: modelName}
}
