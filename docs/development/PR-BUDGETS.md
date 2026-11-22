# Change-type PR review budgets

The *protected target* `.env.ci` defines the default and typed budgets; PRs never read their own edited
policy. The line budget applies to added **plus** deleted lines, including tests and docs. Changed-byte
(128 KiB) and changed-blob (1 MiB) caps remain in force. This is not an exemption from cohesive scope, actual
agent verification, exact-head CI or source guards.

| Conventional title | Branch prefix | Line limit | Path limit |
| --- | --- | ---: | ---: |
| `feat(...)` | `feature/` | 1,200 | 15 |
| `docs(...)` | `docs/` | 2,000 | 10 |
| `fix`, `refactor`, `test`, `perf`, `chore`, `revert` | the matching prefix | 600 | 10 |

These are the values in `.env.ci` (`FLOWSTATE_PR_MAX_LINES`, `_FEAT`, `_DOCS`, `FLOWSTATE_PR_MAX_FILES`,
`_FEAT`); a kind without its own key uses the default. A test pins them.

An exception is a policy change, not a waiver: on 2026-11-21 the owner approved one pull request above the
bounds (#373), and it was made by raising the bounds in two reviewed policy changes (#372, #376) and
restoring them in a third once it had merged.

The protected selector requires matching title and branch types. Misclassification
is a review finding, not a workaround for the size limit. CI uses the protected
base policy and selector (once rolled out), and `pre-push` uses the corresponding
trusted local base. Merges into `next` re-evaluate the squash subject's change
type. A larger cross-package feature may still require splitting for semantic
cohesion; reclassifying an oversized change without changing its behaviour is
not a valid correction.
