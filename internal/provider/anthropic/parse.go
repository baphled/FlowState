package anthropic

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// feed parses one stdout line. Non-JSON lines and unknown types yield nothing.
func (p *parser) feed(raw []byte) []provider.StreamEvent {
	var l wireLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil
	}
	switch l.Type {
	case "stream_event":
		return p.delta(&l)
	case "assistant":
		return p.assistant(&l)
	case "result":
		return p.result(&l)
	}
	return nil
}

func (p *parser) delta(l *wireLine) []provider.StreamEvent {
	if l.Event == nil || l.Event.Type != "content_block_delta" || l.Event.Delta.Type != "text_delta" {
		return nil
	}
	if l.Event.Delta.Text == "" {
		return nil
	}
	p.textSeen = true
	return []provider.StreamEvent{{Content: l.Event.Delta.Text}}
}

// assistant supplies text only when no delta covered the turn. Lines
// carrying an error field hold Claude Code's synthetic error text, not model
// output, so they are never emitted as content.
func (p *parser) assistant(l *wireLine) []provider.StreamEvent {
	seen := p.textSeen
	p.textSeen = false
	if l.Error == "authentication_failed" {
		p.authFailed = true
	}
	if l.Error != "" || seen || l.Message == nil {
		return nil
	}
	var out []provider.StreamEvent
	for _, c := range l.Message.Content {
		if c.Type == "text" && c.Text != "" {
			out = append(out, provider.StreamEvent{Content: c.Text})
		}
	}
	return out
}

// finishReason maps Claude Code's stop_reason to the provider-neutral finish reason, matching the API-key route:
// a missing, end_turn or stop_sequence reason is a normal stop, max_tokens is truncation, anything else is kept.
func finishReason(stopReason string) string {
	switch stopReason {
	case "", "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	}
	return stopReason
}

func (p *parser) result(l *wireLine) []provider.StreamEvent {
	p.done = true
	if !l.IsError && (l.Subtype == "" || l.Subtype == "success") {
		// A finish event must name a reason; Claude Code sends null for local commands.
		return []provider.StreamEvent{{Kind: provider.StreamEventFinish, FinishReason: finishReason(l.StopReason)}}
	}
	if l.Subtype == "success" {
		l.Subtype = "" // an is_error result is no success, whatever its subtype says
	}
	msg := cmp.Or(strings.TrimSpace(l.Result), l.Subtype, "unknown error")
	if p.authFailed || strings.Trim(string(l.APIErrorStatus), `"`) == "401" {
		return []provider.StreamEvent{{Err: fmt.Errorf("%w (Claude Code said: %s)", ErrNotSignedIn, msg)}}
	}
	return []provider.StreamEvent{{Err: fmt.Errorf("anthropic: %s", msg)}}
}

// endError reports why a stream that ended without a result line failed.
func (p *parser) endError(waitErr error, stderr string) error {
	switch {
	case p.authFailed:
		return ErrNotSignedIn
	case waitErr != nil:
		return fmt.Errorf("anthropic: process failed: %w; stderr: %s", waitErr, tail(stderr))
	default:
		return fmt.Errorf("anthropic: stream ended without a result; stderr: %s", tail(stderr))
	}
}

const stderrTailLimit = 2048

// tail bounds s to its last stderrTailLimit bytes.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > stderrTailLimit {
		s = "..." + s[len(s)-stderrTailLimit:]
	}
	return s
}

func newLineReader(r io.Reader, limit int) *lineReader {
	return &lineReader{r: bufio.NewReader(r), limit: limit}
}

// next returns the next line (possibly partial at EOF) and the read error.
// A line longer than the cap (its newline not counted) fails with ErrLineTooLong.
func (l *lineReader) next() ([]byte, error) {
	var buf []byte
	for {
		chunk, err := l.r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(bytes.TrimSuffix(buf, []byte("\n"))) > l.limit {
			return nil, fmt.Errorf("%w: a stdout line exceeds the %d-byte limit", ErrLineTooLong, l.limit)
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return bytes.TrimSpace(buf), err
		}
	}
}
