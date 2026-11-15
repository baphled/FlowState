# Change-type PR review budgets

The *protected target* `.env.ci` defines the default and typed changed-line
budgets; PRs never read their own edited policy. The selected budget applies to
added **plus** deleted lines, including tests and docs. Path (10), changed-byte
(128 KiB) and changed-blob (1 MiB) caps remain in force. This is not an exemption
from cohesive scope, actual agent verification, exact-head CI or source guards.

| Conventional title | Branch prefix | Line limit |
| --- | --- | ---: |
| `feat(...)` | `feature/` | 1,200 |
| `fix(...)` | `fix/` | 800 |
| `refactor(...)` | `refactor/` | 900 |
| `test(...)` | `test/` | 900 |
| `docs(...)` | `docs/` | 900 |
| `perf(...)` | `perf/` | 900 |
| `chore(...)` | `chore/` | 600 |
| `revert(...)` | `revert/` | 600 |

The protected selector requires matching title and branch types. Misclassification
is a review finding, not a workaround for the size limit. CI uses the protected
base policy and selector (once rolled out), and `pre-push` uses the corresponding
trusted local base. Merges into `next` re-evaluate the squash subject's change
type. A larger cross-package feature may still require splitting for semantic
cohesion; reclassifying an oversized change without changing its behaviour is
not a valid correction.
