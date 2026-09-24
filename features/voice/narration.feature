Feature: Tool call narration
  As a user listening to spoken agent turns
  I want tool calls narrated as short descriptions
  So that raw command lines and JSON payloads are never read aloud

  @voice @narration
  Scenario: Bash commands are described, never spoken verbatim
    When I narrate a "bash" tool call with input {"command":"go test ./internal/voice/ -count=1"}
    Then the narration does not contain "go test ./internal/voice/ -count=1"
    And the narration mentions "test"

  @voice @narration
  Scenario: File tools speak the action and path
    When I narrate a "read" tool call with input {"filePath":"internal/voice/tts.go"}
    Then the narration mentions "read"
    And the narration mentions "internal/voice/tts.go"

  @voice @narration
  Scenario: Unknown tools fall back to a generic phrase
    When I narrate a "mystery_tool" tool call with input {"query":"find the thing"}
    Then the narration mentions "mystery_tool"

  @voice @narration
  Scenario: Narrations are truncated
    When I narrate a "bash" tool call with input {"command":"grep -r " + "needle "}
    Then the narration is at most 160 characters

  @voice @narration
  Scenario: Code fences are stripped from narrated text
    When I narrate turn text "Here is the code:\n```go\nfmt.Println(1)\n```\ndone"
    Then the narration does not contain "fmt.Println"
    And the narration mentions "Here is the code"

  @voice @narration
  Scenario: Tool results narrate outcomes, not content
    When I narrate a tool_result event with content "PASS\nok  github.com/baphled/flowstate/internal/voice"
    Then the narration mentions "finished"
    And the narration does not contain "ok  github.com"

  @voice @narration
  Scenario: Tool errors narrate a short failure
    When I narrate a tool_error event with content "exit status 1: expected ';' at line 42"
    Then the narration mentions "failed"
    And the narration does not contain "expected ';' at line 42"
