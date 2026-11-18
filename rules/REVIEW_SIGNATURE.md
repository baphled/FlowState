# Reviewer signature and execution boundary

The admission library separates a signed review from proof of an independently
executed review. `review_signature` verifies Ed25519 signatures with a configured
public key; it has no signing API or private key. `review_execution` checks
candidate scope, validity and authenticated harness observations.
`admission_review.authenticate_agent_review` requires both checks.

## Inputs and authority

The trusted caller supplies the public key as 64 lowercase hexadecimal digits,
the current repository, PR number, exact head/base SHA, integer UTC time and an
`authenticated_execution(reference)` adapter. A signature proves possession of
the configured attestor key. It does not identify an agent or prove execution.

The adapter must independently authenticate the execution authority, retrieve its
actual completed run and return observations bound to the signed review payload.
A raw Dropbox download, caller-supplied JSON, different names or model labels do
not meet this contract. There is deliberately no default adapter. Missing keys,
missing authority, invalid evidence and adapter failures prevent acceptance.
The authority must derive actor/session/run identity from authenticated execution,
not copy those fields from the review request. Provision and qualify that authority
separately; the source modules do not claim an installed trusted signer or harness.

The review retains the existing `flowstate-agent-review-v1` fields and additionally
requires `repository`, `base_sha`, `issued_at`, `expires_at` and `execution_ref`.
Its lifetime is at most 3,600 seconds; issuance and expiry are integer Unix UTC
seconds. The execution reference uses the same bounded Dropbox Metadata JSON
reference grammar as review records. Both runs contain nonempty `actor_id`,
`session_id` and `run_id`; each reviewer value differs from its implementer value.
The signed review must bind the current repository, PR, head and base exactly.

The independently authenticated execution observation has these fields:

- `record_type`: `flowstate-review-execution-v1`.
- `metadata_ref`: the signed `execution_ref` read from the authority.
- `state`: `completed`; `verdict`: `PASS`.
- `review_payload_sha256`: lowercase SHA-256 of the canonical signing bytes below.
- `reviewer` and `implementer`: exact signed run observations.

A review can be revalidated while fresh for its unchanged candidate. Its execution
reference and signed payload identify that review. Cross-repository, PR, head or
base replay and expired evidence fail. If the caller supplies `admission_nonce`
in the candidate, the signed review must match it. The merge controller must
atomically reserve/consume a one-use admission bound to the current evidence,
recheck authority availability and prevent concurrent merges or evidence changes.
These pure checks cannot revoke an existing GitHub success status or implement
that durable atomic merge transaction by themselves.

## Canonical signing bytes

The envelope is `attestation`, containing exactly `algorithm: "Ed25519"`,
`key_id` and `signature`. `key_id` is lowercase SHA-256 of the 32 raw public-key
bytes; `signature` is 128 lowercase hexadecimal digits. The signing payload is
all review fields except the top-level `attestation`. Nested fields are retained.

Prefix the encoding with ASCII `FLOWSTATE-REVIEW-V1` and one zero byte. Encode
values recursively using the following grammar. Decimal lengths count bytes or
entries as stated, contain no leading zeroes except `0`, and use ASCII digits.

- Null: ASCII `n`; booleans: ASCII `b0` or `b1`.
- Integer: ASCII `i`, its shortest signed base-10 form, then `;`.
- String: ASCII `s`, UTF-8 byte length, `:`, then strict UTF-8 bytes.
- Array: ASCII `a`, element count, `:`, then encoded values in order.
- Object: ASCII `o`, member count, `:`, then encoded key/value pairs. Keys are
  strings, sorted lexicographically by unsigned UTF-8 bytes.

Floats, non-string keys, invalid Unicode, duplicate JSON keys and non-JSON values
are forbidden. The transport JSON decoder must reject duplicate object keys before
verification; canonicalization receives the resulting restricted value tree.
No Unicode normalization or JSON escaping is applied to signing bytes. A string
`é` is encoded as bytes `73 32 3a c3 a9`. The owning test includes a complete
non-ASCII object vector for independent implementations.

## Verification

From the repository root, with Python 3.11+:

```bash
python3 -m pip install -r scripts/requirements-admission.txt
python3 -B -m unittest discover -s scripts/tests -p 'test_review_*.py' -v
python3 -B -m unittest scripts.tests.test_admission_review -v
```

Synthetic private keys exist only in tests. Actual signer keys, execution records
and review evidence stay outside the source repository.
