package methods

// Receiver is a test type for method checks.
type Receiver struct{}

func (r *Receiver) MethodNoDoc() {} // want `exported method MethodNoDoc missing doc comment`

// Does something on the receiver.
//
// Side effects:
//   - None.
func (r *Receiver) BadMethodStart() {} // want `doc comment for BadMethodStart should start with "BadMethodStart"`

// MethodMissingReturns does something on the receiver.
//
// Side effects:
//   - None.
func (r *Receiver) MethodMissingReturns() int { return 0 } // want `exported method MethodMissingReturns missing Returns: section`

// MethodMissingExpected does something on the receiver.
//
// Side effects:
//   - None.
func (r *Receiver) MethodMissingExpected(x int) {} // want `exported method MethodMissingExpected missing Expected: section`

// MethodMissingSideEffects does something on the receiver.
func (r *Receiver) MethodMissingSideEffects() {} // want `exported method MethodMissingSideEffects missing Side effects: section`

// FullyDocMethod validates all sections.
//
// Expected:
//   - x must be positive.
//
// Returns:
//   - The processed value.
//
// Side effects:
//   - None.
func (r *Receiver) FullyDocMethod(x int) int { return x }

//lint:ignore U1000 test fixture for analyser - intentionally unused
func (r *Receiver) unexportedMethod() {}
