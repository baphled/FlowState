package app

// The transform phase and the tool.after point of ADR-012 packet 3b. A transform hook may return args; the
// engine records the rewrite, never the tool.call. A tool.after hook observes: it sees the outcome and cannot
// change it, and its deny is recorded and ignored.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/baphled/flowstate/internal/tool"
)

// After runs the tool.after hooks that match the tool, in file order. Every hook runs: there is nothing to
// deny, so a failure, a deny or an args field is recorded and the outcome is left as it was.
func (g *hookGate) After(ctx context.Context, name string, args json.RawMessage, outcome tool.CallOutcome) error {
	for _, h := range g.after {
		if !hookMatches(h, name) {
			continue
		}
		stdin, ierr := g.document(HookToolAfter, name, args, outcomeDocument(outcome))
		if _, err := g.invoke(ctx, h, stdin, ierr, args); err != nil {
			return err
		}
	}
	return nil
}

// outcomeDocument is the call's outcome as a tool.after hook reads it.
func outcomeDocument(o tool.CallOutcome) map[string]any {
	if o.OK {
		return map[string]any{"ok": true, "result": o.Result}
	}
	return map[string]any{"ok": false, "error": map[string]any{"code": o.Code, "message": o.Message}}
}

// checkRewrite validates the args a transform hook returned. A rewrite that is not a JSON object, or is over
// the 64 KiB hook stdout cap, turns the hook's result into a failure, so on_failure decides. It returns the
// result and the rewritten arguments, which are nil when nothing was rewritten.
func checkRewrite(h Hook, res tool.HookResult) (tool.HookResult, json.RawMessage) {
	if !h.Transform || res.Verdict != tool.HookAllow || len(res.Output.Args) == 0 {
		return res, nil
	}
	rewritten := bytes.TrimSpace(res.Output.Args)
	if len(rewritten) > tool.HookStdoutCap || !bytes.HasPrefix(rewritten, []byte("{")) {
		res.Verdict, res.Failure, res.Output.Args = tool.HookFailure, tool.HookFailureInvalidOutput, nil
		return res, nil
	}
	return res, rewritten
}

// addRewrite records a rewrite in hook.result: the flag, both hashes and the rewritten arguments.
func addRewrite(payload map[string]any, shown, rewritten json.RawMessage) {
	before, after := sha256.Sum256(shown), sha256.Sum256(rewritten)
	payload["args_rewritten"] = true
	payload["args_sha256"] = hex.EncodeToString(before[:])
	payload["rewritten_args_sha256"] = hex.EncodeToString(after[:])
	payload["rewritten_args"] = rewritten
}
