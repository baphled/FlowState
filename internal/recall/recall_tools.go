package recall

import (
	"log/slog"
	"reflect"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// RegisterRecallTools creates the recall tools for an engine configuration and appends them to cfg.Tools.
//
// The function expects cfg to be a pointer to an engine.Config value. It reads the
// configuration via reflection so the recall package does not import engine directly.
// Tools whose names already exist in cfg.Tools are not appended a second time, so an
// engine whose tool slice already carries recall tools (the delegate construction
// path) does not advertise duplicate names to the provider.
//
// Expected:
//   - cfg points to a struct with Tools, Store, EmbeddingProvider, TokenCounter, and Manifest fields.
//
// Returns:
//   - The recall tools present on cfg.Tools after the call (newly appended or
//     already present).
//   - Nil when cfg is invalid or the recall dependencies are unavailable.
//
// Side effects:
//   - Appends the recall tools missing from cfg.Tools when the required
//     dependencies are present.
func RegisterRecallTools(cfg any) []tool.Tool {
	configValue := reflect.ValueOf(cfg)
	if !configValue.IsValid() || configValue.Kind() != reflect.Pointer || configValue.IsNil() {
		return nil
	}

	configValue = configValue.Elem()
	if configValue.Kind() != reflect.Struct {
		return nil
	}

	toolsField := configValue.FieldByName("Tools")
	store := loadRecallStore(configValue.FieldByName("Store"))
	embedder := loadRecallEmbedder(configValue.FieldByName("EmbeddingProvider"))
	tokenCounter := loadRecallTokenCounter(configValue.FieldByName("TokenCounter"))
	model := loadRecallModelName(configValue.FieldByName("Manifest"))
	bus := loadRecallEventBus(configValue.FieldByName("EventBus"))

	if !toolsField.IsValid() || !toolsField.CanSet() || toolsField.Kind() != reflect.Slice {
		return nil
	}

	if store == nil || embedder == nil || tokenCounter == nil || model == "" {
		slog.Error("recall tools not registered: missing dependencies",
			"store", store != nil, "embedding_provider", embedder != nil,
			"token_counter", tokenCounter != nil, "embedding_model", model != "")
		return nil
	}

	factory := NewToolFactory(store, embedder, tokenCounter, model, bus)
	return appendMissingRecallTools(toolsField, factory.Tools())
}

// appendMissingRecallTools merges the recall tools into the reflected
// Tools slice, skipping names already present so an engine whose tool
// slice already carries recall tools (the delegate construction path)
// does not advertise duplicate names to the provider.
//
// Expected:
//   - toolsField is a settable slice of tool.Tool values.
//   - recallTools are the factory-built recall tools to merge.
//
// Returns:
//   - The recall tools present on the slice after the merge (newly
//     appended or already present).
//
// Side effects:
//   - Replaces the Tools slice contents when any tool is appended.
func appendMissingRecallTools(toolsField reflect.Value, recallTools []tool.Tool) []tool.Tool {
	currentTools := make([]tool.Tool, 0, toolsField.Len()+len(recallTools))
	existingNames := make(map[string]bool, toolsField.Len())
	for i := range toolsField.Len() {
		if existing, ok := toolsField.Index(i).Interface().(tool.Tool); ok {
			currentTools = append(currentTools, existing)
			existingNames[existing.Name()] = true
		}
	}

	registered := make([]tool.Tool, 0, len(recallTools))
	appended := false
	for _, recallTool := range recallTools {
		registered = append(registered, recallTool)
		if existingNames[recallTool.Name()] {
			continue
		}
		currentTools = append(currentTools, recallTool)
		appended = true
	}

	if !appended {
		return registered
	}

	updatedTools := reflect.MakeSlice(toolsField.Type(), 0, len(currentTools))
	for _, registeredTool := range currentTools {
		updatedTools = reflect.Append(updatedTools, reflect.ValueOf(registeredTool))
	}
	toolsField.Set(updatedTools)

	return registered
}

// loadRecallStore extracts a FileContextStore from a reflected field.
//
// Expected:
//   - field contains a *FileContextStore value or nil.
//
// Returns:
//   - The FileContextStore when present.
//   - Nil when the field is invalid, nil, or of the wrong type.
//
// Side effects:
//   - None.
func loadRecallStore(field reflect.Value) *FileContextStore {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	store, ok := field.Interface().(*FileContextStore)
	if !ok {
		return nil
	}
	return store
}

// loadRecallEmbedder extracts a provider.Provider from a reflected field.
//
// Expected:
//   - field contains a provider.Provider value or nil.
//
// Returns:
//   - The provider when present.
//   - Nil when the field is invalid, nil, or of the wrong type.
//
// Side effects:
//   - None.
func loadRecallEmbedder(field reflect.Value) provider.Provider {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	embedder, ok := field.Interface().(provider.Provider)
	if !ok {
		return nil
	}
	return embedder
}

// loadRecallTokenCounter extracts a TokenCounter from a reflected field.
//
// Expected:
//   - field contains a TokenCounter value or nil.
//
// Returns:
//   - The token counter when present.
//   - Nil when the field is invalid, nil, or of the wrong type.
//
// Side effects:
//   - None.
func loadRecallTokenCounter(field reflect.Value) TokenCounter {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	counter, ok := field.Interface().(TokenCounter)
	if !ok {
		return nil
	}
	return counter
}

// loadRecallModelName extracts the embedding model name from a reflected manifest field.
//
// Expected:
//   - field contains a manifest struct with ContextManagement.EmbeddingModel.
//
// Returns:
//   - The embedding model name.
//   - An empty string when the field is invalid or does not match the expected shape.
//
// Side effects:
//   - None.
func loadRecallModelName(field reflect.Value) string {
	if !field.IsValid() || field.Kind() != reflect.Struct {
		return ""
	}
	management := field.FieldByName("ContextManagement")
	if !management.IsValid() || management.Kind() != reflect.Struct {
		return ""
	}
	model := management.FieldByName("EmbeddingModel")
	if !model.IsValid() || model.Kind() != reflect.String {
		return ""
	}
	return model.String()
}

// loadRecallEventBus extracts an EventBus from a reflected field.
//
// Expected:
//   - field contains an *eventbus.EventBus value or nil.
//
// Returns:
//   - The EventBus when present.
//   - Nil when the field is invalid, nil, or of the wrong type.
//
// Side effects:
//   - None.
func loadRecallEventBus(field reflect.Value) *eventbus.EventBus {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	bus, ok := field.Interface().(*eventbus.EventBus)
	if !ok {
		return nil
	}
	return bus
}
