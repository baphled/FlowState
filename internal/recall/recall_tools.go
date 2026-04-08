package recall

import (
	"reflect"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// RegisterRecallTools creates the recall tools for an engine configuration and appends them to cfg.Tools.
//
// The function expects cfg to be a pointer to an engine.Config value. It reads the
// configuration via reflection so the recall package does not import engine directly.
//
// Expected:
//   - cfg points to a struct with Tools, Store, EmbeddingProvider, TokenCounter, and Manifest fields.
//
// Returns:
//   - The recall tools that were added to cfg.Tools.
//   - Nil when cfg is invalid or the recall dependencies are unavailable.
//
// Side effects:
//   - Appends recall tools to the cfg.Tools slice when the required dependencies are present.
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

	if !toolsField.IsValid() || !toolsField.CanSet() || toolsField.Kind() != reflect.Slice {
		return nil
	}

	if store == nil || embedder == nil || tokenCounter == nil || model == "" {
		return nil
	}

	factory := NewToolFactory(store, embedder, tokenCounter, model)
	recallTools := factory.Tools()
	currentTools := make([]tool.Tool, 0, toolsField.Len()+len(recallTools))
	for i := range toolsField.Len() {
		if existing, ok := toolsField.Index(i).Interface().(tool.Tool); ok {
			currentTools = append(currentTools, existing)
		}
	}
	currentTools = append(currentTools, recallTools...)

	updatedTools := reflect.MakeSlice(toolsField.Type(), 0, len(currentTools))
	for _, registeredTool := range currentTools {
		updatedTools = reflect.Append(updatedTools, reflect.ValueOf(registeredTool))
	}
	toolsField.Set(updatedTools)

	return recallTools
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
