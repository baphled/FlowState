# Ginkgo conversion — deferred files

The following stdlib `func TestXxx(t *testing.T)` test files were not
converted in this refactor pass and remain in their original `testing`
package form. Reasons below.

## internal/tui/uikit/feedback/modal_test.go

- 898 lines, 46 individual `func Test*` functions covering the legacy
  modal package.
- Currently `package feedback` (internal). Sibling tests in the same
  directory (`detail_modal_test.go`, `bordered_overlay_test.go`,
  `confirm_modal_test.go`, `info_modal_test.go`,
  `modal_container_test.go`, `help_modal_test.go`,
  `modal_ginkgo_test.go`) are already `package feedback_test` Ginkgo and
  share `suite_test.go`.
- Reaches into 4 unexported members of `*Modal`: `calculateOpacity()`,
  `fadeStartTime`, `messageRotator`, `spinner`. Promoting these to a
  per-package `export_test.go` is mechanically straightforward (under
  the 10-shim threshold called out in CLAUDE.md) but the conversion
  itself is repetitive and large; deferred to keep this batch focused
  on the higher-leverage files.
- Suggested follow-up: do this as its own batch with `Describe` blocks
  grouped by constructor (`NewErrorModal`, `NewLoadingModal`, etc.).

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
