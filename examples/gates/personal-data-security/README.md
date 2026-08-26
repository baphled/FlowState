# personal-data-security

A standalone FlowState gate that scans gate-request payloads for UK personal
data and either redacts the matches (default) or blocks the request entirely.

## What it detects

Regex-based detection of seven categories:

| Category      | Example                             |
|---------------|-------------------------------------|
| `NI_NUMBER`   | `AB123456C`                         |
| `NHS_NUMBER`  | `123 456 7890`                      |
| `UK_POSTCODE` | `SW1A 1AA`                          |
| `EMAIL`       | `someone@example.co.uk`             |
| `UK_PHONE`    | `020 7946 0958`, `+44 20 7946 0958` |
| `DOB`         | `12/03/1985`, `12-03-1985`          |
| `IBAN`        | `GB29NWBK60161331926819`            |

## What it does NOT detect

**Names.** Regex is unreliable for human names — there is no pattern that
distinguishes "Tayo" (a name) from "Aldi" (a shop) without context. Catching
names properly requires NER (named-entity recognition), which is a future
enhancement. If your swarm regularly handles named individuals and the names
themselves are sensitive, prefer the `block` action and have the upstream
agent handle name redaction explicitly.

## Install

Symlink the gate into your FlowState gates directory:

```bash
ln -s "$(pwd)/examples/gates/personal-data-security" \
      ~/.config/flowstate/gates/personal-data-security
```

## Reference from a swarm manifest

Add an entry under `harness.gates` in your swarm manifest:

```yaml
harness:
  gates:
    - name: pds-output
      kind: ext:personal-data-security
      when: post-member
      target: writer            # the agent whose output to scan
      output_key: final-output  # which key in the agent's output to scan
      policy:
        action: strip           # 'strip' (default) or 'block'
```

## Behaviour

The gate reads a JSON request from stdin with shape:

```json
{
  "payload": "string OR object — both accepted",
  "policy": {"action": "strip"}
}
```

The key `content` is accepted as an alias for `payload`. The action can also
be set via the `PDS_MODE` environment variable (lowest priority after payload
policy).

### `action: strip` (default)

Each match is replaced with `[REDACTED:<CATEGORY>]` and the redacted text is
returned as `output`. The gate still passes.

```bash
$ echo '{"payload":"my NI is AB123456C"}' | ./gate.py
{"pass": true, "output": "my NI is [REDACTED:NI_NUMBER]", "redactions": [{"category": "NI_NUMBER", "original": "AB123456C", "position": 9}]}
```

### `action: block`

Any match fails the gate. The response includes the categories found and up to
five example matches so the upstream agent can be told what to fix.

```bash
$ echo '{"payload":"NHS 123 456 7890 at SW1A 1AA","policy":{"action":"block"}}' | ./gate.py
{"pass": false, "reason": "personal data detected", "categories": ["NHS_NUMBER", "UK_POSTCODE"], "examples": ["123 456 7890", "SW1A 1AA"]}
```

### Object payloads

Object/array payloads are flattened to JSON for scanning. The redacted
`output` is returned as a string — callers that need structured output should
re-parse it themselves.

## Tests

```bash
bash tests.sh
```

Five cases: clean, NI strip, block-on-multiple, object payload, mixed strip.
