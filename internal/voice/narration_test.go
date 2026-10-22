package voice

import (
	"strings"
	"testing"
)

func TestNarrateToolCallBashNotVerbatim(t *testing.T) {
	cmd := "go test ./internal/voice/ -count=1"
	got := NarrateToolCall("bash", `{"command":"`+cmd+`"}`)
	if strings.Contains(got, cmd) {
		t.Fatalf("bash command spoken verbatim: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "go") {
		t.Fatalf("expected go mention, got %q", got)
	}
}

func TestNarrateToolCallBashGit(t *testing.T) {
	got := NarrateToolCall("bash", `{"command":"git status --porcelain"}`)
	if !strings.Contains(got, "git") {
		t.Fatalf("expected git mention, got %q", got)
	}
}

func TestNarrateToolCallReadSpeaksPath(t *testing.T) {
	got := NarrateToolCall("read", `{"filePath":"internal/voice/tts.go"}`)
	if !strings.Contains(got, "Reading") || !strings.Contains(got, "internal/voice/tts.go") {
		t.Fatalf("unexpected read narration: %q", got)
	}
}

func TestNarrateToolCallWriteEdit(t *testing.T) {
	if got := NarrateToolCall("write", `{"filePath":"a.txt"}`); !strings.Contains(got, "Writing a.txt") {
		t.Fatalf("unexpected write narration: %q", got)
	}
	if got := NarrateToolCall("edit", `{"filePath":"a.txt"}`); !strings.Contains(got, "Editing a.txt") {
		t.Fatalf("unexpected edit narration: %q", got)
	}
}

func TestNarrateToolCallDelegate(t *testing.T) {
	got := NarrateToolCall("delegate", `{"subagent_type":"QA-Engineer","message":"run tests now please"}`)
	if !strings.Contains(got, "QA-Engineer") || strings.Contains(got, "run tests now") {
		t.Fatalf("unexpected delegate narration: %q", got)
	}
}

func TestNarrateToolCallCoordination(t *testing.T) {
	got := NarrateToolCall("coordination_store", `{"operation":"get","key":"a/b/c"}`)
	if !strings.Contains(got, "get") || !strings.Contains(got, "a/b/c") {
		t.Fatalf("unexpected coordination narration: %q", got)
	}
}

func TestNarrateToolCallUnknownFallback(t *testing.T) {
	got := NarrateToolCall("mystery_tool", `{"query":"find the thing"}`)
	if !strings.Contains(got, "mystery_tool") {
		t.Fatalf("expected tool name fallback, got %q", got)
	}
}

func TestNarrateToolCallMalformedInput(t *testing.T) {
	got := NarrateToolCall("bash", "not json at all")
	if got == "" {
		t.Fatal("expected non-empty narration for malformed input")
	}
}

func TestNarrateToolCallTruncation(t *testing.T) {
	long := strings.Repeat("grep -r needle ", 40)
	got := NarrateToolCall("bash", `{"command":"`+long+`"}`)
	if len(got) > narrationMaxLen {
		t.Fatalf("narration not truncated: len=%d", len(got))
	}
	if got := NarrateToolCall("read", `{"filePath":"`+strings.Repeat("x", 400)+`"}`); len([]rune(got)) > narrationMaxLen {
		t.Fatalf("path narration not truncated: len=%d", len(got))
	}
}

func TestNarrateToolCallNeverSpeaksMultiline(t *testing.T) {
	got := NarrateToolCall("write", `{"filePath":"a.txt","content":"line one\nline two\nline three"}`)
	if strings.Contains(got, "line one") || strings.Contains(got, "\n") {
		t.Fatalf("content leaked into narration: %q", got)
	}
}

func TestNarrateToolOutcome(t *testing.T) {
	if got := NarrateToolOutcome("tool_result"); !strings.EqualFold(got, "finished.") {
		t.Fatalf("unexpected result phrase: %q", got)
	}
	if got := NarrateToolOutcome("tool_error"); !strings.EqualFold(got, "that failed.") {
		t.Fatalf("unexpected error phrase: %q", got)
	}
	if got := NarrateToolOutcome("text"); got != "" {
		t.Fatalf("expected empty phrase for text, got %q", got)
	}
}

func TestPreprocessTTSTurnStripsCodeFences(t *testing.T) {
	in := "Here is the code:\n```go\nfmt.Println(1)\n```\ndone"
	got := PreprocessTTSTurn(in)
	if strings.Contains(got, "fmt.Println") {
		t.Fatalf("code fence leaked: %q", got)
	}
	if !strings.Contains(got, "Here is the code") {
		t.Fatalf("text dropped: %q", got)
	}
}

func TestTurnNarratorSegments(t *testing.T) {
	n := NewTurnNarrator()
	n.AddEvent("text", "", "Building now.")
	n.AddEvent("tool_call", "bash", `{"command":"go build ./..."}`)
	n.AddEvent("tool_result", "", "huge output content")
	n.AddEvent("tool_error", "", "boom")
	n.AddEvent("thinking", "", "internal reasoning")
	got := n.Segments()
	if len(got) != 4 {
		t.Fatalf("expected 4 segments, got %d: %q", len(got), got)
	}
	if strings.Contains(strings.Join(got, " "), "huge output content") {
		t.Fatal("tool result content spoken")
	}
	if strings.Contains(strings.Join(got, " "), "boom") {
		t.Fatal("tool error content spoken")
	}
}
