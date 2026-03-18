package funcs

func ExportedNoDoc() {} // want `exported function ExportedNoDoc missing doc comment`

// This does something.
//
// Side effects:
//   - None.
func BadNameStart() {} // want `doc comment for BadNameStart should start with "BadNameStart"`

// ReturnsValue does nothing special.
//
// Side effects:
//   - None.
func ReturnsValue() int { return 0 } // want `exported function ReturnsValue missing Returns: section`

// TakesParams does something with input.
//
// Side effects:
//   - None.
func TakesParams(x int) {} // want `exported function TakesParams missing Expected: section`

// NoSideEffects does something.
func NoSideEffects() {} // want `exported function NoSideEffects missing Side effects: section`

// MissesMultiple does something.
func MissesMultiple(x int) int { return x } // want `exported function MissesMultiple missing Returns: section` `exported function MissesMultiple missing Expected: section` `exported function MissesMultiple missing Side effects: section`

// FullyDocumented validates all sections are present.
//
// Expected:
//   - x must be positive.
//
// Returns:
//   - The doubled value.
//
// Side effects:
//   - None.
func FullyDocumented(x int) int { return x * 2 }

// VoidNoParams demonstrates a void function with no params.
//
// Side effects:
//   - None.
func VoidNoParams() {
}

//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedNoDoc() {} // want `doc comment for unexportedNoDoc should start with "unexportedNoDoc"` `unexported function unexportedNoDoc missing Side effects: section`

// This does something.
//
// Side effects:
//   - None.
//
//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedBadNameStart() {} // want `doc comment for unexportedBadNameStart should start with "unexportedBadNameStart"`

// unexportedReturnsValue does nothing special.
//
// Side effects:
//   - None.
//
//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedReturnsValue() int { return 0 } // want `unexported function unexportedReturnsValue missing Returns: section`

// unexportedTakesParams does something with input.
//
// Side effects:
//   - None.
//
//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedTakesParams(x int) {} // want `unexported function unexportedTakesParams missing Expected: section`

// unexportedNoSideEffects does something.
//
//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedNoSideEffects() {} // want `unexported function unexportedNoSideEffects missing Side effects: section`

// unexportedFullyDocumented validates all sections are present.
//
// Expected:
//   - x must be positive.
//
// Returns:
//   - The doubled value.
//
// Side effects:
//   - None.
//
//lint:ignore U1000 test fixture for analyser - intentionally unused
func unexportedFullyDocumented(x int) int { return x * 2 }
