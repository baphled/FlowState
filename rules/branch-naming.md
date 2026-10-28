# Branch Naming

FlowState uses a strict branch naming convention so that branch names stay
stable identifiers: they feed CI routing, worktree cleanup after an approved
PR head, and the Dropbox Metadata records that reference attempts. A branch
name is part of the audit trail, not a scratch pad.

## Convention

Every development branch is named:

    <type>/<kebab-case-slug>[-YYYYMMDD]

- `type` is the work category, matching the conventional-commit type where one
  exists.
- `kebab-case-slug` is short, lowercase, hyphen-separated. Use hyphens, never
  underscores or spaces. ASCII only.
- The optional `-YYYYMMDD` date suffix disambiguates repeat attempts at similar
  work (for example `chore/code-modularity-20261028`).

Branches are created from `origin/next` inside the registered bare root
(`~/Projects/FlowState.git`), per the worktree instructions in `AGENTS.md`.
Working directly on `main` or `next` is illegal; every change goes through a
feature branch PR.

## Allowed prefixes

| Prefix      | Use for                                          | Example                              |
|-------------|--------------------------------------------------|--------------------------------------|
| `feature/`  | New behaviour or capability                      | `feature/session-engine`             |
| `fix/`      | Bug fixes                                         | `fix/scroll-offset`                  |
| `chore/`    | Tooling, CI, dependencies, refactors of policy    | `chore/ci-quality-gates-20261028`    |
| `docs/`     | Documentation-only changes                        | `docs/architecture-rules`            |
| `refactor/` | Behaviour-preserving code restructuring          | `refactor/provider-registry`         |
| `test/`     | Test-only additions or migration                  | `test/ginkgo-store-suites`           |
| `perf/`     | Performance work with no behaviour change         | `perf/render-cache`                  |
| `revert/`   | Reverting a merged PR                             | `revert/54-ginkgo-guards`            |

Shared integration branches follow the same scheme (`chore/...`) and are
rebased onto `next`, never merged into feature branches as a substitute for an
accepted dependency.

## Examples

    feature/provider-registry
    fix/broken-tests
    chore/documentation-tools-20261028
    test/ginkgo-cmd-suites

## Anti-examples

    feature_provider-registry      # underscore in the separator
    Feature/ProviderRegistry       # capitals and CamelCase
    my-feature                     # no type prefix
    feature/session_engine         # underscore in slug
    fix/bug                        # uninformative slug
    feature/wip-dont-merge-final   # mutable status in the name
    patch-1                        # default GitHub web-edit name
    feature/final-final-v2         # revision noise; force-push instead

## Rationale

- Traceability: the type prefix mirrors the conventional-commit type used in
  `make ai-commit`, so PR titles, commit messages and branch names line up.
- Worktree automation: `make worktree-new`/`worktree-remove` and the cleanup
  after an approved exact PR head key off branch identity; unstable or
  duplicated names make cleanup unsafe.
- Review triage: a reviewer sees `fix/...` and knows failure coverage matters;
  `docs/...` signals no behavioural risk.
- The optional date suffix keeps repeat policy attempts (as used by the
  2026-10-28 rollout branches) distinct without inventing new prefixes.

## Enforcement

CI validates the source branch of every pull request against
`^(feature|fix|chore|docs|refactor|test|perf|revert)/[a-z0-9]+(-[a-z0-9]+)*(-\d{8})?$`
in `.github/workflows/pr-validation.yml` (validate-branch-name job). PRs from
non-conforming branches fail the check until the branch is renamed
(`git branch -m <old> <new>` then re-push). Long-lived shared branches
(`next`, `main`) are excluded because they are targets, not sources.
