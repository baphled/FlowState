package support

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
)

// ChatRequest represents a chat completion request for BDD testing.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []Tool
}

// ChatResponse represents a chat completion response for BDD testing.
type ChatResponse struct {
	Message Message
	Usage   Usage
}

// StreamChunk represents a streaming response chunk for BDD testing.
type StreamChunk struct {
	Content   string
	Done      bool
	Error     error
	EventType string
	ToolCall  *ToolCall
}

// EmbedRequest represents an embedding request for BDD testing.
type EmbedRequest struct {
	Input string
	Model string
}

// Model represents a model definition for BDD testing.
type Model struct {
	ID            string
	Provider      string
	ContextLength int
}

// Usage represents token usage statistics for BDD testing.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Tool represents a tool definition for BDD testing.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]interface{}
}

// ToolCall represents a tool invocation for BDD testing.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// longStreamChunks is the number of chunks emitted when the mock provider is
// in long-response mode. Large enough that tests can reliably observe tokens
// arriving and cancel mid-stream before natural completion.
const longStreamChunks = 500

// longStreamChunkDelay is the per-chunk delay in long-response mode. Small
// enough to keep scenarios fast but large enough that a well-timed cancel
// aborts before all chunks emit.
const longStreamChunkDelay = 2 * time.Millisecond

// MockProvider is a test double for provider implementations in BDD scenarios.
type MockProvider struct {
	name          string
	responses     []string
	embeddings    []float64
	models        []Model
	responseIndex int
	// longStream enables a slow, lengthy streaming response so scenarios
	// covering mid-stream cancellation can observe and interrupt the stream
	// before it naturally completes.
	longStream bool
}

// NewMockProvider creates a new MockProvider with default responses.
//
// Returns:
//   - A configured MockProvider with sensible defaults for testing.
//
// Side effects:
//   - None.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		name:       "mock",
		responses:  []string{"Hello! How can I help you today?"},
		embeddings: make([]float64, 384),
		models: []Model{
			{ID: "mock-model", Provider: "mock", ContextLength: 4096},
		},
	}
}

// Name returns the provider name.
//
// Returns:
//   - The string "mock".
//
// Side effects:
//   - None.
func (m *MockProvider) Name() string {
	return m.name
}

// Stream streams a mock response character by character for the given chat request.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - req contains the chat request with messages to respond to.
//
// Returns:
//   - A channel of StreamChunk values containing the mock response.
//   - nil error (mock never fails to start).
//
// Side effects:
//   - Spawns a goroutine to send chunks on the returned channel.
func (m *MockProvider) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 16)
	if m.longStream {
		go m.streamLong(ctx, ch)
		return ch, nil
	}
	go func() {
		defer close(ch)
		response := m.getContextualResponse(req)
		for i, r := range response {
			select {
			case <-ctx.Done():
				return
			case ch <- StreamChunk{Content: string(r), Done: i == len(response)-1}:
			}
		}
	}()
	return ch, nil
}

// streamLong emits a long sequence of small chunks with a short delay between
// each, respecting context cancellation. It closes the channel on completion
// or cancellation WITHOUT emitting an error chunk: a user-initiated cancel is
// not an error at the mock boundary.
//
// Expected:
//   - ctx may be cancelled mid-stream.
//   - ch is open and will be closed on exit.
//
// Side effects:
//   - Sends up to longStreamChunks StreamChunk values on ch.
func (m *MockProvider) streamLong(ctx context.Context, ch chan<- StreamChunk) {
	defer close(ch)
	for i := range longStreamChunks {
		select {
		case <-ctx.Done():
			return
		case <-time.After(longStreamChunkDelay):
		}
		done := i == longStreamChunks-1
		select {
		case <-ctx.Done():
			return
		case ch <- StreamChunk{Content: "lorem ", Done: done}:
		}
	}
}

// SetLongStream toggles the slow, lengthy streaming mode used by the
// mid-stream cancellation scenario.
//
// Expected:
//   - enabled is true to engage long-mode streaming, false to restore the
//     default short contextual response.
//
// Side effects:
//   - Mutates longStream so subsequent Stream calls emit longStreamChunks
//     chunks with a short per-chunk delay.
func (m *MockProvider) SetLongStream(enabled bool) {
	m.longStream = enabled
}

// LongStreamFullLen returns the total byte length a long-mode stream would
// produce if allowed to complete. Used by tests to verify that a cancelled
// stream's collected content is strictly shorter than the full payload.
//
// Returns:
//   - Expected full byte length of a long-mode stream.
//
// Side effects:
//   - None.
func LongStreamFullLen() int {
	return longStreamChunks * len("lorem ")
}

// getContextualResponse returns a contextual response based on the chat request.
//
// Expected:
//   - req contains the chat request with messages to analyse.
//
// Returns:
//   - A string response, either contextual (if name found) or from the response list.
//
// Side effects:
//   - None.
func (m *MockProvider) getContextualResponse(req ChatRequest) string {
	if name := findNameFromHistory(req.Messages); name != "" {
		return "Your name is " + name + "."
	}
	return m.responses[m.responseIndex%len(m.responses)]
}

// findNameFromHistory extracts a user's name from the message history.
//
// Expected:
//   - messages is a slice of Message values from the chat history.
//
// Returns:
//   - The extracted name if found, or an empty string if not present.
//
// Side effects:
//   - None.
func findNameFromHistory(messages []Message) string {
	if !containsNameQuery(messages) {
		return ""
	}
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		name := extractNameFromContent(msg.Content)
		if name != "" {
			return name
		}
	}
	return ""
}

// containsNameQuery checks whether the message history contains a name query.
//
// Expected:
//   - messages is a slice of Message values to search.
//
// Returns:
//   - true if any user message contains "what is my name", false otherwise.
//
// Side effects:
//   - None.
func containsNameQuery(messages []Message) bool {
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(strings.ToLower(msg.Content), "what is my name") {
			return true
		}
	}
	return false
}

// extractNameFromContent extracts a name from message content.
//
// Expected:
//   - content is a string that may contain "my name is" followed by a name.
//
// Returns:
//   - The extracted name if found, or an empty string if the pattern is not present.
//
// Side effects:
//   - None.
func extractNameFromContent(content string) string {
	lowerContent := strings.ToLower(content)
	if !strings.Contains(lowerContent, "my name is") {
		return ""
	}
	idx := strings.Index(lowerContent, "my name is")
	nameStart := idx + len("my name is")
	if nameStart >= len(content) {
		return ""
	}
	return strings.TrimSpace(content[nameStart:])
}

// Chat performs a synchronous mock chat completion request.
//
// Expected:
//   - The mock provider has at least one configured response.
//
// Returns:
//   - A ChatResponse containing the next mock response.
//   - nil error (mock never fails).
//
// Side effects:
//   - Advances the response index for subsequent calls.
func (m *MockProvider) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	response := m.responses[m.responseIndex%len(m.responses)]
	m.responseIndex++
	return ChatResponse{
		Message: Message{Role: "assistant", Content: response},
		Usage:   Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}, nil
}

// Embed returns mock embeddings for the given input.
//
// Expected:
//   - The mock provider has configured embeddings.
//
// Returns:
//   - A float64 slice of mock embedding values.
//   - nil error (mock never fails).
//
// Side effects:
//   - None.
func (m *MockProvider) Embed(_ context.Context, _ EmbedRequest) ([]float64, error) {
	return m.embeddings, nil
}

// Models returns the available mock models.
//
// Returns:
//   - A slice of mock Model definitions.
//
// Side effects:
//   - None.
func (m *MockProvider) Models() ([]Model, error) {
	return m.models, nil
}

// SetResponses configures the mock responses.
//
// Expected:
//   - responses is a non-empty slice of response strings.
//
// Side effects:
//   - Replaces the provider's internal response list.
func (m *MockProvider) SetResponses(responses []string) {
	m.responses = responses
}

// SetEmbeddings configures the mock embeddings.
//
// Expected:
//   - embeddings is a float64 slice to return from Embed calls.
//
// Side effects:
//   - Replaces the provider's internal embeddings.
func (m *MockProvider) SetEmbeddings(embeddings []float64) {
	m.embeddings = embeddings
}

// SetModels configures the mock models.
//
// Expected:
//   - models is a slice of Model definitions to return from Models calls.
//
// Side effects:
//   - Replaces the provider's internal model list.
func (m *MockProvider) SetModels(models []Model) {
	m.models = models
}

// BDDProviderRegistry is a test registry for BDD model selection scenarios.
type BDDProviderRegistry struct{}

// NewBDDProviderRegistry creates a new BDDProviderRegistry.
//
// Returns:
//   - New BDDProviderRegistry instance.
//
// Side effects:
//   - None.
func NewBDDProviderRegistry() *BDDProviderRegistry {
	return &BDDProviderRegistry{}
}

// List returns the names of all registered providers.
//
// Returns:
//   - Slice of provider names.
//
// Side effects:
//   - None.
func (r *BDDProviderRegistry) List() []string {
	return []string{"ollama", "openai"}
}

// Get returns the provider with the given name.
//
// Expected:
//   - name is a valid provider name.
//
// Returns:
//   - Provider instance and nil error on success.
//
// Side effects:
//   - None.
func (r *BDDProviderRegistry) Get(name string) (provider.Provider, error) {
	switch name {
	case "ollama":
		return &bddMockProvider{name: "ollama", modelList: []provider.Model{
			{ID: "llama3.2", Provider: "ollama", ContextLength: 8192},
			{ID: "mistral", Provider: "ollama", ContextLength: 8192},
		}}, nil
	case "openai":
		return &bddMockProvider{name: "openai", modelList: []provider.Model{
			{ID: "gpt-4o", Provider: "openai", ContextLength: 128000},
			{ID: "gpt-4o-mini", Provider: "openai", ContextLength: 128000},
		}}, nil
	default:
		return nil, fmt.Errorf("provider %q not found", name)
	}
}

// bddMockProvider is a mock provider for BDD testing.
type bddMockProvider struct {
	name      string
	modelList []provider.Model
}

// Name returns the provider name.
//
// Returns:
//   - Provider name string.
//
// Side effects:
//   - None.
func (p *bddMockProvider) Name() string { return p.name }

// Models returns the list of available models.
//
// Returns:
//   - Slice of models and nil error.
//
// Side effects:
//   - None.
func (p *bddMockProvider) Models() ([]provider.Model, error) {
	return p.modelList, nil
}

// Chat performs a chat request (no-op for BDD testing).
//
// Expected:
//   - Request context and chat request are provided.
//
// Returns:
//   - Empty response and nil error.
//
// Side effects:
//   - None.
func (p *bddMockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Stream performs a streaming chat request (no-op for BDD testing).
//
// Expected:
//   - Request context and chat request are provided.
//
// Returns:
//   - Empty stream channel and nil error.
//
// Side effects:
//   - None.
func (p *bddMockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	go func() { close(ch) }()
	return ch, nil
}

// Embed generates embeddings (no-op for BDD testing).
//
// Expected:
//   - Request context and embed request are provided.
//
// Returns:
//   - Nil slice and nil error.
//
// Side effects:
//   - None.
func (p *bddMockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// BDDMockShell is a test shell for BDD provider setup scenarios.
type BDDMockShell struct {
	savedConfig *config.AppConfig
}

// NewBDDMockShell creates a new BDDMockShell.
//
// Returns:
//   - New BDDMockShell instance.
//
// Side effects:
//   - None.
func NewBDDMockShell() *BDDMockShell {
	return &BDDMockShell{}
}

// WriteConfig saves the config to the mock shell.
//
// Expected:
//   - cfg is the configuration to save.
//
// Returns:
//   - Nil error on success.
//
// Side effects:
//   - Stores config in savedConfig field.
func (s *BDDMockShell) WriteConfig(cfg *config.AppConfig) error {
	s.savedConfig = cfg
	return nil
}

// SavedConfig returns the last saved config.
//
// Returns:
//   - Last saved config or nil if none saved.
//
// Side effects:
//   - None.
func (s *BDDMockShell) SavedConfig() *config.AppConfig {
	return s.savedConfig
}
