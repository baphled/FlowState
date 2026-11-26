package app

import "regexp"

// hookDeniedCode is the error code a tool.result carries when a tool.before hook denied the call.
const hookDeniedCode = "hook_denied"

// hookDenialFixedText is what the model sees for a hook_denied result whose message is not exactly
// "denied by hook <id>": the reason, and any raw JSON, never reach the model.
const hookDenialFixedText = "error: denied by hook"

// hookDenialMessage matches the whole message of a denial; the id charset is the hook loader's.
var hookDenialMessage = regexp.MustCompile(`^denied by hook ([a-z0-9][a-z0-9._-]{0,63})$`)

// hookDenialText is the one text the model sees for a call a hook denied: "error: denied by hook <id>".
// The live loop, the rebuild (rebuildHistory), LoadSessionHistory, replayedBytes and the summariser
// transcript all render a denial through it, so they cannot disagree. It reports false for any
// result that is not a hook denial (a successful result, another error code, a message that is not
// code other than hook_denied), which each caller then renders as before. A hook_denied result is
// always a denial: the text names the hook id and nothing else, and is the fixed
// hookDenialFixedText when the message is not exactly "denied by hook <id>". It only reads the payload.
func hookDenialText(payloadJSON string) (string, bool) {
	p := payloadMap(payloadJSON)
	if ok, isBool := p["ok"].(bool); !isBool || ok {
		return "", false
	}
	failure, isMap := p["error"].(map[string]any)
	if !isMap || failure["code"] != hookDeniedCode {
		return "", false
	}
	message, _ := failure["message"].(string)
	match := hookDenialMessage.FindStringSubmatch(message)
	if match == nil {
		return hookDenialFixedText, true
	}
	return hookDenialFixedText + " " + match[1], true
}
