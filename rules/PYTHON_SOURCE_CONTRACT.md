# Python source quality contract

The current FlowState Dev code and agent-work contracts require focused
responsibilities, bounded code complexity, and one owning test file per
implementation. This guard applies those rules to **changed committed**
Python modules under `scripts/`; unchanged legacy modules are not exempt
when subsequently modified.

## Boundaries

- Production Python modules: at most 250 lines and 35 conditional branch
  points per module.
- Each sync/async function: at most 60 source lines, 40 lexical AST statements,
  and a cyclomatic score of 15 (starting at 1, counting control-flow branches,
  Boolean operands and match cases). Nested function/class/lambda bodies are
  checked as their own scopes rather than charged to the enclosing function.
- Each changed implementation `scripts/name.py` must have one owning test
  `scripts/tests/test_name.py`, present as a regular committed blob.
  The test must contain at least one `test_*` function or method.
  The same rule applies to nested modules and their sibling `tests/`.
- Each changed, deleted or renamed root/nested test module is reconciled with
  its corresponding implementation in the final candidate tree. Deleting or
  renaming an owner test while its implementation survives is fatal. Package
  `__init__.py` files need no owning test.
- Inline suppressions, generated comments, working-tree edits and renames
  do not waive the check. The verifier reviews semantic cohesion separately.

The guard reads the entire changed blob from the candidate Git tree and
compares against the **exact protected base**, with no diff-hunk exemptions.
Deletion and rename effects are included. In CI the checker and its Git-source
helper are loaded from the protected base, so a candidate cannot weaken the
policy implementation it is being checked by. The initial bootstrap is the
single exception because the protected base has no checker yet; that candidate
still requires exact-head independent review before merge. Violations are fatal
and participate in the required Lint & Format → Build dependency. This does not
assess untouched legacy source.

## Local and CI verification

From the repository root, with the true full base SHA:

```bash
make check-python-contract BASE_SHA=<actual-40-character-base-sha>
```

The local target runs its regression cases, then checks the committed source.
CI also executes the candidate regression suite, but enforcement uses the
protected-base checker. Negative fixtures cover overlong and branching
functions/modules, nested scopes, missing/deleted/renamed owners, nested
pairing, orphaned or symlinked files, inline waivers, syntax errors, and a dirty
working copy masking a committed violation.

A green guard demonstrates structural checks only, not good design or
authenticated independent review. Source change, tests, documentation,
Dropbox evidence and distinct verifier requirements remain mandatory.
