#!/usr/bin/env bash
# Test harness for personal-data-security gate.
# Each test pipes a request JSON to ./gate.py and asserts the response.
set -euo pipefail

GATE="$(dirname "$0")/gate.py"
PASS=0
FAIL=0

assert_json() {
  local name="$1" expected_jq="$2" req="$3"
  local out
  out=$(printf '%s' "$req" | "$GATE")
  if printf '%s' "$out" | jq -e "$expected_jq" >/dev/null; then
    echo "  PASS  $name"
    PASS=$((PASS+1))
  else
    echo "  FAIL  $name"
    echo "        request : $req"
    echo "        response: $out"
    echo "        expected: $expected_jq"
    FAIL=$((FAIL+1))
  fi
}

# Test 1: Clean text -> pass:true, no output, no redactions
assert_json "clean text" \
  '.pass == true and (has("output") | not) and (has("redactions") | not)' \
  '{"payload":"this is a perfectly fine sentence with no PII"}'

# Test 2: NI number, default policy -> pass:true, redacted, category NI_NUMBER present
assert_json "NI number stripped (default policy)" \
  '.pass == true and (.output | contains("[REDACTED:NI_NUMBER]")) and ([ .redactions[] | select(.category == "NI_NUMBER") ] | length > 0)' \
  '{"payload":"my NI is AB123456C please verify"}'

# Test 3: NHS + postcode + email with action=block -> pass:false, three categories, correct reason
assert_json "block on NHS + postcode + email" \
  '.pass == false and (.categories | length == 3) and (.reason == "personal data detected")' \
  '{"payload":"NHS 123 456 7890 lives at SW1A 1AA email a@b.co","policy":{"action":"block"}}'

# Test 4: Object payload, default policy -> postcode redacted in flattened output
assert_json "object payload flattened and redacted" \
  '.pass == true and (.output | contains("[REDACTED:UK_POSTCODE]")) and ([ .redactions[] | select(.category == "UK_POSTCODE") ] | length > 0)' \
  '{"payload":{"sections":{"A":"child Tayo lives at SW1A 1AA"}}}'

# Test 5: Mixed phone + IBAN + DOB -> all three redacted
assert_json "mixed phone + IBAN + DOB stripped" \
  '.pass == true and (.output | contains("[REDACTED:UK_PHONE]")) and (.output | contains("[REDACTED:IBAN]")) and (.output | contains("[REDACTED:DOB]"))' \
  '{"payload":"call 020 7946 0958 IBAN GB29NWBK60161331926819 DOB 12/03/1985"}'

echo
echo "Results: $PASS passed, $FAIL failed"
[[ $FAIL -eq 0 ]]
