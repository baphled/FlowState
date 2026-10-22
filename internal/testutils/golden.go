package testutils

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/provider"
)

// goldenChunk represents a serialisable form of provider.StreamChunk for JSON I/O.
type goldenChunk struct {
	Content        string                   `json:"content,omitempty"`
	Done           bool                     `json:"done,omitempty"`
	ErrorMessage   string                   `json:"error,omitempty"`
	EventType      string                   `json:"event_type,omitempty"`
	ToolCall       *provider.ToolCall       `json:"tool_call,omitempty"`
	ToolResult     *provider.ToolResultInfo `json:"tool_result,omitempty"`
	DelegationInfo *provider.DelegationInfo `json:"delegation_info,omitempty"`
}

// goldenRecording represents a collection of goldenChunks for JSON serialisation.
type goldenRecording struct {
	Chunks []goldenChunk `json:"chunks"`
}

// ConvertGoldenChunks converts a slice of goldenChunks to provider.StreamChunks.
//
// Expected:
//   - chunks: A slice of serialisable golden chunks.
//
// Returns:
//   - A slice of provider.StreamChunks with deserialised content.
//
// Side effects:
//   - None.
func ConvertGoldenChunks(chunks []goldenChunk) []provider.StreamChunk {
	result := make([]provider.StreamChunk, len(chunks))
	for i := range chunks {
		gc := chunks[i]
		var err error
		if gc.ErrorMessage != "" {
			err = errors.New(gc.ErrorMessage)
		}
		result[i] = provider.StreamChunk{
			Content:        gc.Content,
			Done:           gc.Done,
			Error:          err,
			EventType:      gc.EventType,
			ToolCall:       gc.ToolCall,
			ToolResult:     gc.ToolResult,
			DelegationInfo: gc.DelegationInfo,
		}
	}
	return result
}

// convertToGoldenChunks converts a slice of provider.StreamChunks to goldenChunks for serialisation.
//
// Expected:
//   - chunks: A slice of provider.StreamChunks.
//
// Returns:
//   - A slice of serialisable golden chunks.
//
// Side effects:
//   - None.
func convertToGoldenChunks(chunks []provider.StreamChunk) []goldenChunk {
	result := make([]goldenChunk, len(chunks))
	for i := range chunks {
		chunk := chunks[i]
		result[i] = goldenChunk{
			Content:        chunk.Content,
			Done:           chunk.Done,
			ErrorMessage:   errorString(chunk.Error),
			EventType:      chunk.EventType,
			ToolCall:       chunk.ToolCall,
			ToolResult:     chunk.ToolResult,
			DelegationInfo: chunk.DelegationInfo,
		}
	}
	return result
}

// errorString returns the error message as a string, or empty if err is nil.
//
// Expected:
//   - err: An error value (may be nil).
//
// Returns:
//   - The error message as a string, or empty string if err is nil.
//
// Side effects:
//   - None.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// GoldenPlayer deserialises a golden file into provider.StreamChunks.
type GoldenPlayer struct {
	path string
}

// NewGoldenPlayer creates a new GoldenPlayer for the given golden file path.
//
// Expected:
//   - path: File path to the golden recording JSON file.
//
// Returns:
//   - A new GoldenPlayer instance ready to load the golden file.
//
// Side effects:
//   - None.
func NewGoldenPlayer(path string) *GoldenPlayer {
	return &GoldenPlayer{path: path}
}

// Load reads the golden file and returns the deserialised chunks.
//
// Expected:
//   - The golden file path was set via NewGoldenPlayer.
//
// Returns:
//   - A slice of deserialised provider.StreamChunks and an error if reading or unmarshalling fails.
//
// Side effects:
//   - Reads the file from disk.
func (g *GoldenPlayer) Load() ([]provider.StreamChunk, error) {
	data, err := os.ReadFile(g.path)
	if err != nil {
		return nil, err
	}

	var recording goldenRecording
	if err := json.Unmarshal(data, &recording); err != nil {
		return nil, err
	}

	return ConvertGoldenChunks(recording.Chunks), nil
}

// GoldenRecorder serialises and saves provider.StreamChunks to a golden file.
type GoldenRecorder struct {
	path string
}

// NewGoldenRecorder creates a new GoldenRecorder for the given path.
//
// Expected:
//   - path: File path where the golden recording JSON should be saved.
//
// Returns:
//   - A new GoldenRecorder instance ready to save chunks.
//
// Side effects:
//   - None.
func NewGoldenRecorder(path string) *GoldenRecorder {
	return &GoldenRecorder{path: path}
}

// Save writes the chunks to the golden file in JSON format.
//
// Expected:
//   - chunks: A slice of provider.StreamChunks to serialise.
//
// Returns:
//   - An error if JSON marshalling, directory creation, or file writing fails.
//
// Side effects:
//   - Creates intermediate directories if needed.
//   - Writes a JSON file to disk at the path specified in NewGoldenRecorder.
func (g *GoldenRecorder) Save(chunks []provider.StreamChunk) error {
	golden := convertToGoldenChunks(chunks)
	recording := goldenRecording{Chunks: golden}

	data, err := json.MarshalIndent(recording, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(g.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(g.path, data, 0o600)
}
