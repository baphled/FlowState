#!/bin/sh
# echo-stdin gate fixture — used by extproc_test.go to pin the wire-
# format invariant that ExtGateRequest.Payload reaches the subprocess
# as a parsed JSON value (object/array/string/number/bool/null) rather
# than the base64-encoded string Go's encoding/json emits for `[]byte`
# fields.
#
# Reads the JSON request on stdin, inspects the payload field's JSON
# type via python's json.load + type(), and emits a pass:false
# response whose `reason` carries that type name. The test asserts
# reason == "dict" — the composed JSON object decoded as a Python
# dict; the bug shape (base64 string) would land as reason == "str".
#
# Implementation note: the python script runs via `python3 -c` so the
# heredoc does not steal the subprocess's stdin (the heredoc form
# `python3 - <<EOF` would route the script onto python's stdin and
# leave nothing for json.load). The `-c` form lets python read the
# parent process's stdin verbatim.
exec python3 -c '
import json, sys
req = json.load(sys.stdin)
payload = req.get("payload")
print(json.dumps({"pass": False, "reason": type(payload).__name__}))
'
