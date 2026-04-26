# Ginkgo conversion — deferred files

The following stdlib `func TestXxx(t *testing.T)` test files were not
converted and remain in their original `testing` package form by
design. Reasons below.

## tools/analyzers/{docblocks,gatingdrift}/

- `tools/analyzers/docblocks/analyzer_test.go`,
  `tools/analyzers/docblocks/testdata/src/funcs/funcs_test.go`,
  `tools/analyzers/gatingdrift/analyzer_test.go` are go/analysis
  static-analyzers under `tools/`, not `internal/`.
- They use `analysistest.Run`, the `golang.org/x/tools/go/analysis`
  testing harness, which is itself driven by `*testing.T` and is the
  upstream-recommended way to test analyzers. Converting to Ginkgo
  would mean wrapping `analysistest.Run` calls in `It(...)`, which
  works but loses the per-fact diagnostic positions the harness
  emits. The cost/benefit doesn't tip toward conversion.
- `funcs_test.go` is testdata for the docblocks analyzer (literal
  Go test fixtures); it is not a real test of FlowState code and
  should not be touched.
