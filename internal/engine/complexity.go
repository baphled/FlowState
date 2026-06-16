package engine

import (
	"regexp"
	"strings"
)

// TaskComplexity represents the estimated complexity tier for a session's
// primary task. The tier controls how aggressively the engine enforces todo
// discipline: Simple tasks have no enforcement, Moderate tasks get soft
// nudges, Complex tasks get the hard gate.
type TaskComplexity int

const (
	// ComplexityUnknown is the zero value used before the first user
	// message has been classified. Defaults to Moderate enforcement
	// so unclassified sessions are not left ungated.
	ComplexityUnknown TaskComplexity = iota

	// ComplexitySimple indicates a trivial task — short input, no code,
	// no complexity keywords. No todo enforcement; the agent can make
	// unlimited tool calls without touching its todo list.
	ComplexitySimple

	// ComplexityModerate indicates a standard task — medium-length
	// input or some code/keyword signals. The engine injects todo
	// context as a soft nudge but does not hard-gate tool calls.
	ComplexityModerate

	// ComplexityComplex indicates a demanding task — long input,
	// multiple complexity keywords, or code-heavy content. The full
	// hard gate (TodoStrictMode semantics) is enforced: after
	// todoStrictModeThreshold non-todo tool calls, further non-todo
	// calls are rejected until the model updates its todo list.
	ComplexityComplex
)

// String returns a human-readable name for the complexity tier.
func (c TaskComplexity) String() string {
	switch c {
	case ComplexitySimple:
		return "simple"
	case ComplexityModerate:
		return "moderate"
	case ComplexityComplex:
		return "complex"
	default:
		return "unknown"
	}
}

// complexityKeywords maps to patterns that signal higher task complexity.
// Sourced from heuristic analysis research (tianpan.co, Tutti router):
// refactor, architect, debug, optimise, migrate, integrate, design, etc.
var complexityKeywords = regexp.MustCompile(
	`(?i)\b(refactor|architect|optimi[sz]e|debug|migrat|integrat|` +
		`design|deploy|implement|build|create|orchestrat|` +
		`distribut|concurr|parallel|asynchron|pipeline|` +
		`refactor|rewrit|restructur|overhaul|abstract)\b`,
)

// simpleKeywords maps to patterns that signal lower task complexity.
// Short lookups, summaries, translations, and questions tend to be simple.
var simpleKeywords = regexp.MustCompile(
	`(?i)\b(summari[sz]e|translate|list|show|read|what|explain|` +
		`describe|find|tell|give|quick|brief|overview)\b`,
)

// codeBlockPattern detects fenced code blocks in the user message.
var codeBlockPattern = regexp.MustCompile("(?s)```.*?```")

// ComplexitySignals holds the raw signals extracted from a user message
// that feed into the complexity estimate. Exposed for testing and
// observability.
type ComplexitySignals struct {
	Length            int
	WordCount         int
	HasCode           bool
	ComplexityMatches int
	SimpleMatches     int
}

// EstimateComplexity classifies a user message into a TaskComplexity tier
// using zero-cost heuristics: message length, word count, code-block
// detection, and keyword matching. The estimator runs in sub-millisecond
// time and makes zero LLM calls.
//
// Classification rules (evaluated in order):
//   - Simple: length < 300 chars AND no code blocks AND no complexity
//     keyword matches AND at least one simple keyword match.
//   - Complex: length > 1500 chars OR (code blocks present AND ≥2
//     complexity keyword matches) OR ≥3 complexity keyword matches.
//   - Moderate: everything else (the default for unclassified input).
//
// Expected:
//   - message is the user's first (or primary) message for the session.
//
// Returns:
//   - A TaskComplexity tier and the ComplexitySignals used to derive it.
func EstimateComplexity(message string) (TaskComplexity, ComplexitySignals) {
	signals := ComplexitySignals{
		Length:    len(message),
		WordCount: len(strings.Fields(message)),
		HasCode:   codeBlockPattern.MatchString(message),
	}

	complexityMatches := complexityKeywords.FindAllString(message, -1)
	signals.ComplexityMatches = len(complexityMatches)

	simpleMatches := simpleKeywords.FindAllString(message, -1)
	signals.SimpleMatches = len(simpleMatches)

	if signals.Length < 300 && !signals.HasCode && signals.ComplexityMatches == 0 && signals.SimpleMatches >= 1 {
		return ComplexitySimple, signals
	}

	if signals.Length > 1500 || signals.ComplexityMatches >= 3 || (signals.HasCode && signals.ComplexityMatches >= 2) {
		return ComplexityComplex, signals
	}

	return ComplexityModerate, signals
}

// EnforcesTodoGate reports whether the given complexity tier should enforce
// the hard todo gate (TodoStrictMode semantics). Only Complex tasks trigger
// the hard gate; Simple and Moderate tasks rely on soft nudges.
func (c TaskComplexity) EnforcesTodoGate() bool {
	return c == ComplexityComplex
}

// todoToolNames is the closed set of tool names that count as "todo tools"
// for strict-gate counter resets and work-call tracking exclusion. Any tool
// not in this set is considered a "work" tool call.
var todoToolNames = map[string]struct{}{
	"todowrite":   {},
	"todo_update": {},
	"todo_append": {},
	"todo_insert": {},
}

// isTodoTool reports whether the given tool name is a todo-management tool
// (todowrite, todo_update, todo_append, or todo_insert). Used by the strict
// gate and the stale-continuation work-call counter to distinguish todo
// operations from "real work" tool calls.
func isTodoTool(name string) bool {
	_, ok := todoToolNames[name]
	return ok
}
