# Go testing contract

Read the current Dropbox `/FlowState/Dev/governance/RULE-TEST-FRAMEWORK.md` before
work or resumption. Use Ginkgo v2 and Gomega, pinned in go.mod; Cucumber, Godog,
Gherkin, and new plain TestX behavioural functions are forbidden. The ordinary
TestX entry point only registers Gomega failure handling and calls RunSpecs.

Each It/Specify/table-entry body owns one behavioural expectation. Keep required
setup error checks explicit; move shared setup into BeforeEach, not mutable
container initializers. Do not hide extra behavioural assertions in helpers or
bundle unrelated outcomes in composite matchers. The AST gate checks syntax;
independent review checks the meaning and preserved failure coverage.

Specs run in parallel processes and randomized order. Use independent temporary
resources, httptest servers and ports, explicit dependency injection, fresh
fixtures and DeferCleanup. Do not share files/databases, mutate ambient process
state or rely on another spec's results. Serial/Ordered need a separately reviewed
policy change rather than a local waiver. Do not call t.Parallel inside Ginkgo.

Use bounded Eventually/Consistently for observable asynchronous behaviour, with
explicit timeouts/polling and cancellation. No arbitrary time.Sleep, live provider
calls, focused/pending specs or runtime Skip in required coverage. Prefer fakes at
ports and local fixtures. Exercise failures, cancellation and resource cleanup.

## Migration and required checks

Existing unchanged legacy tests continue to execute with go test; migration is
in small package PRs. A changed test file must satisfy the new contract in full.
The guard compares actual PR base to committed HEAD, with rename detection off,
so a rename does not exempt legacy tests. Working-copy edits cannot hide a committed
violation; use explicit `--worktree` only for a precommit preview. Do not delete tests to make the gate pass;
retain the behavioural cases and review coverage changes.

`make test` runs legacy coverage and Ginkgo recursively with process parallelism,
full randomization, race detection, bounded timeout, fail-on-pending and
fail-on-empty. Ginkgo prints the random seed; retain failures in Dropbox Metadata
and reproduce with `--seed`. No retry-until-green gate or Copilot quota dependency.

Run `make check-test-contract BASE_SHA=<actual PR base SHA>`, `make check` and the
required Actions jobs. ginkgolinter additionally checks matcher usage, async
intervals, focused containers and container-state pollution. Passing syntax/lint
checks does not prove that an assertion is meaningful; independent review remains
required. No merge is authorized by this testing change.
