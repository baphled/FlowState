package api

import (
	"encoding/json"
	"net/http"
)

// sseThinking represents a model-reasoning event in a server-sent event
// stream. Wire shape: {"type":"thinking","content":"..."} — matches the
// frontend parser at flowstate-web src/lib/sseEvent.ts (SSEThinkingEvent)
// whose chatStore handler accumulates the text onto the in-flight
// assistant message's thinkingContent for the ThinkingPanel.
type sseThinking struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// writeSSEThinking marshals a model-reasoning fragment as a JSON event and
// writes it as a server-sent event with type "thinking".
//
// Expected: parameters for writeSSEThinking.
// Side effects: None.
func writeSSEThinking(w http.ResponseWriter, flusher http.Flusher, content string) {
	data := sseThinking{Type: "thinking", Content: content}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	writeSSE(w, flusher, string(jsonData))
}
