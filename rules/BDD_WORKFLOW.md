# Testing Workflow

Choose the smallest useful test that demonstrates the required behaviour.
Go unit and integration tests are the default. Cucumber/Godog/Gherkin is
prohibited in this repository (owner decision 2026-10-27): do not add
feature files, runner dependencies, or workflow steps.

## Choosing Coverage

| Change | Validation |
|--------|------------|
| Business logic or bug fix | Focused Go test; reproduce the bug before fixing it |
| Package, storage, or protocol boundary | Go integration test using temporary resources or a local test server |
| Shared acceptance flow | Focused Go integration test with clear scenario-style names |
| CI, configuration, or documentation | Validate the affected workflow or configuration; add tests only for behaviour that needs coverage |

Reuse existing tests where they cover the risk. Avoid duplicating a Go assertion
in Gherkin or adding scaffolding just to meet a process requirement.

## Development Loop

1. Define the expected behaviour and identify the relevant existing coverage.
2. For a bug fix, add a regression test and confirm the failure.
3. Make the smallest implementation change and run the focused tests.
4. Run the applicable checks before pushing; keep commits small and reviewable.

## CI Gates

CI runs the Go suite with `go test -race -count=1 -v ./...` on Linux, macOS, and
Windows. Formatting, vet, lint, build, commit validation, and security scanning
remain separate checks. Required job names must remain compatible with branch
protection. All checks and the required independent approval must pass before
merging; do not bypass protection.

```bash
make test                          # Go tests
go test -race -count=1 ./...         # Uncached tests with race detection
make check                         # Format, lint, and test
```

```bash
make test                          # Go tests
go test -race -count=1 ./...         # Uncached tests with race detection
make check                         # Format, lint, and test
```
