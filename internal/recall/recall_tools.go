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
	for i := 0; i < toolsField.Len(); i++ {
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

func loadRecallStore(field reflect.Value) *FileContextStore {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	store, _ := field.Interface().(*FileContextStore)
	return store
}

func loadRecallEmbedder(field reflect.Value) provider.Provider {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	embedder, _ := field.Interface().(provider.Provider)
	return embedder
}

func loadRecallTokenCounter(field reflect.Value) TokenCounter {
	if !field.IsValid() || field.IsNil() {
		return nil
	}
	counter, _ := field.Interface().(TokenCounter)
	return counter
}

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
