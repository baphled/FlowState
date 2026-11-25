package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// Typed errors for the per-call correlated EventSink adapter. The
// adapter fails closed: an invalid or mismatched call identity is
// rejected before any event reaches persistence, and a persistence
// failure makes the adapter sticky so no unacknowledged outcome can
// later be exposed as acknowledged.
var (
	// ErrInvalidCallEventCall rejects a provider ToolCall that fails
	// provider validation before any event is appended.
	ErrInvalidCallEventCall = errors.New("app: invalid tool call for event correlation")

	// ErrCallEventMismatch rejects an event that does not match the
	// adapter's bound call identity (tool name or actor).
	ErrCallEventMismatch = errors.New("app: event does not match correlated call")

	// ErrCallEventOrder rejects a result event before its call start,
	// or a repeated start for the same call.
	ErrCallEventOrder = errors.New("app: correlated call event order violation")

	// ErrCallEventSticky reports that a previous append already failed;
	// every later Append fails so no unacknowledged outcome is exposed.
	ErrCallEventSticky = errors.New("app: event sink sticky after append failure")
)

// NewCallEventSink builds the per-call correlated EventSink adapter for
// one immutable provider ToolCall. The call must satisfy provider
// validation; otherwise ErrInvalidCallEventCall is returned and no
// event is ever appended. The caller keeps ownership of
// call.Arguments: the adapter normalises a copy and never mutates the
// caller's payload.
func NewCallEventSink(writer tool.EventSink, call provider.ToolCall) (tool.EventSink, error) {
	if err := call.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCallEventCall, err)
	}
	args, err := canonicalJSON(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("%w: arguments: %v", ErrInvalidCallEventCall, err)
	}
	return &callEventSink{writer: writer, call: call, args: args}, nil
}

// Append validates and annotates one tool.call or tool.result event for
// the bound call before it reaches the underlying writer. Any identity
// or order violation is rejected before persistence. Once an append
// fails, the adapter is sticky: every later Append returns
// ErrCallEventSticky joined with the original failure, so an
// unacknowledged outcome can never be exposed as acknowledged.
func (s *callEventSink) Append(ctx context.Context, actor, typ string, payload any) (int64, error) {
	if s.sticky != nil {
		return -1, fmt.Errorf("%w: %w", ErrCallEventSticky, s.sticky)
	}
	seq, err := s.appendValidated(ctx, actor, typ, payload)
	if err != nil {
		s.sticky = err
		return -1, err
	}
	return seq, nil
}

func (s *callEventSink) appendValidated(ctx context.Context, actor, typ string, payload any) (int64, error) {
	toolName, fields, ok := callEventPayload(payload)
	if !ok {
		return -1, fmt.Errorf("%w: unexpected payload for %s", ErrCallEventMismatch, typ)
	}
	if toolName != s.call.Name || actor != tool.ActorPrefix+s.call.Name {
		return -1, fmt.Errorf("%w: actor %q tool %q", ErrCallEventMismatch, actor, toolName)
	}
	fields["tool_call_id"] = s.call.ID
	switch typ {
	case tool.TypeToolCall:
		if s.started || s.done {
			return -1, fmt.Errorf("%w: repeated start for %q", ErrCallEventOrder, s.call.ID)
		}
		args, err := normaliseCallArgs(fields["args"])
		if err != nil || args != s.args {
			return -1, fmt.Errorf("%w: arguments for %q", ErrCallEventMismatch, s.call.ID)
		}
		s.started = true
	case tool.TypeToolResult:
		if !s.started || s.done {
			return -1, fmt.Errorf("%w: result for %q", ErrCallEventOrder, s.call.ID)
		}
		s.done = true
	default:
		return -1, fmt.Errorf("%w: event type %s", ErrCallEventMismatch, typ)
	}
	return s.writer.Append(ctx, actor, typ, fields)
}

// callEventPayload extracts the loop's tool name and a mutable copy of
// its payload map; ok is false when payload is not the loop's
// map[string]any shape.
func callEventPayload(payload any) (string, map[string]any, bool) {
	fields, ok := payload.(map[string]any)
	if !ok {
		return "", nil, false
	}
	name, ok := fields["tool"].(string)
	if !ok {
		return "", nil, false
	}
	copied := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		copied[key] = value
	}
	return name, copied, true
}

// normaliseCallArgs reduces the annotated args value (RawMessage, map or
// any) to the canonical form of canonicalJSON so it can be compared with
// the bound call's arguments.
func normaliseCallArgs(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return canonicalJSON(raw)
}

// canonicalJSON is the one canonical form both sides of the argument
// comparison pass through, so equality is semantic: whitespace, object key
// order, HTML escaping (\u003c vs <) and unicode escapes do not matter,
// while values do. Numbers keep their exact text through UseNumber, so
// integers beyond 2^53 are not rounded. Trailing data after the first value
// is rejected, and so is anything whose meaning depends on a decoder's
// choice: invalid UTF-8 (a decoder substitutes U+FFFD) and an object with
// two keys equal under case folding, exact duplicates included (a struct
// decoder matches keys case-insensitively and the last one in document
// order wins, an order the canonical form does not keep). It only builds a
// comparison key; stored payloads are never rewritten.
func canonicalJSON(raw []byte) (string, error) {
	if !utf8.Valid(raw) {
		return "", errors.New("arguments are not valid UTF-8")
	}
	if err := rejectAmbiguousKeys(raw); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return "", err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("trailing data after JSON value")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// rejectAmbiguousKeys walks raw token by token, without recursion, and
// fails when an object holds two keys that fold to the same string.
func rejectAmbiguousKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walker keyWalker
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := walker.step(tok); err != nil {
			return err
		}
	}
}

// step consumes one token: a key is checked against the keys already seen in
// its object, an opening delimiter pushes a frame, a closing one pops it.
func (w *keyWalker) step(tok json.Token) error {
	delim, isDelim := tok.(json.Delim)
	if top := w.top(); top != nil && top.object && top.wantKey && !isDelim {
		key, _ := tok.(string)
		folded := foldKey(key)
		if _, dup := top.seen[folded]; dup {
			return fmt.Errorf("object has two keys equal under case folding: %q", key)
		}
		top.seen[folded] = struct{}{}
		top.wantKey = false
		return nil
	}
	switch {
	case isDelim && (delim == '{' || delim == '['):
		frame := keyFrame{object: delim == '{', wantKey: delim == '{'}
		if frame.object {
			frame.seen = map[string]struct{}{}
		}
		w.stack = append(w.stack, frame)
	case isDelim:
		w.stack = w.stack[:len(w.stack)-1]
		w.valueDone()
	default:
		w.valueDone()
	}
	return nil
}

// top returns the innermost open container, or nil at the top level.
func (w *keyWalker) top() *keyFrame {
	if len(w.stack) == 0 {
		return nil
	}
	return &w.stack[len(w.stack)-1]
}

// valueDone marks a finished value: the next token of an object is a key.
func (w *keyWalker) valueDone() {
	if top := w.top(); top != nil && top.object {
		top.wantKey = true
	}
}

// foldKey maps a key to the smallest member of each rune's simple case
// folding orbit, the equivalence encoding/json uses to match field names.
func foldKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < least {
				least = f
			}
		}
		b.WriteRune(least)
	}
	return b.String()
}
